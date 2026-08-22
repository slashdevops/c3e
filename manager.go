package c3e

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/valkey-io/valkey-go"
)

// CacheRepository defines the interface for Valkey operations used by CacheManager.
// This uses an interface to allow for easier testing and mocking since you can implement dependency injection.
type CacheRepository interface {
	// Do executes a single command.
	Do(ctx context.Context, cmd valkey.Completed) (resp valkey.ValkeyResult)
	// DoMulti executes multiple commands in a pipeline/batch.
	DoMulti(ctx context.Context, multi ...valkey.Completed) (resps []valkey.ValkeyResult)
	// DoCache executes a single command with server-assisted client-side caching.
	DoCache(ctx context.Context, cmd valkey.Cacheable, ttl time.Duration) (resp valkey.ValkeyResult)
	// DoMultiCache executes multiple commands with server-assisted client-side caching.
	DoMultiCache(ctx context.Context, multi ...valkey.CacheableTTL) (resps []valkey.ValkeyResult)
	// B returns a command builder.
	B() valkey.Builder
}

// CacheManager handles the low-level Valkey operations and dependency tracking.
// It does not implement any high-level policies like stale-while-revalidate or thundering herd protection.
// Those are implemented in SafeCacheManager.
type CacheManager struct {
	client             CacheRepository
	logger             *slog.Logger
	clientCacheEnabled bool
}

// NewCacheManager creates a new core cache manager.
// Parameters:
//   - client: Valkey client/repository to use
//   - clientCacheEnabled: whether to enable server-assisted client-side caching via DoCache
//
// The manager logs through slog.Default(). Inject a scoped (or silent) logger
// via SafeCacheManagerConfig.Logger when constructing a SafeCacheManager.
func NewCacheManager(client CacheRepository, clientCacheEnabled bool) (*CacheManager, error) {
	if client == nil {
		return nil, fmt.Errorf("client cannot be nil")
	}

	return &CacheManager{
		client:             client,
		logger:             slog.Default(),
		clientCacheEnabled: clientCacheEnabled,
	}, nil
}

// Set caches an item and registers its dependencies.
// It performs the following operations in a batch:
// 1. Removes the item from old dependencies (if updated).
// 2. Adds the item to new dependencies.
// 3. Sets the item data with the specified TTL.
//
// Parameters:
//   - ctx: Context for the operation
//   - identifier: The unique identifier for the item being cached
//   - data: The pre-serialized []byte of the CachedItem wrapper
//   - dependencies: A list of CacheIdentifiers representing dependencies
//   - ttl: Time-to-live for the cache entry
func (m *CacheManager) Set(ctx context.Context, identifier CacheIdentifier, data []byte, dependencies []CacheIdentifier, ttl time.Duration) error {
	cKey := cacheKey(identifier)
	dKey := depsForKey(cKey)

	// Create the list of full dependency keys, e.g., "dep:user:123"
	newDepKeys := make([]string, len(dependencies))
	for i, dep := range dependencies {
		newDepKeys[i] = depKey(dep)
	}

	// --- 1. Get old dependencies ---
	// The forward-dependency set is mutable metadata: always read it fresh, even
	// when client-side caching is enabled. A stale (client-cached) read here
	// would compute the wrong "old" dependency set and leave orphaned entries in
	// the reverse-dependency sets.
	builder := m.client.B()
	oldDepKeysResult := m.client.Do(ctx, builder.Smembers().Key(dKey).Build())

	oldDepKeys, err := oldDepKeysResult.AsStrSlice()
	if err != nil && !valkey.IsValkeyNil(err) {
		return fmt.Errorf("%w: %w", ErrGetOldDependencies, err)
	}

	// If key doesn't exist, AsStrSlice returns error but we treat it as empty list
	if valkey.IsValkeyNil(err) {
		oldDepKeys = []string{}
	}

	// Build a map of new dependencies for quick lookup
	newDepsMap := make(map[string]bool, len(newDepKeys))
	for _, k := range newDepKeys {
		newDepsMap[k] = true
	}

	// --- 2. Build commands for batch execution ---
	cmds := make([]valkey.Completed, 0)

	// The dependency-tracking keys (reverse-dep sets and the forward-dep list)
	// are given the same TTL as the cache entry. Otherwise they are SADD'ed with
	// no expiry and, when a cache entry expires naturally instead of being
	// explicitly invalidated, its membership in these sets would linger forever
	// — an unbounded memory leak in Valkey. Each Set refreshes the TTL, so sets
	// that still back a live dependent stay alive while genuinely orphaned ones
	// expire.
	ttlSecs := int64(ttl.Seconds())

	// Remove this cache key from any reverse-deps it no longer needs
	for _, oldKey := range oldDepKeys {
		if !newDepsMap[oldKey] {
			cmds = append(cmds, builder.Srem().Key(oldKey).Member(cKey).Build())
		}
	}

	// Add new dependencies (reverse-dep sets), each bounded by the entry TTL.
	//
	// A reverse-dependency set is SHARED by every entry that depends on the same
	// thing, so an unconditional EXPIRE here lets the last writer shorten it —
	// including below the TTL of an entry already in the set. Jittered TTLs make
	// that routine rather than rare: with 10% jitter on a 12h hard TTL, entries
	// land anywhere in 10.8h–13.2h, so dep:role:R can expire up to 2.4h before an
	// authorization entry that depends on it. In that window Invalidate finds an
	// empty set, cascades to nothing, and reports success — a revoked role keeps
	// working until the dependent entry expires on its own.
	//
	// Two commands give the set the TTL of its longest-lived member and never
	// less. NX sets an expiry only when the key has none, which covers the first
	// dependent and any set whose TTL has since lapsed. GT then raises it only
	// when this entry outlives what is already there. Neither can shorten it.
	//
	// NX is not redundant: GT treats a key with no TTL as having an infinite one
	// and refuses to set it, so GT alone would leave the set persistent forever —
	// exactly the leak the TTL exists to prevent.
	for _, newKey := range newDepKeys {
		cmds = append(cmds, builder.Sadd().Key(newKey).Member(cKey).Build())
		cmds = append(cmds, builder.Expire().Key(newKey).Seconds(ttlSecs).Nx().Build())
		cmds = append(cmds, builder.Expire().Key(newKey).Seconds(ttlSecs).Gt().Build())
	}

	// Set the cached data with TTL
	cmds = append(cmds, builder.Set().Key(cKey).Value(valkey.BinaryString(data)).ExSeconds(ttlSecs).Build())

	// Delete old forward-dep list
	cmds = append(cmds, builder.Del().Key(dKey).Build())

	// Add new forward-dep list if there are dependencies, bounded by the entry TTL
	if len(newDepKeys) > 0 {
		cmds = append(cmds, builder.Sadd().Key(dKey).Member(newDepKeys...).Build())
		cmds = append(cmds, builder.Expire().Key(dKey).Seconds(ttlSecs).Build())
	}

	// --- 3. Execute all commands in a batch ---
	results := m.client.DoMulti(ctx, cmds...)

	// Check for errors in any of the results
	for i, result := range results {
		if err := result.Error(); err != nil {
			return fmt.Errorf("%w: command %d failed: %w", ErrCommandExecution, i, err)
		}
	}

	return nil
}

// Get performs a raw get from Valkey.
// It returns the serialized []byte of the CachedItem wrapper.
// Returns valkey.Nil if the key does not exist.
func (m *CacheManager) Get(ctx context.Context, identifier CacheIdentifier, ttl time.Duration) ([]byte, error) {
	cKey := cacheKey(identifier)

	builder := m.client.B()
	var result valkey.ValkeyResult

	if m.clientCacheEnabled {
		getCmd := builder.Get().Key(cKey).Cache()
		result = m.client.DoCache(ctx, getCmd, ttl)
	} else {
		getCmd := builder.Get().Key(cKey).Build()
		result = m.client.Do(ctx, getCmd)
	}

	val, err := result.ToString()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return nil, ErrCacheMiss
		}

		return nil, fmt.Errorf("%w: %w", ErrCommandExecution, err)
	}

	return []byte(val), nil
}

// Invalidate performs a cascading invalidation for an entity.
// When an entity (e.g., "user:123") is changed or deleted, this function will:
//  1. Delete its own cache entry (cache:user:123).
//  2. Clean up the forward dependencies (deps-for:cache:user:123) by removing this item
//     from the reverse dependency sets of items it depends on.
//  3. Find all cache keys that depend on this entity (read dep:user:123).
//  4. For each dependent, recursively cascade the invalidation downstream.
//  5. Delete all reverse-dependency sets encountered during the cascade.
//
// This function ONLY cascades DOWN the dependency tree (to dependents), not up.
// Example: If Project depends on User, invalidating User will cascade to Project,
// but invalidating Project will NOT cascade to User.
func (m *CacheManager) Invalidate(ctx context.Context, identifier CacheIdentifier) error {
	// Use a queue for breadth-first invalidation (safer than recursion)
	itemKey := cacheKey(identifier)
	level := []string{depKey(identifier)}
	visited := make(map[string]bool)
	builder := m.client.B()

	// Collect all deletion commands
	delCommands := make([]valkey.Completed, 0)

	m.logger.Debug("cache: starting invalidation", "entity_key", identifier.String())

	// --- Step 1: Clean up the root item's forward dependencies ---
	// Remove the root item from the reverse-dependency sets of items it depends on.
	// This prevents stale references when the root item is deleted.
	rootDepsKey := depsForKey(itemKey)
	rootDepsResult := m.client.Do(ctx, builder.Smembers().Key(rootDepsKey).Build())
	if rootDeps, err := rootDepsResult.AsStrSlice(); err == nil {
		for _, dep := range rootDeps {
			// Remove this cache key from the reverse dependency set
			delCommands = append(delCommands, builder.Srem().Key(dep).Member(itemKey).Build())
		}
	}

	// Delete the forward-dependency list for the root item
	delCommands = append(delCommands, builder.Del().Key(rootDepsKey).Build())

	// Delete the cache entry for the root item
	delCommands = append(delCommands, builder.Del().Key(itemKey).Build())

	// --- Step 2: Cascade invalidation to all dependents (downstream) ---
	//
	// Breadth-first, one level at a time, with a single round trip per lookup
	// kind per level. This used to issue one SMEMBERS per node *plus* one per
	// dependent, all sequentially, so a wide dependency graph turned into
	// hundreds of serial round trips on the write path — against a server the
	// read path deliberately fast-fails on. Batching makes the cost O(depth)
	// round trips instead of O(nodes).
	//
	// Deleted dep sets are tracked so a dependent's forward list does not SREM
	// against a set already queued for deletion, which is what the per-node
	// version used the currentDepKey comparison for.
	deletedDepKeys := make(map[string]bool)

	for len(level) > 0 {
		// Drop anything already handled, and claim the rest for this level.
		pending := level[:0:0]

		for _, depK := range level {
			if visited[depK] {
				continue
			}

			visited[depK] = true

			pending = append(pending, depK)
		}

		if len(pending) == 0 {
			break
		}

		// One round trip: every reverse-dependency set in this level.
		memberCmds := make([]valkey.Completed, 0, len(pending))
		for _, depK := range pending {
			memberCmds = append(memberCmds, builder.Smembers().Key(depK).Build())
		}

		dependentsOf := make(map[string][]string, len(pending))

		for i, res := range m.client.DoMulti(ctx, memberCmds...) {
			dependents, err := res.AsStrSlice()
			if err != nil && !valkey.IsValkeyNil(err) {
				return fmt.Errorf("%w for key %s: %w", ErrGetDependents, pending[i], err)
			}

			if valkey.IsValkeyNil(err) {
				dependents = nil
			}

			dependentsOf[pending[i]] = dependents
		}

		// The dep sets themselves go away regardless of what they contained.
		for _, depK := range pending {
			delCommands = append(delCommands, builder.Del().Key(depK).Build())
			deletedDepKeys[depK] = true
		}

		// Collect this level's dependents, de-duplicated: two dep sets in the
		// same level can name the same cache entry.
		seen := make(map[string]bool)
		cKeys := make([]string, 0)

		for _, depK := range pending {
			for _, cKey := range dependentsOf[depK] {
				if seen[cKey] {
					continue
				}

				seen[cKey] = true

				cKeys = append(cKeys, cKey)
			}
		}

		if len(cKeys) == 0 {
			level = nil
			continue
		}

		m.logger.Debug("cache: invalidation level",
			"dep_keys", len(pending), "dependents_found", len(cKeys))

		// Second round trip: every dependent's forward-dependency list.
		fwdCmds := make([]valkey.Completed, 0, len(cKeys))
		for _, cKey := range cKeys {
			fwdCmds = append(fwdCmds, builder.Smembers().Key(depsForKey(cKey)).Build())
		}

		fwdResults := m.client.DoMulti(ctx, fwdCmds...)

		next := make([]string, 0, len(cKeys))

		for i, cKey := range cKeys {
			// Unlink this dependent from the reverse sets it belongs to, so a
			// surviving set does not keep pointing at a deleted entry. Sets
			// already queued for deletion need no SREM.
			if fwdDeps, err := fwdResults[i].AsStrSlice(); err == nil {
				for _, dep := range fwdDeps {
					if !deletedDepKeys[dep] {
						delCommands = append(delCommands, builder.Srem().Key(dep).Member(cKey).Build())
					}
				}
			}

			delCommands = append(delCommands,
				builder.Del().Key(cKey).Build(),
				builder.Del().Key(depsForKey(cKey)).Build(),
			)

			// Anything depending on this dependent belongs to the next level.
			if nextDepKey := depKeyFromCacheKey(cKey); !visited[nextDepKey] {
				next = append(next, nextDepKey)
			}
		}

		level = next
	}

	// Execute all deletions in a batch
	if len(delCommands) == 0 {
		return nil
	}

	// Valkey can handle large DoMulti, but we could batch if needed
	results := m.client.DoMulti(ctx, delCommands...)

	// Check for errors in deletion results
	for i, result := range results {
		if err := result.Error(); err != nil {
			m.logger.Warn("cache deletion error", "command_index", i, "error", err.Error())
			// Deletion errors are typically not critical (key might not exist)
			// but we should still report them
			if !valkey.IsValkeyNil(err) {
				return fmt.Errorf("%w: deletion command %d failed: %w", ErrCommandExecution, i, err)
			}
		}
	}

	return nil
}

// Set is a generic function that encodes typed data and stores it in the cache.
// It handles the encoding based on the specified encoder type and wraps the data
// in a CachedItem before storing it.
//
// Parameters:
//   - ctx: Context for the operation
//   - manager: The CacheManager instance to use
//   - encoderType: The encoder type (JSON or Gob)
//   - identifier: The cache identifier for the item
//   - dependencies: List of dependencies for this cache entry
//   - data: The typed data to cache
//   - ttl: Time-to-live for the cache entry
//
// Returns an error if encoding or caching fails.
func Set[T any](
	ctx context.Context,
	manager *CacheManager,
	encoderType CacheEncoderType,
	identifier CacheIdentifier,
	dependencies []CacheIdentifier,
	data T,
	ttl time.Duration,
) error {
	var serializedData []byte
	var err error

	// Encode based on type
	switch encoderType {
	case CacheEncoderTypeGob:
		serializedData, err = EncodeGob(data)
	case CacheEncoderTypeJSON:
		fallthrough
	default:
		serializedData, err = EncodeJSON(data)
	}

	if err != nil {
		return fmt.Errorf("failed to encode value: %w", err)
	}

	// Wrap in CachedItem with soft TTL
	item := CachedItem{
		Data:      serializedData,
		RefreshAt: time.Now().Add(ttl / 2).Unix(), // Default soft TTL is half of hard TTL
	}

	wrapperData, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to marshal wrapper: %w", err)
	}

	return manager.Set(ctx, identifier, wrapperData, dependencies, ttl)
}

// Get is a generic function that retrieves and decodes typed data from the cache.
// It handles the decoding based on the specified encoder type and unwraps the data
// from the CachedItem wrapper.
//
// Parameters:
//   - ctx: Context for the operation
//   - manager: The CacheManager instance to use
//   - encoderType: The encoder type (JSON or Gob) used when storing the data
//   - identifier: The cache identifier for the item
//   - ttl: Client-side cache TTL (ignored if client cache is disabled)
//
// Returns the decoded data of type T and an error if retrieval or decoding fails.
// Returns ErrCacheMiss if the item is not found in the cache.
func Get[T any](
	ctx context.Context,
	manager *CacheManager,
	encoderType CacheEncoderType,
	identifier CacheIdentifier,
	ttl time.Duration,
) (T, error) {
	var result T

	// Get the raw data from cache
	wrapperData, err := manager.Get(ctx, identifier, ttl)
	if err != nil {
		return result, err
	}

	// Unmarshal the CachedItem wrapper
	var item CachedItem
	if err := json.Unmarshal(wrapperData, &item); err != nil {
		return result, fmt.Errorf("failed to unmarshal wrapper: %w", err)
	}

	// Decode the inner data based on encoder type
	switch encoderType {
	case CacheEncoderTypeGob:
		result, err = DecodeGob[T](item.Data)
		if err != nil {
			return result, fmt.Errorf("failed to decode gob data: %w", err)
		}
	case CacheEncoderTypeJSON:
		fallthrough
	default:
		if err := json.Unmarshal(item.Data, &result); err != nil {
			return result, fmt.Errorf("failed to unmarshal json data: %w", err)
		}
	}

	return result, nil
}

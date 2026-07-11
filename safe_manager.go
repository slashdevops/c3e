package c3e

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/singleflight"
)

type SafeCacheManagerConsumer interface {
	Get(ctx context.Context, identifier CacheIdentifier, dest any, fetcher FetcherFunc) error
	Invalidate(ctx context.Context, identifier CacheIdentifier) error
}

const (
	// MaxSafeCacheManagerQueryTimeout is the maximum allowed timeout for queries in SafeCacheManager.
	MaxSafeCacheManagerQueryTimeout = 500 * time.Millisecond

	// MinSafeCacheManagerQueryTimeout is the minimum allowed timeout for queries in SafeCacheManager.
	MinSafeCacheManagerQueryTimeout = 5 * time.Millisecond

	// DefaultSafeCacheManagerQueryTimeout is the default timeout for queries in SafeCacheManager.
	// Set to 70ms for faster fallback when cache is unavailable while still allowing most cache hits
	DefaultSafeCacheManagerQueryTimeout = 70 * time.Millisecond

	// backgroundRefreshTimeout bounds a detached stale-while-revalidate refresh.
	// The refresh runs on a context.WithoutCancel copy of the request context so
	// it survives the request returning; this timeout keeps it from running
	// unbounded. (Phase 2 will expose this via a functional option.)
	backgroundRefreshTimeout = 30 * time.Second
)

// FetcherFunc is the function signature for fetching data from the primary source.
// It returns:
// - data: The fresh data from your primary source (DB, API, etc.)
// - dependencies: A list of CacheIdentifiers that this data depends on
// - err: Any error encountered during fetching
type FetcherFunc func(ctx context.Context) (data any, dependencies []CacheIdentifier, err error)

// SafeCacheManagerConfig holds configuration for the SafeCacheManager.
type SafeCacheManagerConfig struct {
	Hooks         Hooks            // Optional observability callbacks; the zero value is a no-op
	Logger        *slog.Logger     // Logger for the cache manager; defaults to slog.Default() when nil
	EncoderType   CacheEncoderType // The encoder type to use (JSON or Gob), defaults to JSON
	HardTTL       time.Duration    // The hard expiration time
	SoftTTL       time.Duration    // The time until the data is considered "stale"
	JitterPercent float64          // The max % (0.0 to 1.0) of jitter
	QueryTimeout  time.Duration    // The maximum duration for queries for this cache manager, defaults to 80 ms
}

// SafeCacheManager wraps the core CacheManager with thundering herd
// protection and stale-while-revalidate logic.
type SafeCacheManager struct {
	hooks  Hooks
	cache  *CacheManager
	sfg    *singleflight.Group
	logger *slog.Logger
	cfg    SafeCacheManagerConfig
}

// NewSafeCacheManager creates the high-level, application-facing manager.
func NewSafeCacheManager(cacheManager *CacheManager, config SafeCacheManagerConfig) (*SafeCacheManager, error) {
	if cacheManager == nil {
		return nil, fmt.Errorf("cacheManager cannot be nil")
	}

	// Validate only the library's structural invariants. Business policy
	// bounds (e.g. "hard TTL must be 1–72h") belong to the calling application,
	// not to a general-purpose cache library.
	if config.JitterPercent < 0 || config.JitterPercent >= 1 {
		return nil, fmt.Errorf("JitterPercent must be in [0.0, 1.0)")
	}

	if config.HardTTL <= 0 {
		return nil, fmt.Errorf("HardTTL must be greater than 0")
	}

	if config.SoftTTL < 0 || config.SoftTTL > config.HardTTL {
		return nil, fmt.Errorf("SoftTTL must be between 0 and HardTTL")
	}

	if config.QueryTimeout == 0 {
		config.QueryTimeout = DefaultSafeCacheManagerQueryTimeout
	}

	if config.QueryTimeout < MinSafeCacheManagerQueryTimeout || config.QueryTimeout > MaxSafeCacheManagerQueryTimeout {
		return nil, fmt.Errorf("QueryTimeout must be between %s and %s", MinSafeCacheManagerQueryTimeout, MaxSafeCacheManagerQueryTimeout)
	}

	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// Propagate the resolved logger to the wrapped low-level manager so the
	// whole stack logs through the same (injectable) logger.
	cacheManager.logger = logger

	return &SafeCacheManager{
		cache:  cacheManager,
		sfg:    &singleflight.Group{},
		logger: logger,
		cfg:    config,
		hooks:  config.Hooks,
	}, nil
}

// log returns the configured logger, falling back to slog.Default when nil.
// Managers built directly as a struct literal (e.g. the per-request
// GetWithEncoder path) may not have a logger set; this keeps every logging
// call site panic-free regardless of how the manager was constructed.
func (m *SafeCacheManager) log() *slog.Logger {
	if m.logger != nil {
		return m.logger
	}
	return slog.Default()
}

// Get retrieves an item, using all best-practice policies.
// - dest: A pointer to the variable to unmarshal data into (e.g., &Project{})
// - fetcher: The function to call on a cache miss.
func (m *SafeCacheManager) Get(ctx context.Context, identifier CacheIdentifier, dest any, fetcher FetcherFunc) error {
	cKey := cacheKey(identifier)

	// Report the cache outcome + elapsed time to the OnGet hook on return.
	start := time.Now()
	result := ResultError
	defer func() { m.hooks.onGet(ctx, identifier, result, time.Since(start)) }()

	var wrapperData []byte
	var err error

	// 1. Create a derived context with the specific query timeout
	cacheCtx, cancel := context.WithTimeout(ctx, m.cfg.QueryTimeout)
	defer cancel()

	// 2. Try the raw cache get - use the cacheCtx
	// Use the CacheManager's Get method which handles client-side caching if enabled
	wrapperData, err = m.cache.Get(cacheCtx, identifier, m.cfg.HardTTL)

	// 3. Handle Cache HIT
	if err == nil {
		var item CachedItem
		if err := json.Unmarshal(wrapperData, &item); err != nil {
			m.log().Warn("cache: failed to unmarshal cached wrapper", "key", cKey, "error", err)

			result = ResultMiss // corrupt entry → refetch from source
			return m.blockingFetch(ctx, identifier, dest, fetcher)
		}

		// 2a. Check if STALE (soft TTL expired)
		if time.Now().Unix() > item.RefreshAt {
			// --- CASE B: STALE (Serve stale, refresh in background) ---
			// Launch a NON-BLOCKING refresh.
			//
			// The refresh MUST NOT use the request context: the caller returns
			// as soon as we serve the stale value below, which cancels that
			// context and would abort the in-flight fetch/cache-write, so the
			// value would stay stale until the hard TTL forces a blocking
			// fetch. context.WithoutCancel keeps request-scoped values (e.g.
			// the trace span) while dropping cancellation; a dedicated timeout
			// bounds the detached work.
			bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backgroundRefreshTimeout)
			go func() {
				defer cancel()
				_, _, _ = m.sfg.Do(cKey, func() (any, error) {
					// We only care about errors for logging
					_, err := m.fetchAndCache(bgCtx, identifier, fetcher)
					if err != nil {
						m.log().Warn("cache: background refresh failed", "key", cKey, "error", err)
					}
					m.hooks.onRefresh(bgCtx, identifier, err)

					return nil, nil // Result doesn't matter
				})
			}()
			result = ResultStale
		} else {
			result = ResultHit
		}

		// --- CASE A (Fresh) or B (Stale) ---
		// In both cases, we serve the data we have *right now*.
		return m.unmarshalData(item.Data, dest)
	}

	// 4. Handle TIMEOUT
	// If cache query timeout is reached but main context is still valid, fallback to database
	if ctx.Err() == nil && cacheCtx.Err() != nil {
		m.log().Warn("cache: query timeout reached, falling back to database",
			"key", cKey,
			"timeout", m.cfg.QueryTimeout)
		result = ResultTimeout
		return m.blockingFetch(ctx, identifier, dest, fetcher)
	}

	// 5. Handle Cache MISS
	if err == ErrCacheMiss {
		// --- CASE C: HARD MISS (Fetch, block, and return) ---
		result = ResultMiss
		return m.blockingFetch(ctx, identifier, dest, fetcher)
	}

	// 6. Handle ALL other cache errors (connectivity, network, parsing, etc.)
	// Immediately fallback to database - don't wait or retry
	// This ensures fast recovery when cache is unavailable
	m.log().Warn("cache: error accessing cache, falling back to database",
		"key", cKey,
		"error", err)
	return m.blockingFetch(ctx, identifier, dest, fetcher)
}

// Invalidate invalidates the cache for a given entity.
func (m *SafeCacheManager) Invalidate(ctx context.Context, identifier CacheIdentifier) error {
	start := time.Now()
	err := m.cache.Invalidate(ctx, identifier)
	m.hooks.onInvalidate(ctx, identifier, time.Since(start), err)
	return err
}

// blockingFetch does a synchronous fetch and cache.
func (m *SafeCacheManager) blockingFetch(ctx context.Context, identifier CacheIdentifier, dest any, fetcher FetcherFunc) error {
	cKey := cacheKey(identifier)

	// sfg.Do ensures this block runs only once for a given cKey
	res, err, _ := m.sfg.Do(cKey, func() (any, error) {
		return m.fetchAndCache(ctx, identifier, fetcher)
	})

	if err != nil {
		// do not wrap the error here to preserve its type
		// return fmt.Errorf("cache: failed to fetch data: %w", err)
		return err
	}

	// `res` is the `wrapperData` ([]byte) from fetchAndCache
	var item CachedItem
	if err := json.Unmarshal(res.([]byte), &item); err != nil {
		return fmt.Errorf("cache: failed to unmarshal fetched wrapper: %w", err)
	}

	// Unmarshal the inner data into the user's destination
	return m.unmarshalData(item.Data, dest)
}

// unmarshalData decodes the data using the configured encoder type.
func (m *SafeCacheManager) unmarshalData(data []byte, dest any) error {
	encoderType := m.cfg.EncoderType
	if encoderType == "" {
		encoderType = CacheEncoderTypeJSON
	}

	switch encoderType {
	case CacheEncoderTypeGob:
		buf := bytes.NewReader(data)
		return gob.NewDecoder(buf).Decode(dest)
	case CacheEncoderTypeJSON:
		fallthrough
	default:
		return json.Unmarshal(data, dest)
	}
}

// fetchAndCache is the single-flight function that does the work.
// It returns the serialized wrapper (`[]byte`) to the singleflight group.
func (m *SafeCacheManager) fetchAndCache(ctx context.Context, identifier CacheIdentifier, fetcher FetcherFunc) (any, error) {
	// 1. Get data and dependencies from the primary source
	data, deps, err := fetcher(ctx)
	if err != nil {
		return nil, err
	}

	// 2. Serialize the inner data based on encoder type
	var serializedData []byte
	encoderType := m.cfg.EncoderType
	if encoderType == "" {
		encoderType = CacheEncoderTypeJSON // Default to JSON
	}

	switch encoderType {
	case CacheEncoderTypeGob:
		serializedData, err = EncodeGob(data)
		if err != nil {
			return nil, fmt.Errorf("failed to encode data with gob: %w", err)
		}
	case CacheEncoderTypeJSON:
		fallthrough
	default:
		serializedData, err = json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal data: %w", err)
		}
	}

	// 3. Create the cache wrapper
	item := CachedItem{
		Data:      serializedData,
		RefreshAt: time.Now().Add(m.cfg.SoftTTL).Unix(),
	}

	// 4. Serialize the wrapper
	wrapperData, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal wrapper: %w", err)
	}

	// 5. Apply jitter to hard TTL
	finalTTL := jitterTTL(m.cfg.HardTTL, m.cfg.JitterPercent)

	// 6. Set in cache - use client-side caching if enabled
	// The Set method handles client-side caching internally if enabled in CacheManager
	err = m.cache.Set(ctx, identifier, wrapperData, deps, finalTTL)
	if err != nil {
		// Log but return the data anyway, as we successfully fetched it
		m.log().Warn("cache: failed to set cache", "key", cacheKey(identifier), "error", err)
	}

	// 7. Return the serialized wrapper to singleflight (for blocking waiters)
	return wrapperData, nil
}

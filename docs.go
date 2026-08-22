// Package c3e is a high-performance, dependency-aware caching layer built
// on top of [Valkey] (Redis-compatible).
//
// # Why the name?
//
// The package is named c3e — pronounced "see three ee" — using the same
// numeric-contraction style as well-known community identifiers like
// k8s (Kubernetes), i18n (Internationalization), and l10n
// (Localization). The leading and trailing letters are kept; the digit
// counts the omitted letters in the middle.
//
// It admits two complementary readings, both intended:
//
//  1. As a numeronym for "cache": c · 3 · e, where "ach" is the
//     three-letter middle that the digit replaces. The package
//     implements a cache.
//  2. As an initialism for "Cache with Cascading Expiration Engine" —
//     the expansion called out in the package's [README]: a one-letter
//     "Cache" prefix, the digit 3 standing in for the three-word
//     descriptor (Cache · Cascading · Expiration), and the trailing
//     "Engine".
//
// Both readings point at the same thing — a cache engine with
// cascading expiration.
//
// Go's style guide discourages "container" package names (util, common,
// helpers, misc) — but it accepts widely-understood numeronyms when
// they are documented and disambiguated. This docs.go is that
// documentation.
//
// # Overview
//
// The package provides:
//
//   - Dependency tracking with cascading invalidation when a depended-on
//     entity changes.
//   - Stale-while-revalidate: serve cached data immediately while a
//     background refresh re-fetches behind the scenes.
//   - Thundering-herd protection via singleflight, so only one goroutine
//     fetches data for a given key under concurrent load.
//   - Optional client-side caching (Valkey-tracking) for extreme read
//     performance.
//   - TTL jitter to prevent synchronized cache stampedes.
//   - Type-safe operations via a generic wrapper.
//   - An injectable logger and observability hooks (hit/miss/stale/refresh/
//     invalidate) for metrics and tracing without wrapping the manager.
//
// # Architecture
//
// The package layers from low-level to application-facing:
//
//  1. CacheRepository — abstraction over the Valkey client.
//  2. CacheManager (low-level) — direct Valkey operations and
//     dependency-graph maintenance.
//  3. SafeCacheManager (high-level) — application-facing API enforcing
//     stale-while-revalidate, singleflight, jitter, and best practices.
//  4. TypedSafeCacheManager (high-level, generic) — type-safe wrapper
//     around SafeCacheManager for a specific value type.
//
// # Basic usage
//
//	// Construct the Valkey client and the cache stack.
//	valkeyClient, _ := valkey.NewClient(valkey.ClientOption{
//	    InitAddress:  []string{"localhost:6379"},
//	    DisableRetry: true, // do not queue when the cache is down
//	})
//
//	cacheManager, err := c3e.NewCacheManager(valkeyClient, false)
//	if err != nil {
//	    // handle error
//	}
//
//	safeCacheManager, err := c3e.NewSafeCacheManager(cacheManager, c3e.SafeCacheManagerConfig{
//	    HardTTL:       5 * time.Minute, // absolute expiration
//	    SoftTTL:       3 * time.Minute, // stale-after time
//	    JitterPercent: 0.1,             // 10% TTL jitter
//	})
//	if err != nil {
//	    // handle error
//	}
//
//	// Option A — the standalone generic helper (recommended).
//	identifier := c3e.CacheIdentifier{Type: "project", ID: "123"}
//	project, err := c3e.GetSafe(ctx, safeCacheManager, identifier,
//	    func(ctx context.Context) (Project, []c3e.CacheIdentifier, error) {
//	        p, err := db.GetProject(ctx, "123")
//	        if err != nil {
//	            return Project{}, nil, err
//	        }
//	        deps := []c3e.CacheIdentifier{
//	            {Type: "user", ID: "456"},
//	            {Type: "org", ID: "789"},
//	        }
//	        return p, deps, nil
//	    })
//
//	// Option B — the typed wrapper for repeated Get/Invalidate of one type.
//	projectCache := c3e.NewTypedSafeCacheManager[Project](safeCacheManager)
//	project, err = projectCache.Get(ctx, identifier, fetcherFunc)
//
// # Dependency tracking
//
// Every cached entry can declare a list of dependencies. When any of
// those dependencies is invalidated, the entry is invalidated too:
//
//	// A project depends on its owner (user:456) and organization (org:789).
//	// When user:456 is updated, the project cache is automatically dropped.
//	safeCacheManager.Invalidate(ctx, c3e.CacheIdentifier{Type: "user", ID: "456"})
//
// Cascades are transitive: dependents of dependents are also
// invalidated.
//
// Invalidation semantics to keep in mind:
//
//   - It cascades DOWNWARD only (to dependents), never up to an entry's own
//     dependencies.
//   - It is eventually consistent, not atomic: Set and the cascade run as
//     pipelined commands, so a dependent written during an in-flight cascade
//     may be missed; it self-heals at the next write or via TTL. Design for
//     at-least-once invalidation.
//   - Dependency links only cover items already present in a cached value, so
//     adding a new item to a cached collection is invisible to the graph —
//     invalidate the collection's own key when its membership changes.
//   - The dependency-tracking keys carry a TTL so they cannot leak when an entry
//     expires naturally instead of being invalidated. A reverse-dependency set
//     is shared, so its expiry may only ever be raised, never lowered: it is set
//     with EXPIRE NX when the set has none and raised with EXPIRE GT when a
//     longer-lived dependent joins. A set that could be shortened by the most
//     recent writer would expire before entries still listed in it, and an
//     Invalidate landing in that window would cascade to nothing and report
//     success. Both commands are needed — GT treats a key with no expiry as
//     infinite and would refuse to set one at all.
//   - The cascade is breadth-first and batched one level at a time, so its cost
//     is proportional to the depth of the dependency graph rather than to the
//     number of nodes in it.
//
// # Stale-while-revalidate
//
// SafeCacheManager classifies every read and reports the outcome to the OnGet
// hook as a [Result]:
//
//   - hit   — age ≤ SoftTTL. Served immediately.
//   - stale — SoftTTL < age ≤ HardTTL. Served immediately while a background
//     goroutine refreshes the entry. The refresh runs on a
//     [context.WithoutCancel] copy of the caller's context (bounded by its own
//     timeout) so it survives the caller returning.
//   - miss  — age > HardTTL, the entry is absent, or what was stored no longer
//     decodes (a changed EncoderType, or a cached type whose shape moved during
//     a rolling deploy). A stored value that cannot be read is treated as
//     absent and refetched, because returning the decode error instead would
//     fail every read of that key until its hard TTL expired. The caller blocks while
//     the fetcher runs (under singleflight).
//   - timeout / error — the cache did not answer within QueryTimeout, or
//     returned an error; the caller falls back to the fetcher immediately.
//
// The pattern hides cold-start latency while still keeping data
// reasonably fresh.
//
// # Error handling
//
// The package defines sentinel error values for predictable matching:
//
//   - [ErrCacheMiss] — item not found in the cache.
//   - [ErrCommandExecution] — the underlying Valkey command failed.
//   - [ErrGetOldDependencies] — failed to retrieve the previous
//     dependency list during invalidation.
//   - [ErrGetDependents] — failed to retrieve dependents.
//
// Match them with [errors.Is]:
//
//	identifier := c3e.CacheIdentifier{Type: "user", ID: "123"}
//	data, err := cacheManager.Get(ctx, identifier, 0)
//	if errors.Is(err, c3e.ErrCacheMiss) {
//	    // handle cache miss
//	}
//
// # Key format
//
// Keys follow a hierarchical, prefix-based convention so cache, forward
// dependency, and reverse dependency keys never collide:
//
//   - cache:{type}:{id}        — value entries (e.g. "cache:project:123").
//   - dep:{type}:{id}          — reverse-dependency sets.
//   - deps-for:cache:{type}:{id} — forward-dependency lists.
//
// # Thread safety
//
// All exported operations are safe for concurrent use. SafeCacheManager
// uses singleflight so that, no matter how many goroutines race for the
// same missing key, only one fetcher runs and the others wait for its
// result.
//
// # Best practices
//
//  1. Use SafeCacheManager (or its typed wrapper) in application code,
//     not CacheManager directly.
//  2. Choose SoftTTL at 60-80% of HardTTL for a useful stale window.
//  3. Use 5-10% jitter to break synchronized TTL expirations across
//     replicas.
//  4. Keep dependency lists small and intentional — each entry adds
//     bookkeeping cost on Set and cascade work on Invalidate.
//  5. Prefer specific entity types ("project", "user", "policy") over
//     generic ones ("data", "item").
//  6. Declare dependencies completely — correctness relies on the fetcher
//     returning every entity a value depends on.
//  7. Invalidate a collection's key when its membership changes; inserts are
//     invisible to the dependency graph (see the invalidation semantics above).
//
// # Performance characteristics
//
//   - Get (hit)  — O(1): single Valkey GET.
//   - Get (miss) — O(1) cache work + fetcher time + O(D) dependency
//     writes, where D is the dependency count.
//   - Set        — O(D).
//   - Invalidate — O(N·D), where N is cascade depth and D is the
//     average dependency count at each level.
//
// # Observability
//
// The manager logs via an injectable [slog.Logger]
// (SafeCacheManagerConfig.Logger; defaults to [slog.Default]). Inject a scoped
// logger to namespace cache logs, or a discard handler to silence them — a
// library should never write to your global logger unasked.
//
// For metrics and tracing, attach [Hooks] (SafeCacheManagerConfig.Hooks)
// instead of wrapping the manager:
//
//   - OnGet reports every read outcome ([Result]: hit, stale, miss, timeout,
//     error) and its latency — enough for hit ratio, fallback rate, and
//     latency histograms.
//   - OnRefresh reports background stale-while-revalidate refresh health.
//   - OnInvalidate reports invalidations and their latency.
//
// Any nil callback is skipped, so the zero Hooks is a no-op. Callbacks must be
// non-blocking and safe for concurrent use.
//
// [Valkey]: https://valkey.io
// [README]: ./README.md
package c3e

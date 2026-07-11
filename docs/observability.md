# Observability

`c3e` exposes two seams — an injectable logger and a set of hooks — so you can
wire in logging, metrics, and tracing **without wrapping** the manager.

## Structured logging (injectable)

The manager logs via `log/slog`. By default it uses `slog.Default()`; inject a
scoped or silent logger through the config so the library never writes to your
global logger unasked:

```go
cfg := c3e.SafeCacheManagerConfig{
    HardTTL: time.Hour, SoftTTL: 40 * time.Minute, JitterPercent: 0.1,
    Logger: slog.Default().With("component", "cache"), // or a discard handler to silence
}
```

The resolved logger is propagated to the wrapped low-level `CacheManager`, so
the whole stack logs through the same handler.

## Metrics & tracing hooks

Attach `Hooks` to observe every operation. Any nil callback is skipped, so the
zero value is a no-op. **Callbacks must be non-blocking and safe for concurrent
use** — they run on the request path (and, for `OnRefresh`, on a background
goroutine).

```go
cfg.Hooks = c3e.Hooks{
    // Once per Get. result ∈ hit | stale | miss | timeout | error
    OnGet: func(ctx context.Context, id c3e.CacheIdentifier, result c3e.Result, took time.Duration) {
        cacheRequests.WithLabelValues(id.Type, string(result)).Inc()
        cacheLatency.WithLabelValues(id.Type).Observe(took.Seconds())
    },
    // A background stale-while-revalidate refresh finished (err != nil on failure).
    OnRefresh: func(ctx context.Context, id c3e.CacheIdentifier, err error) { /* ... */ },
    // After an Invalidate (err != nil on failure).
    OnInvalidate: func(ctx context.Context, id c3e.CacheIdentifier, took time.Duration, err error) { /* ... */ },
}
```

### The `Result` enum and the golden signals

`OnGet`'s `Result` maps directly to the cache signals worth alerting on:

| `Result` | Meaning | Signal |
| --- | --- | --- |
| `hit` | served fresh from cache | **hit rate** = `hit` / total |
| `stale` | served stale, refresh triggered | **stale-serve rate** |
| `miss` | not cached; blocked on a fetch | part of **fallback rate** |
| `timeout` | cache slower than `QueryTimeout` | part of **fallback rate** |
| `error` | cache/network error | part of **fallback rate** |

`took` feeds a **latency** histogram. `OnRefresh` errors surface
background-refresh health (stale data that never manages to refresh).

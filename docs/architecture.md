# Architecture

## Three-layer design

`c3e` layers from a low-level Valkey primitive up to an application-facing,
type-safe API. Each layer is optional to peel back.

```mermaid
flowchart TD
    App["Your application"]

    subgraph c3e["c3e"]
        direction TB
        T["<b>TypedSafeCacheManager[T]</b> · GetSafe[T]()<br/><i>compile-time type safety — optional</i>"]
        S["<b>SafeCacheManager</b><br/><i>stale-while-revalidate · singleflight<br/>TTL jitter · query timeout · hooks</i>"]
        C["<b>CacheManager</b><br/><i>data + dependency graph · batched commands</i>"]
        R["<b>CacheRepository</b><br/><i>Valkey/Redis client · optional client-side cache</i>"]
        T --> S --> C --> R
    end

    App -->|"Get / Invalidate"| T
    App -->|"Get / Invalidate"| S
    R --> VK[("Valkey / Redis")]
    S -. "cache miss · stale · error" .-> DB[("Source of truth<br/>DB · API · …")]
```

- **`TypedSafeCacheManager[T]`** / **`GetSafe[T]()`** — a generic wrapper that
  adds compile-time type safety. Optional.
- **`SafeCacheManager`** — the application-facing API. Enforces
  stale-while-revalidate, singleflight thundering-herd protection, TTL jitter,
  a bounded query timeout, and observability hooks. **Use this in application
  code.**
- **`CacheManager`** — the low-level primitive. Direct Valkey operations plus
  dependency-graph maintenance.
- **`CacheRepository`** — the interface over the Valkey client; mock it in
  tests.

## Read path (stale-while-revalidate)

The outcome of every `Get` maps 1:1 to the `Result` reported to the `OnGet`
observability hook (`hit` · `stale` · `miss` · `timeout` · `error`):

```mermaid
flowchart TD
    G(["Get(id, fetcher)"]) --> L{"lookup in cache<br/>(bounded by QueryTimeout)"}

    L -->|"fresh · age ≤ SoftTTL"| H["serve cached value<br/><b>→ hit</b>"]
    L -->|"stale · SoftTTL &lt; age ≤ HardTTL"| ST["serve stale value now<br/><b>→ stale</b>"]
    ST --> BG["refresh in background<br/><i>detached context · single-flighted</i>"] --> UP["update cache"]
    L -->|"not cached"| M["fetch from source · cache · serve<br/><b>→ miss</b>"]
    L -->|"slower than QueryTimeout"| TO["fetch from source · serve<br/><b>→ timeout</b>"]
    L -->|"cache/network error"| E["fetch from source · serve<br/><b>→ error</b>"]

    M -. "concurrent callers for the same key<br/>wait on one fetch (singleflight)" .- ST
```

- **Fresh** and **stale** both serve *immediately* — stale never blocks the
  caller; the refresh is detached (runs on a `context.WithoutCancel` copy of the
  request context, bounded by its own timeout) so it survives the request
  returning.
- **Miss / timeout / error** fall back to the source; concurrent callers for the
  same key are collapsed into a single fetch (thundering-herd protection).

## Cache states

```mermaid
stateDiagram-v2
    direction LR
    [*] --> Fresh: written
    Fresh --> Stale: age exceeds SoftTTL
    Stale --> Expired: age exceeds HardTTL
    Expired --> [*]

    Fresh: FRESH · served immediately
    Stale: STALE · served now + refreshed in background
    Expired: EXPIRED / MISS · blocking fetch from source
```

## Key scheme & the dependency reverse index

Each cached item has a structured identifier:

```go
type CacheIdentifier struct {
    Type string // Entity type (e.g., "user", "project", "order")
    ID   string // Entity ID (e.g., "123", "user-456")
}
```

Three key families model the dependency graph so data, forward, and reverse
keys never collide:

| Key | Meaning |
| --- | --- |
| `cache:<type>:<id>` | the cached data (has a TTL) |
| `deps-for:cache:<type>:<id>` | **forward** list: what this entry depends on |
| `dep:<type>:<id>` | **reverse** set: which entries depend on this entity |

Caching `project:123` with dependencies on `user:456` and `org:789` builds a
reverse index — so changing an entity finds every entry that must be dropped:

```mermaid
graph LR
    P123["cache:project:123"] -->|depends on| U["user:456"]
    P124["cache:project:124"] -->|depends on| U
    DASH["cache:dashboard:1"] -->|depends on| U
    P123 -->|depends on| O["org:789"]

    U -. "reverse set dep:user:456" .-> P123
    U -. "dep:user:456" .-> P124
    U -. "dep:user:456" .-> DASH
```

When `user:456` is invalidated, its reverse set names every dependent, which are
deleted and cascaded — **downward only** (dependents), never up to dependencies:

```mermaid
flowchart TD
    I(["Invalidate(user:456)"]) --> D0["delete cache:user:456<br/>+ clean its forward-dep links"]
    D0 --> Q["read dep:user:456<br/>= entries that depend on user:456"]
    Q --> D1["delete each dependent<br/>e.g. cache:project:123, cache:dashboard:1"]
    D1 --> CAS{"does a dependent have<br/>its own dependents?"}
    CAS -->|yes| Q
    CAS -->|no| DONE(["done"])
```

> ⚠️ **You must invalidate a collection's key on inserts.** Dependency links
> only exist for items *already in* a cached list, so adding a new item to a
> cached collection is invisible to the graph — invalidate the collection's own
> key (or let it expire via TTL) when membership changes. See
> [Limitations](limitations.md).

## Performance characteristics

| Operation | Time complexity | Notes |
| --- | --- | --- |
| `Get` (hit) | O(1) | single Valkey `GET` |
| `Get` (miss) | O(1) + fetch + O(D) | D = dependency count |
| `Set` | O(D) | D = number of dependencies |
| `Invalidate` | O(N × D) | N = cascade depth, D = avg deps per level |

## Thread safety

All exported operations are safe for concurrent use:

- Concurrent reads of the same missing key are coalesced via singleflight, so
  only one fetcher runs.
- Dependency tracking uses Valkey server-side operations.
- No manual locking is required.

```go
// Safe to call concurrently from many goroutines — only ONE fetcher runs.
var wg sync.WaitGroup
for i := 0; i < 100; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        cache.Get(ctx, identifier, &result, fetcher)
    }()
}
wg.Wait()
```

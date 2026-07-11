# Limitations & semantics

Understanding these keeps you out of stale-data trouble — especially with long
TTLs, where a missed invalidation stays wrong for the full hard TTL.

- **Invalidate collection keys on inserts.** The dependency graph only links
  items *already in* a cached value. Adding a new item to a cached
  list/aggregate is invisible to the graph — you must invalidate the
  collection's own key when membership changes (or accept staleness until TTL).
  Updates and deletes of existing members cascade normally.
- **Invalidation is downward-only.** Invalidating an entity drops the entries
  that depend on it, not its own dependencies. For `Project → depends on →
  User`: invalidating `User` drops `Project`; invalidating `Project` does not
  touch `User`.
- **Invalidation is eventually consistent, not atomic.** `Set` and the
  `Invalidate` cascade run as pipelined commands, not a single transaction.
  Under concurrency a dependent written during an in-flight cascade may be
  missed; it self-heals at the next write or via TTL. Design for at-least-once
  invalidation.
- **Dependencies must be declared completely.** Correctness relies on the
  fetcher returning *every* entity a value depends on. A missing dependency
  means that class of change won't invalidate the value.
- **Dependency metadata is TTL-bounded.** Reverse/forward dependency keys expire
  with the entry TTL (refreshed on each write), so they can't leak when an entry
  expires naturally. With heterogeneous per-key TTLs, the metadata tracks the
  most recent write's TTL.
- **Single instance / hash slot.** The dependency keys for one value span
  multiple keys, and pipelined multi-key operations assume they are reachable
  together. Running against Redis Cluster with arbitrary cross-entity
  dependencies is not supported without key hash-tagging.
- **One encoder per key.** A cached value must be read back with the same
  encoder (JSON or Gob) that wrote it. Pointing two managers with different
  encoders at the same key yields a decode error on read.
- **JSON payloads are base64-wrapped.** The `CachedItem` envelope stores the
  payload as `[]byte`, so JSON values are base64-encoded inside the wrapper
  (~33% overhead). Use the Gob encoder for Go-only, size-sensitive workloads.

## Error handling

The package defines sentinel errors for predictable matching:

```go
var (
    ErrCacheMiss          = errors.New("cache miss")
    ErrCommandExecution   = errors.New("Valkey command execution failed")
    ErrGetOldDependencies = errors.New("failed to get old dependencies")
    ErrGetDependents      = errors.New("failed to get dependents")
)
```

Match them with `errors.Is`:

```go
err := cache.Get(ctx, identifier, &result, fetcher)
if err != nil {
    if errors.Is(err, c3e.ErrCacheMiss) {
        // handle a cache miss specifically
    } else {
        // handle other errors
    }
}
```

`SafeCacheManager` automatically falls back to the fetcher (source of truth) on
a cache **timeout**, **connection error**, **parse error**, or any other Valkey
error — so you rarely need to handle cache failures yourself. The outcome is
still reported to the [`OnGet` hook](observability.md) as `timeout` or `error`.

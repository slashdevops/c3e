# Configuration

`SafeCacheManager` is configured with a `SafeCacheManagerConfig`:

| Option | Type | Description | Default | Range |
| --- | --- | --- | --- | --- |
| **HardTTL** | `time.Duration` | Absolute expiration time | *required* | `> 0` |
| **SoftTTL** | `time.Duration` | Age at which data is considered "stale" | *required* | `0 – HardTTL` |
| **JitterPercent** | `float64` | Max TTL jitter, as a fraction | *required* | `0.0 – <1.0` |
| **EncoderType** | `CacheEncoderType` | Serialization format | `JSON` | `JSON`, `Gob` |
| **QueryTimeout** | `time.Duration` | Max cache lookup duration before falling back | `70ms` | `5ms – 500ms` |
| **Logger** | `*slog.Logger` | Logger for the manager | `slog.Default()` | — |
| **Hooks** | `Hooks` | Observability callbacks | zero value = no-op | — |

> The library validates only these **structural** invariants. Business-policy
> bounds (for example "hard TTL must be 1–72h") are the calling application's
> responsibility — validate your configuration before constructing the manager.

The constructor returns an error if `HardTTL <= 0`, if `SoftTTL` is outside
`[0, HardTTL]`, if `JitterPercent` is outside `[0.0, 1.0)`, or if a non-zero
`QueryTimeout` falls outside `[5ms, 500ms]`. A zero `QueryTimeout` is replaced
with the 70ms default.

## Choosing values

✅ **Do:**

- Set `SoftTTL` to **60–80% of `HardTTL`** for a useful stale-while-revalidate
  window.
- Use **5–10% jitter** to break synchronized TTL expirations across replicas.
- Keep `QueryTimeout` **low** (the 70ms default) so the cache never becomes a
  latency bottleneck — a slow lookup falls back to the source.
- Set `DisableRetry: true` on the Valkey client so commands fail fast instead of
  queueing when the cache is down.

❌ **Don't:**

- Set `SoftTTL == HardTTL` — that disables stale-while-revalidate (nothing is
  ever served stale; expiry becomes a hard miss).
- Use a very high `QueryTimeout` — it defeats the fast-fallback design.

## Encoder selection

- **JSON** (default) — interoperable and debuggable. Because the `CachedItem`
  envelope stores the payload as `[]byte`, JSON values are base64-encoded inside
  the wrapper (~33% size overhead).
- **Gob** — compact and fast for Go-only workloads. Not portable across
  languages.

A value must always be read back with the **same encoder** that wrote it. See
[Limitations](limitations.md).

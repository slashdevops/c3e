# Best practices & troubleshooting

## Do

1. **Use `SafeCacheManager`** (or the typed wrapper) in application code — not
   `CacheManager` directly.
2. **Set `SoftTTL` to 60–80% of `HardTTL`** for a useful stale window.
3. **Use 5–10% jitter** to prevent synchronized TTL expirations.
4. **Keep dependency lists small** (< ~10 items) and intentional — each one adds
   bookkeeping on `Set` and cascade work on `Invalidate`.
5. **Use specific entity types** (`"project"`, `"user"`) over generic ones
   (`"data"`, `"item"`).
6. **Set `DisableRetry: true`** on the Valkey client so it fails fast instead of
   queueing when the cache is down.
7. **Declare dependencies completely** — correctness relies on the fetcher
   returning every entity a value depends on.
8. **Prefer type-safe operations** (`GetSafe[T]` or `TypedSafeCacheManager[T]`).
9. **Monitor cache metrics** via [hooks](observability.md) — hit rate, latency,
   fallback rate, refresh health.
10. **Test against a real Valkey** in integration tests.

## Don't

1. **Don't cache everything** — cache expensive or hot reads, not everything.
2. **Don't set `SoftTTL == HardTTL`** — it disables stale-while-revalidate.
3. **Don't use a very high `QueryTimeout`** — it defeats fast fallback.
4. **Don't skip dependencies** — a missing dependency means that class of change
   never invalidates the value.
5. **Don't forget to invalidate a collection key on inserts** — see
   [Limitations](limitations.md).
6. **Don't disable jitter** — it invites synchronized stampedes.
7. **Don't point two managers with different encoders at the same key** — a value
   must be read back with the encoder that wrote it.

## Troubleshooting

### Cache always misses

*High database load, hit rate near zero.*

- Verify Valkey is running: `valkey-cli ping`.
- Check the connection string / credentials.
- Ensure `DisableRetry: true` is set on the client.
- Check that TTLs are not too short.

### Stale data served indefinitely

*Background refreshes never seem to happen.*

- Verify the fetcher isn't returning errors silently — wire up
  [`OnRefresh`](observability.md).
- Check logs for `cache: background refresh failed`.
- Ensure `SoftTTL < HardTTL` (otherwise there is no stale window).

### Slow cache responses

*High p99 latency, timeouts.*

- Lower `QueryTimeout` (default 70ms) so slow lookups fall back sooner.
- Reduce the dependency count per item.
- Check Valkey server health and network latency.

### Valkey memory growing

- Verify TTLs are set (not zero).
- Reduce `HardTTL` for less critical data.
- Enable a Valkey eviction policy, e.g. `maxmemory-policy allkeys-lru`.

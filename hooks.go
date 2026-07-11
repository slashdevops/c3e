package c3e

import (
	"context"
	"time"
)

// Result is the outcome of a SafeCacheManager.Get, reported to Hooks.OnGet.
type Result string

const (
	// ResultHit: served fresh from the cache.
	ResultHit Result = "hit"
	// ResultStale: served stale data; a background refresh was triggered.
	ResultStale Result = "stale"
	// ResultMiss: not in cache (or corrupt); value fetched from the source.
	ResultMiss Result = "miss"
	// ResultTimeout: the cache did not respond within QueryTimeout; fell back
	// to the source.
	ResultTimeout Result = "timeout"
	// ResultError: a cache error occurred; fell back to the source.
	ResultError Result = "error"
)

// Hooks are optional observability callbacks invoked on cache events. Any nil
// field is skipped, so a zero Hooks is a no-op.
//
// Callbacks MUST be safe for concurrent use and MUST NOT block — do cheap
// metric/log work or hand off to a channel. They let a caller emit metrics or
// traces (e.g. cache hit ratio, latency, refresh health) without wrapping the
// manager.
type Hooks struct {
	// OnGet is invoked once per Get with the cache outcome and elapsed time.
	OnGet func(ctx context.Context, id CacheIdentifier, result Result, took time.Duration)

	// OnRefresh is invoked when a background stale-while-revalidate refresh
	// finishes; err is non-nil if the refresh failed.
	OnRefresh func(ctx context.Context, id CacheIdentifier, err error)

	// OnInvalidate is invoked after an Invalidate completes; err is non-nil on
	// failure.
	OnInvalidate func(ctx context.Context, id CacheIdentifier, took time.Duration, err error)
}

func (h Hooks) onGet(ctx context.Context, id CacheIdentifier, r Result, took time.Duration) {
	if h.OnGet != nil {
		h.OnGet(ctx, id, r, took)
	}
}

func (h Hooks) onRefresh(ctx context.Context, id CacheIdentifier, err error) {
	if h.OnRefresh != nil {
		h.OnRefresh(ctx, id, err)
	}
}

func (h Hooks) onInvalidate(ctx context.Context, id CacheIdentifier, took time.Duration, err error) {
	if h.OnInvalidate != nil {
		h.OnInvalidate(ctx, id, took, err)
	}
}

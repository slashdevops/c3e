package c3e

import (
	"context"
	"time"

	"github.com/valkey-io/valkey-go"
)

// mockCacheRepository is a mock implementation of CacheRepository for testing
type mockCacheRepository struct {
	doFunc           func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult
	doMultiFunc      func(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult
	doCacheFunc      func(ctx context.Context, cmd valkey.Cacheable, ttl time.Duration) valkey.ValkeyResult
	doMultiCacheFunc func(ctx context.Context, multi ...valkey.CacheableTTL) []valkey.ValkeyResult
	builderFunc      func() valkey.Builder
}

func newMockCacheRepository() *mockCacheRepository {
	return &mockCacheRepository{}
}

func (m *mockCacheRepository) Do(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
	if m.doFunc != nil {
		return m.doFunc(ctx, cmd)
	}
	return valkey.ValkeyResult{}
}

func (m *mockCacheRepository) DoMulti(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
	if m.doMultiFunc != nil {
		return m.doMultiFunc(ctx, multi...)
	}
	results := make([]valkey.ValkeyResult, len(multi))
	return results
}

func (m *mockCacheRepository) DoCache(ctx context.Context, cmd valkey.Cacheable, ttl time.Duration) valkey.ValkeyResult {
	if m.doCacheFunc != nil {
		return m.doCacheFunc(ctx, cmd, ttl)
	}
	return valkey.ValkeyResult{}
}

func (m *mockCacheRepository) DoMultiCache(ctx context.Context, multi ...valkey.CacheableTTL) []valkey.ValkeyResult {
	if m.doMultiCacheFunc != nil {
		return m.doMultiCacheFunc(ctx, multi...)
	}
	results := make([]valkey.ValkeyResult, len(multi))
	return results
}

func (m *mockCacheRepository) B() valkey.Builder {
	if m.builderFunc != nil {
		return m.builderFunc()
	}
	// Return a real builder for testing
	// Use DisableCache to potentially avoid immediate connection or background routines
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableCache: true,
		DisableRetry: true, // Prevent queueing when cache is down
	})

	if err != nil || client == nil {
		// Fallback if client creation fails
		return valkey.Builder{}
	}
	return client.B()
}

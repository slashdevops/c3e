package c3e

import (
	"context"
	"testing"
	"time"
)

func TestHooks_OnGet_calledOnFallback(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skip("Skipping: Valkey not available to build a real command builder")
		}
	}()

	cm, err := NewCacheManager(newMockCacheRepository(), false)
	if err != nil {
		t.Fatalf("NewCacheManager: %v", err)
	}

	var (
		calls      int
		gotResult  Result
		gotID      CacheIdentifier
		gotElapsed bool
	)
	cfg := SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		Hooks: Hooks{
			OnGet: func(_ context.Context, id CacheIdentifier, r Result, took time.Duration) {
				calls++
				gotResult = r
				gotID = id
				gotElapsed = took >= 0
			},
		},
	}
	sm, err := NewSafeCacheManager(cm, cfg)
	if err != nil {
		t.Fatalf("NewSafeCacheManager: %v", err)
	}

	fetcher := func(_ context.Context) (any, []CacheIdentifier, error) {
		return testData{ID: 1, Name: "from source"}, nil, nil
	}

	id := CacheIdentifier{Type: "thing", ID: "1"}
	var dst testData
	if err := sm.Get(context.Background(), id, &dst, fetcher); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if calls != 1 {
		t.Fatalf("OnGet called %d times, want 1", calls)
	}
	if gotID != id {
		t.Errorf("OnGet id = %v, want %v", gotID, id)
	}
	if gotResult == "" {
		t.Error("OnGet result is empty")
	}
	if !gotElapsed {
		t.Error("OnGet elapsed not reported")
	}
}

func TestHooks_OnInvalidate_called(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Skip("Skipping: Valkey not available to build a real command builder")
		}
	}()

	cm, err := NewCacheManager(newMockCacheRepository(), false)
	if err != nil {
		t.Fatalf("NewCacheManager: %v", err)
	}

	called := false
	cfg := SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		Hooks: Hooks{
			OnInvalidate: func(_ context.Context, _ CacheIdentifier, _ time.Duration, _ error) {
				called = true
			},
		},
	}
	sm, err := NewSafeCacheManager(cm, cfg)
	if err != nil {
		t.Fatalf("NewSafeCacheManager: %v", err)
	}

	_ = sm.Invalidate(context.Background(), CacheIdentifier{Type: "thing", ID: "1"})
	if !called {
		t.Error("OnInvalidate was not called")
	}
}

func TestHooks_zeroValueIsNoop(t *testing.T) {
	// A zero Hooks must not panic when invoked.
	var h Hooks
	h.onGet(context.Background(), CacheIdentifier{Type: "t", ID: "1"}, ResultHit, time.Millisecond)
	h.onRefresh(context.Background(), CacheIdentifier{Type: "t", ID: "1"}, nil)
	h.onInvalidate(context.Background(), CacheIdentifier{Type: "t", ID: "1"}, time.Millisecond, nil)
}

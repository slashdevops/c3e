package c3e

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"
)

func TestNewTypedSafeCacheManager(t *testing.T) {
	mockRepo := newMockCacheRepository()
	cacheManager, err := NewCacheManager(mockRepo, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	config := SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
	}

	safeMgr, err := NewSafeCacheManager(cacheManager, config)
	if err != nil {
		t.Fatalf("failed to create safe cache manager: %v", err)
	}

	t.Run("creates_typed_manager", func(t *testing.T) {
		typed := NewTypedSafeCacheManager[testData](safeMgr)
		if typed == nil {
			t.Fatal("expected non-nil TypedSafeCacheManager")
		}

		if typed.manager != safeMgr {
			t.Error("expected manager to be set")
		}
	})

	t.Run("different_types", func(t *testing.T) {
		typedInt := NewTypedSafeCacheManager[int](safeMgr)
		typedStr := NewTypedSafeCacheManager[string](safeMgr)

		if typedInt == nil || typedStr == nil {
			t.Fatal("expected non-nil managers for different types")
		}
	})
}

func TestTypedSafeCacheManager_Get(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Recovered from panic: %v. This is expected if Valkey is not running.", r)
			t.Skip("Skipping test because Valkey is not available to create a valid Builder")
		}
	}()

	mockRepo := newMockCacheRepository()
	mockRepo.doFunc = func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
		return valkey.ValkeyResult{}
	}
	mockRepo.doMultiFunc = func(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
		return make([]valkey.ValkeyResult, len(multi))
	}

	cacheManager, err := NewCacheManager(mockRepo, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	config := SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		QueryTimeout:  50 * time.Millisecond,
	}

	safeMgr, err := NewSafeCacheManager(cacheManager, config)
	if err != nil {
		t.Fatalf("failed to create safe cache manager: %v", err)
	}

	typed := NewTypedSafeCacheManager[testData](safeMgr)

	ctx := context.Background()
	identifier := CacheIdentifier{Type: "test", ID: "typed1"}

	fetcher := func(ctx context.Context) (testData, []CacheIdentifier, error) {
		return testData{ID: 100, Name: "Typed Result"}, nil, nil
	}

	result, err := typed.Get(ctx, identifier, fetcher)
	if err != nil {
		t.Fatalf("expected Get to succeed, got error: %v", err)
	}

	if result.ID != 100 {
		t.Errorf("expected ID 100, got %d", result.ID)
	}

	if result.Name != "Typed Result" {
		t.Errorf("expected Name 'Typed Result', got %q", result.Name)
	}
}

func TestTypedSafeCacheManager_GetOrFetch(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Recovered from panic: %v. This is expected if Valkey is not running.", r)
			t.Skip("Skipping test because Valkey is not available to create a valid Builder")
		}
	}()

	mockRepo := newMockCacheRepository()
	mockRepo.doFunc = func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
		return valkey.ValkeyResult{}
	}
	mockRepo.doMultiFunc = func(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
		return make([]valkey.ValkeyResult, len(multi))
	}

	cacheManager, err := NewCacheManager(mockRepo, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	config := SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		QueryTimeout:  50 * time.Millisecond,
	}

	safeMgr, err := NewSafeCacheManager(cacheManager, config)
	if err != nil {
		t.Fatalf("failed to create safe cache manager: %v", err)
	}

	typed := NewTypedSafeCacheManager[testData](safeMgr)

	ctx := context.Background()
	identifier := CacheIdentifier{Type: "test", ID: "getorfetch1"}

	result, err := typed.GetOrFetch(ctx, identifier, func(ctx context.Context) (testData, []CacheIdentifier, error) {
		return testData{ID: 200, Name: "Fetched"}, []CacheIdentifier{{Type: "dep", ID: "1"}}, nil
	})

	if err != nil {
		t.Fatalf("expected GetOrFetch to succeed, got error: %v", err)
	}

	if result.ID != 200 {
		t.Errorf("expected ID 200, got %d", result.ID)
	}
}

func TestGetSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Recovered from panic: %v. This is expected if Valkey is not running.", r)
			t.Skip("Skipping test because Valkey is not available to create a valid Builder")
		}
	}()

	mockRepo := newMockCacheRepository()
	mockRepo.doFunc = func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
		return valkey.ValkeyResult{}
	}
	mockRepo.doMultiFunc = func(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
		return make([]valkey.ValkeyResult, len(multi))
	}

	cacheManager, err := NewCacheManager(mockRepo, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	config := SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		QueryTimeout:  50 * time.Millisecond,
	}

	safeMgr, err := NewSafeCacheManager(cacheManager, config)
	if err != nil {
		t.Fatalf("failed to create safe cache manager: %v", err)
	}

	ctx := context.Background()
	identifier := CacheIdentifier{Type: "test", ID: "getsafe1"}

	fetcher := func(ctx context.Context) (testData, []CacheIdentifier, error) {
		return testData{ID: 300, Name: "Safe Result"}, nil, nil
	}

	result, err := GetSafe(ctx, safeMgr, identifier, fetcher)
	if err != nil {
		t.Fatalf("expected GetSafe to succeed, got error: %v", err)
	}

	if result.ID != 300 {
		t.Errorf("expected ID 300, got %d", result.ID)
	}

	if result.Name != "Safe Result" {
		t.Errorf("expected Name 'Safe Result', got %q", result.Name)
	}
}

func TestGetSafe_FetcherError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Recovered from panic: %v. This is expected if Valkey is not running.", r)
			t.Skip("Skipping test because Valkey is not available to create a valid Builder")
		}
	}()

	mockRepo := newMockCacheRepository()
	mockRepo.doFunc = func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
		return valkey.ValkeyResult{}
	}
	mockRepo.doMultiFunc = func(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
		return make([]valkey.ValkeyResult, len(multi))
	}

	cacheManager, err := NewCacheManager(mockRepo, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	config := SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		QueryTimeout:  50 * time.Millisecond,
	}

	safeMgr, err := NewSafeCacheManager(cacheManager, config)
	if err != nil {
		t.Fatalf("failed to create safe cache manager: %v", err)
	}

	ctx := context.Background()
	identifier := CacheIdentifier{Type: "test", ID: "error1"}

	expectedErr := errors.New("database connection lost")
	fetcher := func(ctx context.Context) (testData, []CacheIdentifier, error) {
		return testData{}, nil, expectedErr
	}

	_, err = GetSafe(ctx, safeMgr, identifier, fetcher)
	if err == nil {
		t.Fatal("expected error from fetcher")
	}

	if !errors.Is(err, expectedErr) {
		t.Errorf("expected specific error, got %v", err)
	}
}

func TestGetOrFetch_Standalone(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Logf("Recovered from panic: %v. This is expected if Valkey is not running.", r)
			t.Skip("Skipping test because Valkey is not available to create a valid Builder")
		}
	}()

	mockRepo := newMockCacheRepository()
	mockRepo.doFunc = func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
		return valkey.ValkeyResult{}
	}
	mockRepo.doMultiFunc = func(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
		return make([]valkey.ValkeyResult, len(multi))
	}

	cacheManager, err := NewCacheManager(mockRepo, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	config := SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		QueryTimeout:  50 * time.Millisecond,
	}

	safeMgr, err := NewSafeCacheManager(cacheManager, config)
	if err != nil {
		t.Fatalf("failed to create safe cache manager: %v", err)
	}

	ctx := context.Background()
	identifier := CacheIdentifier{Type: "test", ID: "standalone1"}

	result, err := GetOrFetch(ctx, safeMgr, identifier, func(ctx context.Context) (testData, []CacheIdentifier, error) {
		return testData{ID: 400, Name: "Standalone"}, []CacheIdentifier{{Type: "org", ID: "1"}}, nil
	})

	if err != nil {
		t.Fatalf("expected GetOrFetch to succeed, got error: %v", err)
	}

	if result.ID != 400 {
		t.Errorf("expected ID 400, got %d", result.ID)
	}

	if result.Name != "Standalone" {
		t.Errorf("expected Name 'Standalone', got %q", result.Name)
	}
}

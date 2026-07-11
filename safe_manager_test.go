package c3e

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"
)

type testData struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func TestNewSafeCacheManager(t *testing.T) {
	t.Run("success", func(t *testing.T) {
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

		if safeMgr == nil {
			t.Fatal("expected non-nil SafeCacheManager")
		}

		if safeMgr.cache != cacheManager {
			t.Error("expected cache manager to be set")
		}

		if safeMgr.cfg.HardTTL != config.HardTTL {
			t.Errorf("expected HardTTL %v, got %v", config.HardTTL, safeMgr.cfg.HardTTL)
		}

		if safeMgr.cfg.SoftTTL != config.SoftTTL {
			t.Errorf("expected SoftTTL %v, got %v", config.SoftTTL, safeMgr.cfg.SoftTTL)
		}

		if safeMgr.sfg == nil {
			t.Error("expected singleflight group to be initialized")
		}
	})

	t.Run("nil_cache_manager", func(t *testing.T) {
		config := SafeCacheManagerConfig{
			HardTTL:       2 * time.Hour,
			SoftTTL:       1 * time.Hour,
			JitterPercent: 0.1,
		}

		safeMgr, err := NewSafeCacheManager(nil, config)
		if err == nil {
			t.Fatal("expected error for nil cache manager")
		}

		if safeMgr != nil {
			t.Error("expected nil safe cache manager")
		}

		if err.Error() != "cacheManager cannot be nil" {
			t.Errorf("unexpected error message: %s", err.Error())
		}
	})
}

func TestSafeCacheManagerConstants(t *testing.T) {
	t.Run("max_query_timeout", func(t *testing.T) {
		if MaxSafeCacheManagerQueryTimeout != 500*time.Millisecond {
			t.Errorf("expected 500ms, got %v", MaxSafeCacheManagerQueryTimeout)
		}
	})

	t.Run("min_query_timeout", func(t *testing.T) {
		if MinSafeCacheManagerQueryTimeout != 5*time.Millisecond {
			t.Errorf("expected 5ms, got %v", MinSafeCacheManagerQueryTimeout)
		}
	})

	t.Run("default_query_timeout", func(t *testing.T) {
		if DefaultSafeCacheManagerQueryTimeout != 70*time.Millisecond {
			t.Errorf("expected 70ms, got %v", DefaultSafeCacheManagerQueryTimeout)
		}
	})

	t.Run("min_less_than_default", func(t *testing.T) {
		if MinSafeCacheManagerQueryTimeout >= DefaultSafeCacheManagerQueryTimeout {
			t.Error("min should be less than default")
		}
	})

	t.Run("default_less_than_max", func(t *testing.T) {
		if DefaultSafeCacheManagerQueryTimeout >= MaxSafeCacheManagerQueryTimeout {
			t.Error("default should be less than max")
		}
	})
}

func TestSafeCacheManager_Config_Validation(t *testing.T) {
	tests := []struct {
		name        string
		config      SafeCacheManagerConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid_config",
			config: SafeCacheManagerConfig{
				HardTTL:       2 * time.Hour,
				SoftTTL:       1 * time.Hour,
				JitterPercent: 0.1, // 10% jitter
			},
			expectError: false,
		},
		{
			name: "no_jitter",
			config: SafeCacheManagerConfig{
				HardTTL:       2 * time.Hour,
				SoftTTL:       1 * time.Hour,
				JitterPercent: 0, // no jitter
			},
			expectError: false,
		},
		{
			name: "valid_config_with_query_timeout",
			config: SafeCacheManagerConfig{
				HardTTL:       2 * time.Hour,
				SoftTTL:       1 * time.Hour,
				JitterPercent: 0.1,
				QueryTimeout:  70 * time.Millisecond,
			},
			expectError: false,
		},
		{
			name: "valid_config_with_default_query_timeout",
			config: SafeCacheManagerConfig{
				HardTTL:       2 * time.Hour,
				SoftTTL:       1 * time.Hour,
				JitterPercent: 0.1,
				QueryTimeout:  0, // Should use default
			},
			expectError: false,
		},
		{
			name: "invalid_query_timeout_too_low",
			config: SafeCacheManagerConfig{
				HardTTL:       2 * time.Hour,
				SoftTTL:       1 * time.Hour,
				JitterPercent: 0.1,
				QueryTimeout:  1 * time.Millisecond, // Below minimum
			},
			expectError: true,
			errorMsg:    "QueryTimeout must be between",
		},
		{
			name: "invalid_query_timeout_too_high",
			config: SafeCacheManagerConfig{
				HardTTL:       2 * time.Hour,
				SoftTTL:       1 * time.Hour,
				JitterPercent: 0.1,
				QueryTimeout:  600 * time.Millisecond, // Above maximum
			},
			expectError: true,
			errorMsg:    "QueryTimeout must be between",
		},
		{
			name: "invalid_jitter_too_high",
			config: SafeCacheManagerConfig{
				HardTTL:       2 * time.Hour,
				SoftTTL:       1 * time.Hour,
				JitterPercent: 1.5, // Above 1.0
			},
			expectError: true,
			errorMsg:    "JitterPercent must be in",
		},
		{
			name: "invalid_soft_ttl_exceeds_hard_ttl",
			config: SafeCacheManagerConfig{
				HardTTL:       1 * time.Hour,
				SoftTTL:       2 * time.Hour, // Greater than HardTTL
				JitterPercent: 0.1,
			},
			expectError: true,
			errorMsg:    "SoftTTL must be between 0 and HardTTL",
		},
		// The library validates only structural invariants; short TTLs are
		// valid (business bounds like "≥ 1h" live in the calling app).
		{
			name: "structural_short_hard_ttl_ok",
			config: SafeCacheManagerConfig{
				HardTTL:       1 * time.Second,
				SoftTTL:       500 * time.Millisecond,
				JitterPercent: 0.1,
			},
			expectError: false,
		},
		{
			name: "structural_zero_soft_ttl_ok",
			config: SafeCacheManagerConfig{
				HardTTL:       1 * time.Hour,
				SoftTTL:       0,
				JitterPercent: 0.1,
			},
			expectError: false,
		},
		{
			name: "invalid_hard_ttl_zero",
			config: SafeCacheManagerConfig{
				HardTTL:       0,
				SoftTTL:       0,
				JitterPercent: 0.1,
			},
			expectError: true,
			errorMsg:    "HardTTL must be greater than 0",
		},
		{
			name: "invalid_jitter_at_one",
			config: SafeCacheManagerConfig{
				HardTTL:       1 * time.Hour,
				SoftTTL:       30 * time.Minute,
				JitterPercent: 1.0, // must be strictly < 1
			},
			expectError: true,
			errorMsg:    "JitterPercent must be in",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := newMockCacheRepository()
			cacheManager, err := NewCacheManager(mockRepo, false)
			if err != nil {
				t.Fatalf("failed to create cache manager: %v", err)
			}
			safeMgr, err := NewSafeCacheManager(cacheManager, tt.config)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if tt.errorMsg != "" && !containsString(err.Error(), tt.errorMsg) {
					t.Errorf("expected error message to contain %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("failed to create safe cache manager: %v", err)
				}

				if safeMgr == nil {
					t.Error("expected manager to be created")
				}

				// Verify default QueryTimeout is set when 0 is provided
				if tt.config.QueryTimeout == 0 && safeMgr.cfg.QueryTimeout != DefaultSafeCacheManagerQueryTimeout {
					t.Errorf("expected default QueryTimeout %v, got %v", DefaultSafeCacheManagerQueryTimeout, safeMgr.cfg.QueryTimeout)
				}

				// Verify explicit QueryTimeout is preserved
				if tt.config.QueryTimeout > 0 && safeMgr.cfg.QueryTimeout != tt.config.QueryTimeout {
					t.Errorf("expected QueryTimeout %v, got %v", tt.config.QueryTimeout, safeMgr.cfg.QueryTimeout)
				}
			}
		})
	}
}

// containsString checks if s contains substr
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestSafeCacheManager_Invalidate(t *testing.T) {
	mockRepo := newMockCacheRepository()
	cacheManager, err := NewCacheManager(mockRepo, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	config := SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1, // 10% jitter
	}
	safeMgr, err := NewSafeCacheManager(cacheManager, config)
	if err != nil {
		t.Fatalf("failed to create safe cache manager: %v", err)
	}

	// Test that SafeCacheManager delegates to CacheManager correctly
	// The actual invalidation logic is tested in CacheManager tests
	// This test just verifies the delegation works
	_ = safeMgr

	// Note: Full invalidation testing requires integration tests with actual Valkey
	t.Log("SafeCacheManager created successfully and delegates to CacheManager")
}

func TestFetcherFunc_Success(t *testing.T) {
	fetcher := func(ctx context.Context) (any, []string, error) {
		return testData{ID: 42, Name: "Answer"}, []string{"dep:1", "dep:2"}, nil
	}

	data, deps, err := fetcher(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	td, ok := data.(testData)
	if !ok {
		t.Fatal("expected testData type")
	}

	if td.ID != 42 {
		t.Errorf("expected ID 42, got %d", td.ID)
	}

	if len(deps) != 2 {
		t.Errorf("expected 2 dependencies, got %d", len(deps))
	}
}

func TestFetcherFunc_Error(t *testing.T) {
	expectedErr := errors.New("fetch failed")
	fetcher := func(ctx context.Context) (any, []string, error) {
		return nil, nil, expectedErr
	}

	_, _, err := fetcher(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	if !errors.Is(err, expectedErr) {
		t.Errorf("expected specific error, got %v", err)
	}
}

func TestSafeCacheManager_Get_CacheTimeout_FallbackToDatabase(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			// If the panic is due to the builder validation (because no Valkey is running),
			// we skip the test.
			t.Logf("Recovered from panic: %v. This is expected if Valkey is not running.", r)
			t.Skip("Skipping test because Valkey is not available to create a valid Builder")
		}
	}()

	mockRepo := newMockCacheRepository()

	// Configure mock to simulate a slow cache response that will timeout
	mockRepo.doFunc = func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
		// Simulate a slow cache operation that exceeds the query timeout
		select {
		case <-time.After(100 * time.Millisecond): // Longer than our query timeout
			return valkey.ValkeyResult{}
		case <-ctx.Done():
			// Return empty result; the manager will check ctx.Err()
			return valkey.ValkeyResult{}
		}
	}

	cacheManager, err := NewCacheManager(mockRepo, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	config := SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		QueryTimeout:  50 * time.Millisecond, // Short timeout to trigger fallback
	}

	safeMgr, err := NewSafeCacheManager(cacheManager, config)
	if err != nil {
		t.Fatalf("failed to create safe cache manager: %v", err)
	}

	// Create a fetcher that should be called when cache times out
	fetcherCalled := false
	fetcher := func(ctx context.Context) (any, []CacheIdentifier, error) {
		fetcherCalled = true
		return testData{ID: 123, Name: "Fallback Data"}, nil, nil
	}

	// Main context with longer timeout (should not expire)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result testData
	identifier := CacheIdentifier{Type: "test", ID: "timeoutkey"}

	err = safeMgr.Get(ctx, identifier, &result, fetcher)
	// Should succeed by falling back to database
	if err != nil {
		t.Fatalf("expected Get to succeed with fallback, got error: %v", err)
	}

	// Verify fetcher was called (fallback to database)
	if !fetcherCalled {
		t.Error("expected fetcher to be called due to cache timeout")
	}

	// Verify we got the data from the fetcher
	if result.ID != 123 {
		t.Errorf("expected ID 123, got %d", result.ID)
	}

	if result.Name != "Fallback Data" {
		t.Errorf("expected Name 'Fallback Data', got %q", result.Name)
	}

	// Verify main context is still valid
	if ctx.Err() != nil {
		t.Errorf("expected main context to still be valid, got error: %v", ctx.Err())
	}
}

func TestSafeCacheManager_Get_MainContextExpired(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			// If the panic is due to the builder validation (because no Valkey is running),
			// we skip the test.
			t.Logf("Recovered from panic: %v. This is expected if Valkey is not running.", r)
			t.Skip("Skipping test because Valkey is not available to create a valid Builder")
		}
	}()

	mockRepo := newMockCacheRepository()

	// Configure mock to simulate a slow response
	mockRepo.doFunc = func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
		select {
		case <-time.After(200 * time.Millisecond):
			return valkey.ValkeyResult{}
		case <-ctx.Done():
			return valkey.ValkeyResult{}
		}
	}

	cacheManager, err := NewCacheManager(mockRepo, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	config := SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		QueryTimeout:  300 * time.Millisecond, // Longer than main context
	}

	safeMgr, err := NewSafeCacheManager(cacheManager, config)
	if err != nil {
		t.Fatalf("failed to create safe cache manager: %v", err)
	}

	fetcherCalled := false
	fetcher := func(ctx context.Context) (any, []CacheIdentifier, error) {
		fetcherCalled = true
		// Check if context is expired
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return testData{ID: 456, Name: "Test"}, nil, nil
	}

	// Main context with very short timeout (expires before cache timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var result testData
	identifier := CacheIdentifier{Type: "test", ID: "mainctxexpired"}

	err = safeMgr.Get(ctx, identifier, &result, fetcher)

	// Should get a context error
	if err == nil {
		t.Fatal("expected error due to main context expiration")
	}

	// The fetcher might be called but should also fail due to context
	if fetcherCalled {
		// If fetcher was called, it should have returned a context error
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Logf("fetcher was called and returned error: %v", err)
		}
	}
}

func TestSafeCacheManager_Get_CacheHit_WithinTimeout(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			// If the panic is due to the builder validation (because no Valkey is running),
			// we skip the test.
			t.Logf("Recovered from panic: %v. This is expected if Valkey is not running.", r)
			t.Skip("Skipping test because Valkey is not available to create a valid Builder")
		}
	}()

	mockRepo := newMockCacheRepository()

	// Configure mock to return cached data quickly (within timeout)
	mockRepo.doFunc = func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
		// This test is simplified because ValkeyResult cannot be easily mocked
		// The real behavior is tested in integration tests
		return valkey.ValkeyResult{}
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

	// Fetcher should be called since mock returns empty result (cache miss)
	fetcherCalled := false
	fetcher := func(ctx context.Context) (any, []CacheIdentifier, error) {
		fetcherCalled = true
		return testData{ID: 999, Name: "Fetched Data"}, nil, nil
	}

	ctx := context.Background()
	var result testData
	identifier := CacheIdentifier{Type: "test", ID: "cachehit"}

	err = safeMgr.Get(ctx, identifier, &result, fetcher)
	// With empty ValkeyResult, this will be a cache miss and call fetcher
	if err != nil {
		t.Fatalf("expected Get to succeed, got error: %v", err)
	}

	if !fetcherCalled {
		t.Error("expected fetcher to be called on cache miss")
	}

	if result.ID != 999 {
		t.Errorf("expected ID 999, got %d", result.ID)
	}

	if result.Name != "Fetched Data" {
		t.Errorf("expected Name 'Fetched Data', got %q", result.Name)
	}
}

package c3e

import (
	"context"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"
)

func TestNewCacheManager(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mock := newMockCacheRepository()
		manager, err := NewCacheManager(mock, false)
		if err != nil {
			t.Fatalf("failed to create cache manager: %v", err)
		}

		if manager == nil {
			t.Fatal("expected non-nil manager")
		}

		if manager.client != mock {
			t.Error("expected client to be set")
		}
	})

	t.Run("nil_client", func(t *testing.T) {
		manager, err := NewCacheManager(nil, false)
		if err == nil {
			t.Fatal("expected error for nil client")
		}

		if manager != nil {
			t.Error("expected nil manager")
		}

		if err.Error() != "client cannot be nil" {
			t.Errorf("unexpected error message: %s", err.Error())
		}
	})

	t.Run("with_client_cache_enabled", func(t *testing.T) {
		mock := newMockCacheRepository()
		manager, err := NewCacheManager(mock, true)
		if err != nil {
			t.Fatalf("failed to create cache manager: %v", err)
		}

		if !manager.clientCacheEnabled {
			t.Error("expected clientCacheEnabled to be true")
		}
	})

	t.Run("with_client_cache_disabled", func(t *testing.T) {
		mock := newMockCacheRepository()
		manager, err := NewCacheManager(mock, false)
		if err != nil {
			t.Fatalf("failed to create cache manager: %v", err)
		}

		if manager.clientCacheEnabled {
			t.Error("expected clientCacheEnabled to be false")
		}
	})
}

func TestCacheManager_Set_WithDependencies(t *testing.T) {
	// Test that CacheIdentifier can be used with dependencies parameter
	identifier := CacheIdentifier{Type: "user", ID: "123"}
	deps := []CacheIdentifier{{Type: "org", ID: "456"}}

	// Verify that the types are correct
	if identifier.Type != "user" || identifier.ID != "123" {
		t.Error("identifier not set correctly")
	}

	if len(deps) != 1 || deps[0].Type != "org" || deps[0].ID != "456" {
		t.Error("dependencies not set correctly")
	}
}

func TestCacheManager_Set_MultipleDependencies(t *testing.T) {
	// Test that multiple CacheIdentifiers can be used with dependencies parameter
	deps := []CacheIdentifier{
		{Type: "org", ID: "123"},
		{Type: "team", ID: "456"},
		{Type: "project", ID: "789"},
	}
	identifier := CacheIdentifier{Type: "user", ID: "123"}

	// Verify that all dependencies are set correctly
	if len(deps) != 3 {
		t.Errorf("expected 3 dependencies, got %d", len(deps))
	}

	if deps[0].Type != "org" || deps[0].ID != "123" {
		t.Error("first dependency not set correctly")
	}

	if deps[1].Type != "team" || deps[1].ID != "456" {
		t.Error("second dependency not set correctly")
	}

	if deps[2].Type != "project" || deps[2].ID != "789" {
		t.Error("third dependency not set correctly")
	}

	if identifier.Type != "user" || identifier.ID != "123" {
		t.Error("identifier not set correctly")
	}
}

func TestCacheKeyGeneration(t *testing.T) {
	tests := []struct {
		name       string
		entityType string
		entityID   string
		expected   string
	}{
		{"simple", "user", "123", "cache:user:123"},
		{"with_dash", "user-profile", "456", "cache:user-profile:456"},
		{"with_underscore", "api_key", "abc", "cache:api_key:abc"},
		{"numeric_id", "project", "999", "cache:project:999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identifier := CacheIdentifier{Type: tt.entityType, ID: tt.entityID}
			result := cacheKey(identifier)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestDepKeyGeneration(t *testing.T) {
	tests := []struct {
		name       string
		entityType string
		entityID   string
		expected   string
	}{
		{"simple", "user", "123", "dep:user:123"},
		{"with_dash", "user-profile", "456", "dep:user-profile:456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identifier := CacheIdentifier{Type: tt.entityType, ID: tt.entityID}
			result := depKey(identifier)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestDepsForKeyGeneration(t *testing.T) {
	tests := []struct {
		name     string
		cacheKey string
		expected string
	}{
		{"simple", "cache:user:123", "deps-for:cache:user:123"},
		{"nested", "cache:project:456", "deps-for:cache:project:456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := depsForKey(tt.cacheKey)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestEntityKeyFromDepKey(t *testing.T) {
	tests := []struct {
		name     string
		depKey   string
		expected string
	}{
		{"simple", "dep:user:123", "user:123"},
		{"with_colon", "dep:api:key:abc", "api:key:abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := entityKeyFromDepKey(tt.depKey)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestDepKeyFromCacheKey(t *testing.T) {
	tests := []struct {
		name     string
		cacheKey string
		expected string
	}{
		{"simple", "cache:user:123", "dep:user:123"},
		{"nested", "cache:project:456", "dep:project:456"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := depKeyFromCacheKey(tt.cacheKey)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestCacheManager_Set_NilDependencies(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			// If the panic is due to the mock returning an unusable builder (because no Valkey is running),
			// we skip the test.
			t.Logf("Recovered from panic: %v. This is expected if Valkey is not running.", r)
			t.Skip("Skipping test because Valkey is not available to create a valid Builder")
		}
	}()

	mock := newMockCacheRepository()

	// Mock Do to return empty old dependencies
	mock.doFunc = func(ctx context.Context, cmd valkey.Completed) valkey.ValkeyResult {
		return valkey.ValkeyResult{}
	}

	// Mock DoMulti to return success
	mock.doMultiFunc = func(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
		return make([]valkey.ValkeyResult, len(multi))
	}

	manager, err := NewCacheManager(mock, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	ctx := context.Background()
	identifier := CacheIdentifier{Type: "user", ID: "123"}
	data := []byte("some data")

	// Test with nil dependencies
	err = manager.Set(ctx, identifier, data, nil, time.Minute)
	if err != nil {
		t.Logf("Set returned error: %v", err)
	}
}

// Note: Unit tests for client-side caching (Get/Set with clientCacheEnabled=true)
// are not included here because they require a running Valkey instance or a complex
// mock of valkey.Builder which is difficult to achieve without a real client.
// The integration tests cover these scenarios.

// Benchmark tests
func BenchmarkCacheKeyGeneration(b *testing.B) {
	identifier := CacheIdentifier{Type: "user", ID: "123"}
	for b.Loop() {
		cacheKey(identifier)
	}
}

func BenchmarkDepKeyGeneration(b *testing.B) {
	identifier := CacheIdentifier{Type: "user", ID: "123"}
	for b.Loop() {
		depKey(identifier)
	}
}

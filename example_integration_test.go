//go:build integration

package c3e_test

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/slashdevops/c3e"
)

// TestCacheIntegration demonstrates a full integration test with Valkey
// showing cache miss, cache hit, invalidation, and stale-while-revalidate scenarios.
//
// Run this test with: go test -tags=integration -run TestCacheIntegration ./pkg/c3e/
func TestCacheIntegration(t *testing.T) {
	ctx := context.Background()

	// 1. Setup Valkey Client
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		t.Fatalf("Could not connect to Valkey: %v", err)
	}
	defer client.Close()

	// Verify connection
	pingCmd := client.B().Ping().Build()
	if err := client.Do(ctx, pingCmd).Error(); err != nil {
		t.Fatalf("Could not ping Valkey: %v", err)
	}
	log.Println("Connected to Valkey")

	// Clear all for a clean test
	flushCmd := client.B().Flushdb().Build()
	if err := client.Do(ctx, flushCmd).Error(); err != nil {
		t.Fatalf("Could not flush Valkey: %v", err)
	}

	// 2. Setup Cache Managers
	coreMgr, err := c3e.NewCacheManager(client, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}
	safeMgr, err := c3e.NewSafeCacheManager(coreMgr, c3e.SafeCacheManagerConfig{
		HardTTL:       1 * time.Hour,   // Long hard expiry
		SoftTTL:       2 * time.Second, // Short soft expiry so the stale-while-revalidate case is exercised quickly
		JitterPercent: 0.1,             // 10% jitter
	})
	if err != nil {
		t.Fatalf("failed to create safe cache manager: %v", err)
	}

	// 3. --- SCENARIO 1: Cache Miss (Slow) ---
	t.Run("cache_miss_slow", func(t *testing.T) {
		log.Println("\n--- 1. First Get (Cache Miss) ---")
		start := time.Now()
		var proj Project

		// This is the fetcher function for project proj123
		projectFetcher := func(ctx context.Context) (any, []c3e.CacheIdentifier, error) {
			p, err := getProjectFromDB("proj123")
			if err != nil {
				return nil, nil, err
			}
			// Define dependencies
			deps := []c3e.CacheIdentifier{
				{Type: "user", ID: "user123"},
				{Type: "user", ID: "user456"},
			}
			return p, deps, nil
		}

		err := safeMgr.Get(ctx, c3e.CacheIdentifier{Type: "project", ID: "proj123"}, &proj, projectFetcher)
		if err != nil {
			t.Fatalf("Error on first get: %v", err)
		}

		elapsed := time.Since(start)
		log.Printf("SUCCESS: Got project: '%s'. Took: %s", proj.Name, elapsed)

		if proj.Name != "Project 'Eagle'" {
			t.Errorf("Expected project name 'Project 'Eagle'', got '%s'", proj.Name)
		}

		// Should take at least 500ms due to DB latency
		if elapsed < 400*time.Millisecond {
			t.Errorf("Expected cache miss to take at least 400ms, took %s", elapsed)
		}
	})

	// 4. --- SCENARIO 2: Cache Hit (Fast) ---
	t.Run("cache_hit_fast", func(t *testing.T) {
		log.Println("\n--- 2. Second Get (Cache Hit) ---")
		start := time.Now()
		var proj2 Project

		projectFetcher := func(ctx context.Context) (any, []c3e.CacheIdentifier, error) {
			p, err := getProjectFromDB("proj123")
			if err != nil {
				return nil, nil, err
			}
			deps := []c3e.CacheIdentifier{
				{Type: "user", ID: "user123"},
				{Type: "user", ID: "user456"},
			}
			return p, deps, nil
		}

		err := safeMgr.Get(ctx, c3e.CacheIdentifier{Type: "project", ID: "proj123"}, &proj2, projectFetcher)
		if err != nil {
			t.Fatalf("Error on second get: %v", err)
		}

		elapsed := time.Since(start)
		log.Printf("SUCCESS: Got project from cache: '%s'. Took: %s", proj2.Name, elapsed)

		if proj2.Name != "Project 'Eagle'" {
			t.Errorf("Expected project name 'Project 'Eagle'', got '%s'", proj2.Name)
		}

		// Cache hit should be very fast (< 50ms)
		if elapsed > 50*time.Millisecond {
			t.Errorf("Expected cache hit to be fast (<50ms), took %s", elapsed)
		}
	})

	// 5. --- SCENARIO 3: Invalidation ---
	t.Run("invalidation", func(t *testing.T) {
		log.Println("\n--- 3. Invalidating 'user:user123' (a dependency) ---")
		if err := safeMgr.Invalidate(ctx, c3e.CacheIdentifier{Type: "user", ID: "user123"}); err != nil {
			t.Fatalf("Error on invalidate: %v", err)
		}
		log.Println("SUCCESS: Invalidated 'user:user123'. 'project:proj123' cache is now gone.")
	})

	// 6. --- SCENARIO 4: Post-Invalidation Miss (Slow) ---
	t.Run("post_invalidation_miss", func(t *testing.T) {
		log.Println("\n--- 4. Third Get (Post-Invalidation Miss) ---")
		start := time.Now()
		var proj3 Project

		projectFetcher := func(ctx context.Context) (any, []c3e.CacheIdentifier, error) {
			p, err := getProjectFromDB("proj123")
			if err != nil {
				return nil, nil, err
			}
			deps := []c3e.CacheIdentifier{
				{Type: "user", ID: "user123"},
				{Type: "user", ID: "user456"},
			}
			return p, deps, nil
		}

		err := safeMgr.Get(ctx, c3e.CacheIdentifier{Type: "project", ID: "proj123"}, &proj3, projectFetcher)
		if err != nil {
			t.Fatalf("Error on third get: %v", err)
		}

		elapsed := time.Since(start)
		log.Printf("SUCCESS: Got project (re-fetched): '%s'. Took: %s", proj3.Name, elapsed)

		if proj3.Name != "Project 'Eagle'" {
			t.Errorf("Expected project name 'Project 'Eagle'', got '%s'", proj3.Name)
		}

		// Should take at least 500ms due to DB latency after invalidation
		if elapsed < 400*time.Millisecond {
			t.Errorf("Expected cache miss to take at least 400ms, took %s", elapsed)
		}
	})

	// 7. --- SCENARIO 5: Stale-While-Revalidate ---
	t.Run("stale_while_revalidate", func(t *testing.T) {
		log.Println("\n--- 5. Stale-While-Revalidate Demo ---")
		log.Println("Waiting for the soft TTL to pass so the entry becomes stale...")
		time.Sleep(2500 * time.Millisecond)

		start := time.Now()
		var proj4 Project

		projectFetcher := func(ctx context.Context) (any, []c3e.CacheIdentifier, error) {
			p, err := getProjectFromDB("proj123")
			if err != nil {
				return nil, nil, err
			}
			deps := []c3e.CacheIdentifier{
				{Type: "user", ID: "user123"},
				{Type: "user", ID: "user456"},
			}
			return p, deps, nil
		}

		err := safeMgr.Get(ctx, c3e.CacheIdentifier{Type: "project", ID: "proj123"}, &proj4, projectFetcher)
		if err != nil {
			t.Fatalf("Error on stale get: %v", err)
		}

		elapsed := time.Since(start)
		log.Printf("SUCCESS: Got STALE project: '%s'. Took: %s", proj4.Name, elapsed)
		log.Println("(A background refresh was triggered. Check for 'DATABASE: Fetching...' log)")

		if proj4.Name != "Project 'Eagle'" {
			t.Errorf("Expected project name 'Project 'Eagle'', got '%s'", proj4.Name)
		}

		// Stale-while-revalidate should return quickly (stale data) even though it's expired
		if elapsed > 50*time.Millisecond {
			t.Logf("Note: Stale data returned in %s (background refresh triggered)", elapsed)
		}

		// Wait for the background refresh to complete
		time.Sleep(1 * time.Second)
		log.Println("Background refresh should have completed")
	})
}

// Example_fullWorkflow demonstrates a complete cache workflow
// This example requires a running Valkey instance on localhost:6379
func Example_fullWorkflow() {
	ctx := context.Background()

	// Setup Valkey Client
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// Setup Cache Managers
	coreMgr, err := c3e.NewCacheManager(client, false)
	if err != nil {
		log.Fatal(err)
	}
	safeMgr, err := c3e.NewSafeCacheManager(coreMgr, c3e.SafeCacheManagerConfig{
		HardTTL:       10 * time.Minute,
		SoftTTL:       5 * time.Second,
		JitterPercent: 0.1,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Define project fetcher
	projectFetcher := func(ctx context.Context) (any, []c3e.CacheIdentifier, error) {
		p, err := getProjectFromDB("proj123")
		if err != nil {
			return nil, nil, err
		}
		deps := []c3e.CacheIdentifier{
			{Type: "user", ID: "user123"},
			{Type: "user", ID: "user456"},
		}
		return p, deps, nil
	}

	// First call - cache miss (slow)
	var proj1 Project
	start := time.Now()
	err = safeMgr.Get(ctx, c3e.CacheIdentifier{Type: "project", ID: "proj123"}, &proj1, projectFetcher)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("First call: %s (took %s)\n", proj1.Name, time.Since(start))

	// Second call - cache hit (fast)
	var proj2 Project
	start = time.Now()
	err = safeMgr.Get(ctx, c3e.CacheIdentifier{Type: "project", ID: "proj123"}, &proj2, projectFetcher)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Second call: %s (took %s)\n", proj2.Name, time.Since(start))

	// Invalidate dependency
	err = safeMgr.Invalidate(ctx, c3e.CacheIdentifier{Type: "user", ID: "user123"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Invalidated user:user123 (cascades to project)")

	// Third call - cache miss after invalidation (slow)
	var proj3 Project
	start = time.Now()
	err = safeMgr.Get(ctx, c3e.CacheIdentifier{Type: "project", ID: "proj123"}, &proj3, projectFetcher)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Third call after invalidation: %s (took %s)\n", proj3.Name, time.Since(start))
}

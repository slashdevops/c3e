//go:build integration

package c3e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/slashdevops/c3e"
)

// User represents a sample domain entity
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Project represents a project with associated users
type Project struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	UserIDs []string `json:"user_ids"`
}

// --- Database simulation functions ---

// getUserFromDB simulates a slow database call
func getUserFromDB(id string) (*User, error) {
	log.Printf("DATABASE: Fetching user %s...", id)
	time.Sleep(500 * time.Millisecond) // Simulate DB latency

	if id == "user123" {
		return &User{
			ID:   "user123",
			Name: "John Doe",
		}, nil
	}
	return nil, fmt.Errorf("user not found")
}

// getProjectFromDB simulates a slow database call
func getProjectFromDB(id string) (*Project, error) {
	log.Printf("DATABASE: Fetching project %s...", id)
	time.Sleep(500 * time.Millisecond) // Simulate DB latency

	if id == "proj123" {
		return &Project{
			ID:      "proj123",
			Name:    "Project 'Eagle'",
			UserIDs: []string{"user123", "user456"},
		}, nil
	}
	return nil, fmt.Errorf("project not found")
}

// ExampleSafeCacheManager demonstrates basic usage of the SafeCacheManager
// with a real-world scenario of caching user data.
func ExampleSafeCacheManager() {
	// Initialize Valkey client
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// Create cache manager
	cacheManager, err := c3e.NewCacheManager(client, false)
	if err != nil {
		log.Fatal(err)
	}

	// Configure SafeCacheManager
	config := c3e.SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour, // Cache expires after 2 hours
		SoftTTL:       1 * time.Hour, // Start background refresh after 1 hour
		JitterPercent: 0.1,           // Add 10% jitter to prevent thundering herd
	}
	safeCacheManager, err := c3e.NewSafeCacheManager(cacheManager, config)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// Define a fetcher function that loads user data from a database
	fetchUser := func(ctx context.Context) (any, []c3e.CacheIdentifier, error) {
		user, err := getUserFromDB("user123")
		if err != nil {
			return nil, nil, err
		}

		// Define dependencies - this user might be part of a project
		dependencies := []c3e.CacheIdentifier{{Type: "project", ID: "proj123"}}

		return user, dependencies, nil
	}

	// Get user from cache or fetch from database
	var user User
	err = safeCacheManager.Get(ctx, c3e.CacheIdentifier{Type: "user", ID: "user123"}, &user, fetchUser)
	if err != nil {
		log.Printf("Error: %v", err)
		return
	}

	fmt.Printf("User: %s\n", user.Name)
	// Output: User: John Doe
}

// ExampleCacheManager_Set demonstrates setting a value in the cache with dependencies
func ExampleCacheManager_Set() {
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	cacheManager, err := c3e.NewCacheManager(client, false)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	// Fetch user from database
	user, err := getUserFromDB("user123")
	if err != nil {
		log.Fatal(err)
	}

	// Marshal to JSON
	data, err := json.Marshal(user)
	if err != nil {
		log.Fatal(err)
	}

	// Set in cache with dependencies
	identifier := c3e.CacheIdentifier{Type: "user", ID: "user123"}
	err = cacheManager.Set(
		ctx,
		identifier,
		data, // serialized data
		[]c3e.CacheIdentifier{ // dependencies
			{Type: "project", ID: "proj123"},
		},
		5*time.Minute, // TTL
	)
	if err != nil {
		log.Printf("Error setting cache: %v", err)
		return
	}

	fmt.Println("User cached successfully")
	// Output: User cached successfully
}

// ExampleCacheManager_Get demonstrates retrieving a value from the cache
func ExampleCacheManager_Get() {
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	cacheManager, err := c3e.NewCacheManager(client, false)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	// Get from cache
	data, err := cacheManager.Get(ctx, c3e.CacheIdentifier{Type: "user", ID: "user123"}, 0)
	if err == c3e.ErrCacheMiss {
		fmt.Println("Cache miss - need to fetch from database")
		return
	}
	if err != nil {
		log.Printf("Error getting cache: %v", err)
		return
	}

	// Unmarshal the data
	var user User
	if err := json.Unmarshal(data, &user); err != nil {
		log.Printf("Error unmarshaling: %v", err)
		return
	}

	fmt.Printf("User from cache: %s\n", user.Name)
}

// ExampleCacheManager_Invalidate demonstrates invalidating a cache entry
func ExampleCacheManager_Invalidate() {
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	cacheManager, err := c3e.NewCacheManager(client, false)
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	// Invalidate a project will cascade to all dependent entities
	// (users, etc. that depend on this project)
	err = cacheManager.Invalidate(ctx, c3e.CacheIdentifier{Type: "project", ID: "proj123"})
	if err != nil {
		log.Printf("Error invalidating cache: %v", err)
		return
	}

	fmt.Println("Project and all dependents invalidated")
	// Output: Project and all dependents invalidated
}

// ExampleSafeCacheManager_complexScenario demonstrates a more complex caching scenario
// with nested dependencies and background refresh.
func ExampleSafeCacheManager_complexScenario() {
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	cacheManager, err := c3e.NewCacheManager(client, false)
	if err != nil {
		log.Fatal(err)
	}
	config := c3e.SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,    // Min: 1 hour, Max: 72 hours
		SoftTTL:       30 * time.Minute, // Min: 1 minute, Max: HardTTL
		JitterPercent: 0.15,
	}
	safeCacheManager, err := c3e.NewSafeCacheManager(cacheManager, config)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// Fetch project with user dependencies
	fetchProject := func(ctx context.Context) (any, []c3e.CacheIdentifier, error) {
		project, err := getProjectFromDB("proj123")
		if err != nil {
			return nil, nil, err
		}

		// This project depends on its users
		dependencies := []c3e.CacheIdentifier{
			{Type: "user", ID: "user123"},
			{Type: "user", ID: "user456"},
		}

		return project, dependencies, nil
	}

	// Get project - will use cache if fresh, serve stale and refresh if needed
	var project Project
	err = safeCacheManager.Get(ctx, c3e.CacheIdentifier{Type: "project", ID: "proj123"}, &project, fetchProject)
	if err != nil {
		log.Printf("Error: %v", err)
		return
	}

	fmt.Printf("Project loaded: %s with %d users\n", project.Name, len(project.UserIDs))
	// Output: Project loaded: Project 'Eagle' with 2 users
}

// ExampleSafeCacheManager_staleWhileRevalidate demonstrates the stale-while-revalidate behavior
func ExampleSafeCacheManager_staleWhileRevalidate() {
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	cacheManager, err := c3e.NewCacheManager(client, false)
	if err != nil {
		log.Fatal(err)
	}
	config := c3e.SafeCacheManagerConfig{
		HardTTL:       5 * time.Minute,
		SoftTTL:       2 * time.Minute, // After 2 minutes, trigger background refresh
		JitterPercent: 0.1,
	}
	safeCacheManager, err := c3e.NewSafeCacheManager(cacheManager, config)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	fetchCounter := 0
	fetchData := func(ctx context.Context) (any, []c3e.CacheIdentifier, error) {
		fetchCounter++
		data := map[string]any{
			"value":      fmt.Sprintf("data-%d", fetchCounter),
			"fetched_at": time.Now().Unix(),
		}
		return data, nil, nil
	}

	// First call - cache miss, fetches from source
	var data1 map[string]any
	_ = safeCacheManager.Get(ctx, c3e.CacheIdentifier{Type: "data", ID: "key1"}, &data1, fetchData)
	fmt.Printf("First call fetch count: %d\n", fetchCounter)

	// Simulate time passing beyond softTTL
	time.Sleep(2100 * time.Millisecond)

	// Second call - returns stale data immediately, triggers background refresh
	var data2 map[string]any
	_ = safeCacheManager.Get(ctx, c3e.CacheIdentifier{Type: "data", ID: "key1"}, &data2, fetchData)
	fmt.Printf("Second call (stale): value=%s\n", data2["value"])

	// Give background refresh time to complete
	time.Sleep(100 * time.Millisecond)

	// Third call - gets the refreshed data
	var data3 map[string]any
	_ = safeCacheManager.Get(ctx, c3e.CacheIdentifier{Type: "data", ID: "key1"}, &data3, fetchData)
	fmt.Printf("Third call (refreshed): value=%s\n", data3["value"])
	fmt.Printf("Total fetches: %d\n", fetchCounter)

	// Note: The stale-while-revalidate pattern means we only blocked once,
	// subsequent calls got immediate responses while refresh happened in background
}

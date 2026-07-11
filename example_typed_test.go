//go:build integration

package c3e_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/slashdevops/c3e"
)

// ExampleTypedSafeCacheManager demonstrates using the type-safe generic wrapper
func ExampleTypedSafeCacheManager() {
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// Create base managers
	cacheManager, err := c3e.NewCacheManager(client, false)
	if err != nil {
		log.Fatal(err)
	}
	safeMgr, err := c3e.NewSafeCacheManager(cacheManager, c3e.SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1, // 10% jitter
		EncoderType:   c3e.CacheEncoderTypeJSON,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Create typed manager for User
	userCache := c3e.NewTypedSafeCacheManager[User](safeMgr)

	ctx := context.Background()

	// Define typed fetcher
	fetchUser := func(ctx context.Context) (User, []c3e.CacheIdentifier, error) {
		user, err := getUserFromDB("user123")
		if err != nil {
			return User{}, nil, err
		}
		deps := []c3e.CacheIdentifier{{Type: "project", ID: "proj123"}}
		return *user, deps, nil
	}

	// Get user with type safety
	user, err := userCache.Get(ctx, c3e.CacheIdentifier{Type: "user", ID: "user123"}, fetchUser)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("User: %s\n", user.Name)
	// Output: User: John Doe
}

// ExampleTypedSafeCacheManager_GetOrFetch demonstrates using the GetOrFetch method on the typed manager
func ExampleTypedSafeCacheManager_GetOrFetch() {
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// Create base managers
	cacheManager, err := c3e.NewCacheManager(client, false)
	if err != nil {
		log.Fatal(err)
	}
	safeMgr, err := c3e.NewSafeCacheManager(cacheManager, c3e.SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		EncoderType:   c3e.CacheEncoderTypeJSON,
	})
	if err != nil {
		log.Fatal(err)
	}

	// Create typed manager
	userCache := c3e.NewTypedSafeCacheManager[User](safeMgr)
	ctx := context.Background()

	// Use GetOrFetch method
	userIdentifier := c3e.CacheIdentifier{Type: "user", ID: "user123"}
	user, err := userCache.GetOrFetch(
		ctx,
		userIdentifier,
		func(ctx context.Context) (User, []c3e.CacheIdentifier, error) {
			u, err := getUserFromDB("user123")
			if err != nil {
				return User{}, nil, err
			}
			return *u, []c3e.CacheIdentifier{{Type: "project", ID: "proj123"}}, nil
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("User: %s\n", user.Name)
	// Output: User: John Doe
}

// ExampleGetSafe demonstrates the standalone generic GetSafe function
func ExampleGetSafe() {
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// Create base managers
	cacheManager, err := c3e.NewCacheManager(client, false)
	if err != nil {
		log.Fatal(err)
	}
	safeMgr, err := c3e.NewSafeCacheManager(cacheManager, c3e.SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		EncoderType:   c3e.CacheEncoderTypeJSON,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// Define typed fetcher
	fetchUser := func(ctx context.Context) (User, []c3e.CacheIdentifier, error) {
		user, err := getUserFromDB("user123")
		if err != nil {
			return User{}, nil, err
		}
		deps := []c3e.CacheIdentifier{{Type: "project", ID: "proj123"}}
		return *user, deps, nil
	}

	// Get user with type safety using standalone function
	user, err := c3e.GetSafe(ctx, safeMgr, c3e.CacheIdentifier{Type: "user", ID: "user123"}, fetchUser)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("User: %s\n", user.Name)
	// Output: User: John Doe
}

// ExampleGetOrFetch demonstrates the convenience GetOrFetch function
func ExampleGetOrFetch() {
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
	safeCacheManager, err := c3e.NewSafeCacheManager(cacheManager, c3e.SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1, // 10% jitter
		EncoderType:   c3e.CacheEncoderTypeJSON,
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// Use GetOrFetch helper
	userIdentifier := c3e.CacheIdentifier{Type: "user", ID: "user123"}
	user, err := c3e.GetOrFetch(
		ctx,
		safeCacheManager,
		userIdentifier,
		func(ctx context.Context) (User, []c3e.CacheIdentifier, error) {
			u, err := getUserFromDB("user123")
			if err != nil {
				return User{}, nil, err
			}
			return *u, []c3e.CacheIdentifier{{Type: "project", ID: "proj123"}}, nil
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("User: %s\n", user.Name)
	// Output: User: John Doe
}

// ExampleSetWithEncoder demonstrates explicit encoder selection for Set operations
func ExampleSetWithEncoder() {
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

	user := User{
		ID:   "user789",
		Name: "Jane Smith",
	}

	// Set with JSON encoding
	userIdentifier := c3e.CacheIdentifier{Type: "user", ID: "user789"}
	err = c3e.SetWithEncoder(
		ctx,
		cacheManager,
		c3e.CacheEncoderTypeJSON,
		userIdentifier,
		user,
		[]c3e.CacheIdentifier{{Type: "project", ID: "proj456"}},
		2*time.Hour,
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("User cached with JSON encoding")
	// Output: User cached with JSON encoding
}

// ExampleGetWithEncoder demonstrates per-request encoder selection
func ExampleGetWithEncoder() {
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

	// Use Gob encoding for this specific request
	config := c3e.SafeCacheManagerConfig{
		HardTTL:       2 * time.Hour,
		SoftTTL:       1 * time.Hour,
		JitterPercent: 0.1,
		EncoderType:   c3e.CacheEncoderTypeGob, // Use Gob for better performance
	}

	fetchProject := func(ctx context.Context) (Project, []c3e.CacheIdentifier, error) {
		proj, err := getProjectFromDB("proj123")
		if err != nil {
			return Project{}, nil, err
		}
		return *proj, []c3e.CacheIdentifier{{Type: "user", ID: "user123"}, {Type: "user", ID: "user456"}}, nil
	}

	projectIdentifier := c3e.CacheIdentifier{Type: "project", ID: "proj123"}

	// Start from a clean slate: an entry cached earlier with a different
	// encoder would otherwise be decoded with this request's Gob decoder.
	// A cached value must always be read back with the same encoder that
	// wrote it (see the "Limitations" docs); invalidating first guarantees
	// this example is self-contained.
	if err := cacheManager.Invalidate(ctx, projectIdentifier); err != nil {
		log.Fatal(err)
	}

	project, err := c3e.GetWithEncoder(
		ctx,
		cacheManager,
		config,
		projectIdentifier,
		fetchProject,
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Project: %s (using Gob encoding)\n", project.Name)
	// Output: Project: Project 'Eagle' (using Gob encoding)
}

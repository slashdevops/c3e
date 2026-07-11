//go:build integration

package c3e_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/slashdevops/c3e"
)

// Test entities for real-world dependency scenario
type Policy struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Role struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	PolicyID string `json:"policy_id"`
}

type Permission struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	PolicyID string `json:"policy_id"`
	RoleID   string `json:"role_id"`
	UserID   string `json:"user_id"`
}

type Model struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProjectID    string `json:"project_id"`
	LLMEnginesID string `json:"llm_engine_id,omitempty"`
}

type LLMEngine struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TestCascadingInvalidation_RealWorldScenario tests the real-world scenario with multiple entities
// and complex dependency relationships as described in the requirements.
//
// Dependency Graph:
//   - Policy (no dependencies)
//   - Role depends on Policy
//   - User (no dependencies)
//   - Project depends on User
//   - Model depends on Project (and optionally LLM Engine)
//   - Permission depends on Policy, Role, User
//   - LLM Engine (no dependencies)
//
// Run this test with: go test -tags=integration -run TestCascadingInvalidation_RealWorldScenario ./pkg/c3e/
func TestCascadingInvalidation_RealWorldScenario(t *testing.T) {
	ctx := context.Background()

	// Setup Valkey Client
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

	// Clear all for a clean test
	flushCmd := client.B().Flushdb().Build()
	if err := client.Do(ctx, flushCmd).Error(); err != nil {
		t.Fatalf("Could not flush Valkey: %v", err)
	}

	// Setup Cache Managers
	coreMgr, err := c3e.NewCacheManager(client, false)
	if err != nil {
		t.Fatalf("failed to create cache manager: %v", err)
	}

	// --- Step 1: Cache Policy (no dependencies) ---
	t.Run("cache_policy", func(t *testing.T) {
		policy := Policy{ID: "policy1", Name: "Admin Policy"}
		err := c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "policy", ID: "policy1"},
			nil, // no dependencies
			policy,
			5*time.Minute,
		)
		if err != nil {
			t.Fatalf("Failed to cache policy: %v", err)
		}
		t.Log("✓ Cached Policy (no dependencies)")
	})

	// --- Step 2: Cache Role (depends on Policy) ---
	t.Run("cache_role", func(t *testing.T) {
		role := Role{ID: "role1", Name: "Admin Role", PolicyID: "policy1"}
		err := c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "role", ID: "role1"},
			[]c3e.CacheIdentifier{
				{Type: "policy", ID: "policy1"},
			},
			role,
			5*time.Minute,
		)
		if err != nil {
			t.Fatalf("Failed to cache role: %v", err)
		}
		t.Log("✓ Cached Role (depends on Policy)")
	})

	// --- Step 3: Cache User (no dependencies) ---
	t.Run("cache_user", func(t *testing.T) {
		user := User{ID: "user1", Name: "John Doe"}
		err := c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "user", ID: "user1"},
			nil, // no dependencies
			user,
			5*time.Minute,
		)
		if err != nil {
			t.Fatalf("Failed to cache user: %v", err)
		}
		t.Log("✓ Cached User (no dependencies)")
	})

	// --- Step 4: Cache Project (depends on User) ---
	t.Run("cache_project", func(t *testing.T) {
		project := Project{ID: "project1", Name: "AI Project", UserIDs: []string{"user1"}}
		err := c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "project", ID: "project1"},
			[]c3e.CacheIdentifier{
				{Type: "user", ID: "user1"},
			},
			project,
			5*time.Minute,
		)
		if err != nil {
			t.Fatalf("Failed to cache project: %v", err)
		}
		t.Log("✓ Cached Project (depends on User)")
	})

	// --- Step 5: Cache Model #1 (depends on Project) ---
	t.Run("cache_model1", func(t *testing.T) {
		model := Model{ID: "model1", Name: "GPT Model", ProjectID: "project1"}
		err := c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "model", ID: "model1"},
			[]c3e.CacheIdentifier{
				{Type: "project", ID: "project1"},
			},
			model,
			5*time.Minute,
		)
		if err != nil {
			t.Fatalf("Failed to cache model1: %v", err)
		}
		t.Log("✓ Cached Model #1 (depends on Project)")
	})

	// --- Step 6: Cache Permission (depends on Policy, Role, User) ---
	t.Run("cache_permission", func(t *testing.T) {
		permission := Permission{
			ID:       "perm1",
			Name:     "Read Access",
			PolicyID: "policy1",
			RoleID:   "role1",
			UserID:   "user1",
		}
		err := c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "permission", ID: "perm1"},
			[]c3e.CacheIdentifier{
				{Type: "policy", ID: "policy1"},
				{Type: "role", ID: "role1"},
				{Type: "user", ID: "user1"},
			},
			permission,
			5*time.Minute,
		)
		if err != nil {
			t.Fatalf("Failed to cache permission: %v", err)
		}
		t.Log("✓ Cached Permission (depends on Policy, Role, User)")
	})

	// --- Step 7: Cache Model #2 (depends on Project) ---
	t.Run("cache_model2", func(t *testing.T) {
		model := Model{ID: "model2", Name: "BERT Model", ProjectID: "project1"}
		err := c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "model", ID: "model2"},
			[]c3e.CacheIdentifier{
				{Type: "project", ID: "project1"},
			},
			model,
			5*time.Minute,
		)
		if err != nil {
			t.Fatalf("Failed to cache model2: %v", err)
		}
		t.Log("✓ Cached Model #2 (depends on Project)")
	})

	// --- Step 8: Cache LLM Engine (no dependencies) ---
	t.Run("cache_llm_engine", func(t *testing.T) {
		engine := LLMEngine{ID: "engine1", Name: "OpenAI Engine"}
		err := c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "llm_engine", ID: "engine1"},
			nil, // no dependencies
			engine,
			5*time.Minute,
		)
		if err != nil {
			t.Fatalf("Failed to cache llm_engine: %v", err)
		}
		t.Log("✓ Cached LLM Engine (no dependencies)")
	})

	// --- Step 9: Cache Model #3 (depends on Project and LLM Engine) ---
	t.Run("cache_model3", func(t *testing.T) {
		model := Model{
			ID:           "model3",
			Name:         "Custom LLM Model",
			ProjectID:    "project1",
			LLMEnginesID: "engine1",
		}
		err := c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "model", ID: "model3"},
			[]c3e.CacheIdentifier{
				{Type: "project", ID: "project1"},
				{Type: "llm_engine", ID: "engine1"},
			},
			model,
			5*time.Minute,
		)
		if err != nil {
			t.Fatalf("Failed to cache model3: %v", err)
		}
		t.Log("✓ Cached Model #3 (depends on Project and LLM Engine)")
	})

	// --- SCENARIO 1: Invalidate User ---
	// Expected: Should invalidate Project, all Models (1, 2, 3), and Permission
	t.Run("invalidate_user_cascade", func(t *testing.T) {
		t.Log("\n=== SCENARIO 1: Invalidating User ===")

		// Verify all items exist before invalidation
		verifyExists := func(entityType, id string) {
			_, err := c3e.Get[any](ctx, coreMgr, c3e.CacheEncoderTypeJSON,
				c3e.CacheIdentifier{Type: entityType, ID: id}, 0)
			if err != nil {
				t.Fatalf("Expected %s:%s to exist, but got error: %v", entityType, id, err)
			}
		}

		t.Log("Verifying all items exist before invalidation...")
		verifyExists("policy", "policy1")
		verifyExists("role", "role1")
		verifyExists("user", "user1")
		verifyExists("project", "project1")
		verifyExists("model", "model1")
		verifyExists("model", "model2")
		verifyExists("model", "model3")
		verifyExists("permission", "perm1")
		verifyExists("llm_engine", "engine1")
		t.Log("✓ All items confirmed to exist")

		// Invalidate User
		t.Log("Invalidating user:user1...")
		err := coreMgr.Invalidate(ctx, c3e.CacheIdentifier{Type: "user", ID: "user1"})
		if err != nil {
			t.Fatalf("Failed to invalidate user: %v", err)
		}

		// Verify expected items are invalidated
		verifyInvalidated := func(entityType, id string) {
			_, err := c3e.Get[any](ctx, coreMgr, c3e.CacheEncoderTypeJSON,
				c3e.CacheIdentifier{Type: entityType, ID: id}, 0)
			if err != c3e.ErrCacheMiss {
				t.Errorf("Expected %s:%s to be invalidated, but got: %v", entityType, id, err)
			}
		}

		// Verify still exists
		verifyStillExists := func(entityType, id string) {
			_, err := c3e.Get[any](ctx, coreMgr, c3e.CacheEncoderTypeJSON,
				c3e.CacheIdentifier{Type: entityType, ID: id}, 0)
			if err != nil {
				t.Errorf("Expected %s:%s to still exist, but got error: %v", entityType, id, err)
			}
		}

		t.Log("\nVerifying cascade invalidation results:")
		t.Log("  Should be INVALIDATED:")
		verifyInvalidated("user", "user1")
		t.Log("    ✓ user:user1")
		verifyInvalidated("project", "project1")
		t.Log("    ✓ project:project1")
		verifyInvalidated("model", "model1")
		t.Log("    ✓ model:model1")
		verifyInvalidated("model", "model2")
		t.Log("    ✓ model:model2")
		verifyInvalidated("model", "model3")
		t.Log("    ✓ model:model3")
		verifyInvalidated("permission", "perm1")
		t.Log("    ✓ permission:perm1")

		t.Log("  Should STILL EXIST:")
		verifyStillExists("policy", "policy1")
		t.Log("    ✓ policy:policy1")
		verifyStillExists("role", "role1")
		t.Log("    ✓ role:role1")
		verifyStillExists("llm_engine", "engine1")
		t.Log("    ✓ llm_engine:engine1")

		t.Log("\n✅ SCENARIO 1 PASSED: User invalidation correctly cascaded to dependents")
	})

	// --- Re-populate cache for Scenario 2 ---
	t.Run("repopulate_cache", func(t *testing.T) {
		t.Log("\n=== Repopulating cache for Scenario 2 ===")

		// Re-cache only what was invalidated
		user := User{ID: "user1", Name: "John Doe"}
		c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "user", ID: "user1"}, nil, user, 5*time.Minute)

		project := Project{ID: "project1", Name: "AI Project", UserIDs: []string{"user1"}}
		c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "project", ID: "project1"},
			[]c3e.CacheIdentifier{{Type: "user", ID: "user1"}},
			project, 5*time.Minute)

		model1 := Model{ID: "model1", Name: "GPT Model", ProjectID: "project1"}
		c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "model", ID: "model1"},
			[]c3e.CacheIdentifier{{Type: "project", ID: "project1"}},
			model1, 5*time.Minute)

		model2 := Model{ID: "model2", Name: "BERT Model", ProjectID: "project1"}
		c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "model", ID: "model2"},
			[]c3e.CacheIdentifier{{Type: "project", ID: "project1"}},
			model2, 5*time.Minute)

		model3 := Model{ID: "model3", Name: "Custom LLM Model", ProjectID: "project1", LLMEnginesID: "engine1"}
		c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "model", ID: "model3"},
			[]c3e.CacheIdentifier{
				{Type: "project", ID: "project1"},
				{Type: "llm_engine", ID: "engine1"},
			},
			model3, 5*time.Minute)

		permission := Permission{ID: "perm1", Name: "Read Access", PolicyID: "policy1", RoleID: "role1", UserID: "user1"}
		c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
			c3e.CacheIdentifier{Type: "permission", ID: "perm1"},
			[]c3e.CacheIdentifier{
				{Type: "policy", ID: "policy1"},
				{Type: "role", ID: "role1"},
				{Type: "user", ID: "user1"},
			},
			permission, 5*time.Minute)

		t.Log("✓ Cache repopulated")
	})

	// --- SCENARIO 2: Invalidate LLM Engine ---
	// Expected: Should invalidate only Model #3
	t.Run("invalidate_llm_engine_cascade", func(t *testing.T) {
		t.Log("\n=== SCENARIO 2: Invalidating LLM Engine ===")

		// Invalidate LLM Engine
		t.Log("Invalidating llm_engine:engine1...")
		err := coreMgr.Invalidate(ctx, c3e.CacheIdentifier{Type: "llm_engine", ID: "engine1"})
		if err != nil {
			t.Fatalf("Failed to invalidate llm_engine: %v", err)
		}

		// Verify expected items are invalidated
		verifyInvalidated := func(entityType, id string) {
			_, err := c3e.Get[any](ctx, coreMgr, c3e.CacheEncoderTypeJSON,
				c3e.CacheIdentifier{Type: entityType, ID: id}, 0)
			if err != c3e.ErrCacheMiss {
				t.Errorf("Expected %s:%s to be invalidated, but got: %v", entityType, id, err)
			}
		}

		// Verify still exists
		verifyStillExists := func(entityType, id string) {
			_, err := c3e.Get[any](ctx, coreMgr, c3e.CacheEncoderTypeJSON,
				c3e.CacheIdentifier{Type: entityType, ID: id}, 0)
			if err != nil {
				t.Errorf("Expected %s:%s to still exist, but got error: %v", entityType, id, err)
			}
		}

		t.Log("\nVerifying cascade invalidation results:")
		t.Log("  Should be INVALIDATED:")
		verifyInvalidated("llm_engine", "engine1")
		t.Log("    ✓ llm_engine:engine1")
		verifyInvalidated("model", "model3")
		t.Log("    ✓ model:model3 (depends on LLM Engine)")

		t.Log("  Should STILL EXIST:")
		verifyStillExists("policy", "policy1")
		t.Log("    ✓ policy:policy1")
		verifyStillExists("role", "role1")
		t.Log("    ✓ role:role1")
		verifyStillExists("user", "user1")
		t.Log("    ✓ user:user1")
		verifyStillExists("project", "project1")
		t.Log("    ✓ project:project1")
		verifyStillExists("model", "model1")
		t.Log("    ✓ model:model1")
		verifyStillExists("model", "model2")
		t.Log("    ✓ model:model2")
		verifyStillExists("permission", "perm1")
		t.Log("    ✓ permission:perm1")

		t.Log("\n✅ SCENARIO 2 PASSED: LLM Engine invalidation correctly cascaded only to model3")
	})
}

// Example_realWorldCascade demonstrates the cascading invalidation with a visual output
func Example_realWorldCascade() {
	ctx := context.Background()

	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableRetry: true, // Prevent queueing when cache is down
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer client.Close()

	// Flush for clean state
	flushCmd := client.B().Flushdb().Build()
	client.Do(ctx, flushCmd)

	coreMgr, _ := c3e.NewCacheManager(client, false)

	// Build the dependency graph
	fmt.Println("Building cache with dependencies...")

	// Cache entities with dependencies
	policy := Policy{ID: "policy1", Name: "Admin Policy"}
	c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
		c3e.CacheIdentifier{Type: "policy", ID: "policy1"}, nil, policy, 5*time.Minute)

	role := Role{ID: "role1", Name: "Admin Role", PolicyID: "policy1"}
	c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
		c3e.CacheIdentifier{Type: "role", ID: "role1"},
		[]c3e.CacheIdentifier{{Type: "policy", ID: "policy1"}},
		role, 5*time.Minute)

	user := User{ID: "user1", Name: "John"}
	c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
		c3e.CacheIdentifier{Type: "user", ID: "user1"}, nil, user, 5*time.Minute)

	project := Project{ID: "project1", Name: "AI Project", UserIDs: []string{"user1"}}
	c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
		c3e.CacheIdentifier{Type: "project", ID: "project1"},
		[]c3e.CacheIdentifier{{Type: "user", ID: "user1"}},
		project, 5*time.Minute)

	model := Model{ID: "model1", Name: "GPT Model", ProjectID: "project1"}
	c3e.Set(ctx, coreMgr, c3e.CacheEncoderTypeJSON,
		c3e.CacheIdentifier{Type: "model", ID: "model1"},
		[]c3e.CacheIdentifier{{Type: "project", ID: "project1"}},
		model, 5*time.Minute)

	fmt.Println("Cache built successfully")
	fmt.Println("\nInvalidating user:user1...")

	// Invalidate user - should cascade to project and model
	coreMgr.Invalidate(ctx, c3e.CacheIdentifier{Type: "user", ID: "user1"})

	fmt.Println("Cascade complete: user → project → model")
	// Output:
	// Building cache with dependencies...
	// Cache built successfully
	//
	// Invalidating user:user1...
	// Cascade complete: user → project → model
}

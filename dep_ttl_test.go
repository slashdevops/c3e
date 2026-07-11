package c3e

import (
	"context"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"
)

// realClient dials a local Valkey, skipping the test when none is reachable.
func realClient(t *testing.T) valkey.Client {
	t.Helper()
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{"localhost:6379"},
		DisableCache: true,
		DisableRetry: true,
	})
	if err != nil {
		t.Skip("Skipping: no Valkey at localhost:6379")
	}
	if err := client.Do(context.Background(), client.B().Ping().Build()).Error(); err != nil {
		client.Close()
		t.Skip("Skipping: Valkey not reachable at localhost:6379")
	}
	return client
}

// TestSet_dependencyMetadataHasTTL verifies that the reverse-dependency set and
// the forward-dependency list are given a TTL, so they cannot leak when a cache
// entry expires without an explicit Invalidate.
func TestSet_dependencyMetadataHasTTL(t *testing.T) {
	client := realClient(t)
	defer client.Close()
	ctx := context.Background()

	cm, err := NewCacheManager(client, false)
	if err != nil {
		t.Fatalf("NewCacheManager: %v", err)
	}

	child := CacheIdentifier{Type: "c3e_test_child", ID: "1"}
	parent := CacheIdentifier{Type: "c3e_test_parent", ID: "1"}
	revKey := depKey(parent)              // dep:c3e_test_parent:1
	fwdKey := depsForKey(cacheKey(child)) // deps-for:cache:c3e_test_child:1

	// Clean any leftovers, then cache the child depending on the parent.
	client.Do(ctx, client.B().Del().Key(cacheKey(child), revKey, fwdKey).Build())
	if err := cm.Set(ctx, child, []byte(`{"x":1}`), []CacheIdentifier{parent}, time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}
	t.Cleanup(func() {
		client.Do(ctx, client.B().Del().Key(cacheKey(child), revKey, fwdKey).Build())
	})

	for _, k := range []string{revKey, fwdKey} {
		ttl, err := client.Do(ctx, client.B().Ttl().Key(k).Build()).AsInt64()
		if err != nil {
			t.Fatalf("TTL %s: %v", k, err)
		}
		if ttl <= 0 {
			t.Errorf("key %s has TTL %d, want > 0 (no leak)", k, ttl)
		}
	}
}

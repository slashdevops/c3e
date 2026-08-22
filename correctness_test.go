package c3e

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"
)

// flushKeys removes the keys a test used, before and after it runs.
func flushKeys(t *testing.T, client valkey.Client, keys ...string) {
	t.Helper()

	del := func() {
		client.Do(context.Background(), client.B().Del().Key(keys...).Build())
	}

	del()
	t.Cleanup(del)
}

// TestSet_dependencyTTLNeverShrinks is the regression test for the defect that
// let a revoked role keep working for hours.
//
// A reverse-dependency set is shared by every entry that depends on the same
// thing. An unconditional EXPIRE let the last writer shorten it below the TTL of
// an entry already in the set, and jittered TTLs made that ordinary: the set
// could expire well before its dependents, after which Invalidate found nothing
// to cascade to and reported success.
func TestSet_dependencyTTLNeverShrinks(t *testing.T) {
	client := realClient(t)
	defer client.Close()

	ctx := context.Background()

	cm, err := NewCacheManager(client, false)
	if err != nil {
		t.Fatalf("NewCacheManager: %v", err)
	}

	parent := CacheIdentifier{Type: "c3e_ttl_parent", ID: "1"}
	long := CacheIdentifier{Type: "c3e_ttl_long", ID: "1"}
	short := CacheIdentifier{Type: "c3e_ttl_short", ID: "1"}
	revKey := depKey(parent)

	flushKeys(t, client, revKey,
		cacheKey(long), cacheKey(short),
		depsForKey(cacheKey(long)), depsForKey(cacheKey(short)),
	)

	// First dependent: a long-lived entry. NX has to set the TTL here, because
	// GT alone treats a key with no expiry as infinite and would leave the set
	// persistent forever.
	if err := cm.Set(ctx, long, []byte(`{"x":1}`), []CacheIdentifier{parent}, time.Hour); err != nil {
		t.Fatalf("Set long: %v", err)
	}

	ttlAfterLong, err := client.Do(ctx, client.B().Ttl().Key(revKey).Build()).AsInt64()
	if err != nil {
		t.Fatalf("TTL after long: %v", err)
	}

	if ttlAfterLong <= 0 {
		t.Fatalf("dependency set has no TTL (%d); it would leak", ttlAfterLong)
	}

	// Second dependent, deliberately short-lived. Before the fix this rewrote
	// the shared set's TTL down to 3s.
	if err := cm.Set(ctx, short, []byte(`{"x":2}`), []CacheIdentifier{parent}, 3*time.Second); err != nil {
		t.Fatalf("Set short: %v", err)
	}

	ttlAfterShort, err := client.Do(ctx, client.B().Ttl().Key(revKey).Build()).AsInt64()
	if err != nil {
		t.Fatalf("TTL after short: %v", err)
	}

	if ttlAfterShort <= 10 {
		t.Errorf("dependency set TTL was shortened to %ds by a later, shorter-lived dependent; "+
			"it must outlive every member (was %ds)", ttlAfterShort, ttlAfterLong)
	}
}

// TestSet_dependencyTTLGrows checks the other half: a longer-lived dependent
// must raise the shared set's TTL, otherwise the set still expires first.
func TestSet_dependencyTTLGrows(t *testing.T) {
	client := realClient(t)
	defer client.Close()

	ctx := context.Background()

	cm, err := NewCacheManager(client, false)
	if err != nil {
		t.Fatalf("NewCacheManager: %v", err)
	}

	parent := CacheIdentifier{Type: "c3e_grow_parent", ID: "1"}
	first := CacheIdentifier{Type: "c3e_grow_first", ID: "1"}
	second := CacheIdentifier{Type: "c3e_grow_second", ID: "1"}
	revKey := depKey(parent)

	flushKeys(t, client, revKey,
		cacheKey(first), cacheKey(second),
		depsForKey(cacheKey(first)), depsForKey(cacheKey(second)),
	)

	if err := cm.Set(ctx, first, []byte(`{"x":1}`), []CacheIdentifier{parent}, 30*time.Second); err != nil {
		t.Fatalf("Set first: %v", err)
	}

	if err := cm.Set(ctx, second, []byte(`{"x":2}`), []CacheIdentifier{parent}, time.Hour); err != nil {
		t.Fatalf("Set second: %v", err)
	}

	ttl, err := client.Do(ctx, client.B().Ttl().Key(revKey).Build()).AsInt64()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}

	if ttl <= 60 {
		t.Errorf("dependency set TTL is %ds; a longer-lived dependent must raise it", ttl)
	}
}

// TestGet_corruptPayloadHealsInsteadOfFailing covers a payload that no longer
// decodes inside a wrapper that still parses — what an EncoderType change on a
// warm cache, or a changed struct during a rolling deploy, produces.
//
// It used to be returned to the caller as an error, so every read of that key
// failed for the whole hard TTL. It must be treated as a miss.
func TestGet_corruptPayloadHealsInsteadOfFailing(t *testing.T) {
	client := realClient(t)
	defer client.Close()

	ctx := context.Background()

	cm, err := NewCacheManager(client, false)
	if err != nil {
		t.Fatalf("NewCacheManager: %v", err)
	}

	scm, err := NewSafeCacheManager(cm, SafeCacheManagerConfig{
		HardTTL:      time.Hour,
		SoftTTL:      30 * time.Minute,
		EncoderType:  CacheEncoderTypeJSON,
		QueryTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewSafeCacheManager: %v", err)
	}

	id := CacheIdentifier{Type: "c3e_corrupt", ID: "1"}
	flushKeys(t, client, cacheKey(id), depsForKey(cacheKey(id)))

	// A valid wrapper whose payload the configured decoder cannot read.
	wrapper, err := json.Marshal(CachedItem{
		Data:      []byte("this is not valid json"),
		RefreshAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("marshal wrapper: %v", err)
	}

	if err := client.Do(ctx,
		client.B().Set().Key(cacheKey(id)).Value(valkey.BinaryString(wrapper)).ExSeconds(3600).Build(),
	).Error(); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	calls := 0

	var got map[string]any

	err = scm.Get(ctx, id, &got, func(context.Context) (any, []CacheIdentifier, error) {
		calls++
		return map[string]any{"healed": true}, nil, nil
	})
	if err != nil {
		t.Fatalf("Get returned an error instead of refetching: %v", err)
	}

	if calls != 1 {
		t.Errorf("fetcher called %d times, want 1 — a corrupt payload must be treated as a miss", calls)
	}

	if got["healed"] != true {
		t.Errorf("got %v, want the refetched value", got)
	}
}

// TestGet_unencodableValueIsStillReturned covers the inconsistency that made a
// caching problem into a request failure: a failing Set was logged and
// swallowed, but a failing *encode* three lines earlier was returned to the
// caller — even though the source of truth had already answered.
func TestGet_unencodableValueIsStillReturned(t *testing.T) {
	client := realClient(t)
	defer client.Close()

	ctx := context.Background()

	cm, err := NewCacheManager(client, false)
	if err != nil {
		t.Fatalf("NewCacheManager: %v", err)
	}

	scm, err := NewSafeCacheManager(cm, SafeCacheManagerConfig{
		HardTTL:      time.Hour,
		SoftTTL:      30 * time.Minute,
		EncoderType:  CacheEncoderTypeJSON,
		QueryTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewSafeCacheManager: %v", err)
	}

	id := CacheIdentifier{Type: "c3e_unencodable", ID: "1"}
	flushKeys(t, client, cacheKey(id), depsForKey(cacheKey(id)))

	// A channel cannot be marshalled by encoding/json or encoded by gob.
	want := map[string]any{"ok": "value", "bad": make(chan int)}

	var got map[string]any

	if err := scm.Get(ctx, id, &got, func(context.Context) (any, []CacheIdentifier, error) {
		return want, nil, nil
	}); err != nil {
		t.Fatalf("a value that cannot be cached must still be returned: %v", err)
	}

	if got["ok"] != "value" {
		t.Errorf("got %v, want the fetched value delivered intact", got)
	}

	// And nothing should have been stored for it.
	if n, err := client.Do(ctx, client.B().Exists().Key(cacheKey(id)).Build()).AsInt64(); err == nil && n != 0 {
		t.Errorf("an unencodable value must not leave an entry behind")
	}
}

// TestInvalidate_cascadesAcrossLevels guards the batched breadth-first walk. The
// graph is deliberately both deep and wide, because the batching groups by level
// and a per-level bug would not show up on a single chain.
func TestInvalidate_cascadesAcrossLevels(t *testing.T) {
	client := realClient(t)
	defer client.Close()

	ctx := context.Background()

	cm, err := NewCacheManager(client, false)
	if err != nil {
		t.Fatalf("NewCacheManager: %v", err)
	}

	root := CacheIdentifier{Type: "c3e_casc_root", ID: "1"}

	// level1: three entries depending on root.
	// level2: two entries depending on each level1 entry.
	var all []CacheIdentifier

	level1 := make([]CacheIdentifier, 0, 3)

	for i := range 3 {
		level1 = append(level1, CacheIdentifier{Type: "c3e_casc_l1", ID: fmt.Sprintf("%d", i)})
	}

	keys := []string{cacheKey(root), depKey(root), depsForKey(cacheKey(root))}

	for _, l1 := range level1 {
		all = append(all, l1)
		keys = append(keys, cacheKey(l1), depKey(l1), depsForKey(cacheKey(l1)))

		for j := range 2 {
			l2 := CacheIdentifier{Type: "c3e_casc_l2", ID: fmt.Sprintf("%s-%d", l1.ID, j)}
			all = append(all, l2)
			keys = append(keys, cacheKey(l2), depKey(l2), depsForKey(cacheKey(l2)))
		}
	}

	flushKeys(t, client, keys...)

	for _, l1 := range level1 {
		if err := cm.Set(ctx, l1, []byte(`{"l":1}`), []CacheIdentifier{root}, time.Hour); err != nil {
			t.Fatalf("Set l1: %v", err)
		}

		for j := range 2 {
			l2 := CacheIdentifier{Type: "c3e_casc_l2", ID: fmt.Sprintf("%s-%d", l1.ID, j)}
			if err := cm.Set(ctx, l2, []byte(`{"l":2}`), []CacheIdentifier{l1}, time.Hour); err != nil {
				t.Fatalf("Set l2: %v", err)
			}
		}
	}

	for _, id := range all {
		if n, _ := client.Do(ctx, client.B().Exists().Key(cacheKey(id)).Build()).AsInt64(); n != 1 {
			t.Fatalf("precondition: %s should be cached", id)
		}
	}

	if err := cm.Invalidate(ctx, root); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}

	for _, id := range all {
		if n, _ := client.Do(ctx, client.B().Exists().Key(cacheKey(id)).Build()).AsInt64(); n != 0 {
			t.Errorf("%s survived the cascade", id)
		}
	}
}

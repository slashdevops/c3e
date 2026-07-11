# Usage

All snippets assume a constructed `*c3e.SafeCacheManager` (see the
[root README](../README.md#quick-start) for setup).

Runnable, godoc-rendered examples live in the `example_*_test.go` files and on
[pkg.go.dev](https://pkg.go.dev/github.com/slashdevops/c3e#pkg-examples).

## Basic cache usage

```go
type Project struct {
    ID    string `json:"id"`
    Name  string `json:"name"`
    Owner string `json:"owner"`
}

func getProject(ctx context.Context, cache *c3e.SafeCacheManager, projectID string) (*Project, error) {
    identifier := c3e.CacheIdentifier{Type: "project", ID: projectID}

    var project Project
    err := cache.Get(ctx, identifier, &project, func(ctx context.Context) (any, []c3e.CacheIdentifier, error) {
        p, err := db.GetProject(ctx, projectID)
        if err != nil {
            return nil, nil, err
        }
        // Declare what this value depends on.
        deps := []c3e.CacheIdentifier{
            {Type: "user", ID: p.Owner}, // project depends on its owner
        }
        return p, deps, nil
    })
    return &project, err
}
```

## Type-safe generic function (recommended ⭐)

`GetSafe[T]` gives you compile-time type safety without a wrapper type:

```go
func getProjectTypeSafe(ctx context.Context, cache *c3e.SafeCacheManager, projectID string) (Project, error) {
    identifier := c3e.CacheIdentifier{Type: "project", ID: projectID}

    return c3e.GetSafe(ctx, cache, identifier, func(ctx context.Context) (Project, []c3e.CacheIdentifier, error) {
        p, err := db.GetProject(ctx, projectID)
        if err != nil {
            return Project{}, nil, err
        }
        deps := []c3e.CacheIdentifier{{Type: "user", ID: p.Owner}}
        return p, deps, nil
    })
}
```

## TypedSafeCacheManager

When you repeatedly Get/Invalidate one type, bind it once:

```go
projectCache := c3e.NewTypedSafeCacheManager[Project](safeCacheManager)

identifier := c3e.CacheIdentifier{Type: "project", ID: "123"}
project, err := projectCache.Get(ctx, identifier, func(ctx context.Context) (Project, []c3e.CacheIdentifier, error) {
    p, err := db.GetProject(ctx, "123")
    if err != nil {
        return Project{}, nil, err
    }
    return p, []c3e.CacheIdentifier{{Type: "user", ID: p.Owner}}, nil
})
```

## Dependency tracking & cascading invalidation

```go
// Cache a project that depends on several entities.
identifier := c3e.CacheIdentifier{Type: "project", ID: "proj-123"}

var project Project
cache.Get(ctx, identifier, &project, func(ctx context.Context) (any, []c3e.CacheIdentifier, error) {
    p, err := db.GetProject(ctx, "proj-123")
    if err != nil {
        return nil, nil, err
    }
    deps := []c3e.CacheIdentifier{
        {Type: "user", ID: p.Owner},         // owner
        {Type: "organization", ID: p.OrgID}, // organization
        {Type: "team", ID: p.TeamID},        // team
    }
    return p, deps, nil
})

// Later — when the owner changes, every dependent is dropped automatically.
cache.Invalidate(ctx, c3e.CacheIdentifier{Type: "user", ID: "user-456"})
```

See [Limitations](limitations.md) for the exact invalidation contract (downward
only, eventually consistent, complete-dependencies requirement).

## GetOrFetch convenience function

```go
identifier := c3e.CacheIdentifier{Type: "settings", ID: "app-config"}

settings, err := c3e.GetOrFetch(ctx, safeCacheManager, identifier,
    func(ctx context.Context) (AppSettings, []c3e.CacheIdentifier, error) {
        s, err := db.GetSettings(ctx)
        return s, nil, err // no dependencies
    })
```

## Per-request encoder

`GetWithEncoder` / `SetWithEncoder` select JSON or Gob per call instead of using
the manager's default. A value must always be read back with the same encoder
that wrote it (see [Limitations](limitations.md)).

```go
cfg := c3e.SafeCacheManagerConfig{
    HardTTL:     2 * time.Hour,
    SoftTTL:     1 * time.Hour,
    EncoderType: c3e.CacheEncoderTypeGob, // Gob is compact for Go-only structs
}
project, err := c3e.GetWithEncoder(ctx, cacheManager, cfg, identifier, fetchProject)
```

## Advanced patterns

### Multi-level caching

Cache an aggregate that depends on individually-cached items:

```go
func getCachedProjectSummary(ctx context.Context, projectID string) (Summary, error) {
    identifier := c3e.CacheIdentifier{Type: "project-summary", ID: projectID}

    return c3e.GetSafe(ctx, cache, identifier, func(ctx context.Context) (Summary, []c3e.CacheIdentifier, error) {
        project, _ := getProject(ctx, projectID)
        owner, _ := getUser(ctx, project.OwnerID)
        stats, _ := getStats(ctx, projectID)

        summary := Summary{Project: project, Owner: owner, Stats: stats}
        deps := []c3e.CacheIdentifier{
            {Type: "project", ID: projectID},
            {Type: "user", ID: project.OwnerID},
            {Type: "stats", ID: projectID},
        }
        return summary, deps, nil
    })
}
```

### Conditional caching

Return `c3e.ErrCacheMiss` from the fetcher to skip caching a particular value:

```go
func getUser(ctx context.Context, userID string) (User, error) {
    identifier := c3e.CacheIdentifier{Type: "user", ID: userID}

    return c3e.GetSafe(ctx, cache, identifier, func(ctx context.Context) (User, []c3e.CacheIdentifier, error) {
        user, err := db.GetUser(ctx, userID)
        if err != nil {
            return User{}, nil, err
        }
        if user.IsAdmin {
            return user, nil, c3e.ErrCacheMiss // always fetch admins fresh
        }
        return user, nil, nil
    })
}
```

### Preemptive refresh

```go
// Force a refresh on the next Get.
func refreshProjectCache(ctx context.Context, projectID string) error {
    return cache.Invalidate(ctx, c3e.CacheIdentifier{Type: "project", ID: projectID})
}
```

### Bulk invalidation

```go
func invalidateUserData(ctx context.Context, userID string) error {
    ids := []c3e.CacheIdentifier{
        {Type: "user", ID: userID},
        {Type: "user-profile", ID: userID},
        {Type: "user-settings", ID: userID},
        {Type: "user-stats", ID: userID},
    }
    for _, id := range ids {
        if err := cache.Invalidate(ctx, id); err != nil {
            return fmt.Errorf("failed to invalidate %v: %w", id, err)
        }
    }
    return nil
}
```

## Testing

Unit tests can mock the `CacheRepository` interface; integration tests run
against a real Valkey. The suite in this repository skips the real-Valkey cases
when none is reachable at `localhost:6379`, and the heavier end-to-end cases are
guarded behind the `integration` build tag:

```bash
# Unit tests (no Valkey required)
go test -race ./...

# Full suite against a local Valkey
docker run -d --rm -p 6379:6379 valkey/valkey:latest
go test -race -tags=integration ./...
```

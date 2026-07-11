package c3e

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/singleflight"
)

// TypedFetcherFunc is a type-safe fetcher function.
type TypedFetcherFunc[T any] func(ctx context.Context) (data T, dependencies []CacheIdentifier, err error)

// TypedSafeCacheManager is a generic wrapper around SafeCacheManager that provides
// type-safe operations with a specific data type.
type TypedSafeCacheManager[T any] struct {
	manager *SafeCacheManager
}

// NewTypedSafeCacheManager creates a type-safe cache manager for a specific type.
func NewTypedSafeCacheManager[T any](manager *SafeCacheManager) *TypedSafeCacheManager[T] {
	return &TypedSafeCacheManager[T]{
		manager: manager,
	}
}

// GetSafe retrieves an item from the SafeCacheManager with type safety.
// This is a standalone generic function alternative to TypedSafeCacheManager.
func GetSafe[T any](ctx context.Context, manager *SafeCacheManager, identifier CacheIdentifier, fetcher TypedFetcherFunc[T]) (T, error) {
	var result T
	untypedFetcher := func(ctx context.Context) (any, []CacheIdentifier, error) {
		return fetcher(ctx)
	}

	err := manager.Get(ctx, identifier, &result, untypedFetcher)

	return result, err
}

// Get retrieves an item with type safety.
func (m *TypedSafeCacheManager[T]) Get(ctx context.Context, identifier CacheIdentifier, fetcher TypedFetcherFunc[T]) (T, error) {
	var result T

	// Wrap the typed fetcher into the untyped one
	untypedFetcher := func(ctx context.Context) (any, []CacheIdentifier, error) {
		return fetcher(ctx)
	}

	err := m.manager.Get(ctx, identifier, &result, untypedFetcher)

	return result, err
}

// Invalidate provides direct access to the underlying invalidation.
func (m *TypedSafeCacheManager[T]) Invalidate(ctx context.Context, identifier CacheIdentifier) error {
	return m.manager.Invalidate(ctx, identifier)
}

// GetOrFetch is a convenience method that combines Get with a direct database query function.
func (m *TypedSafeCacheManager[T]) GetOrFetch(ctx context.Context, identifier CacheIdentifier, queryFunc func(ctx context.Context) (data T, dependencies []CacheIdentifier, err error)) (T, error) {
	return GetOrFetch(ctx, m.manager, identifier, queryFunc)
}

// GetOrFetch is a convenience method that combines Get with a direct database query function.
// It uses the encoder type configured in the SafeCacheManager.
func GetOrFetch[T any](
	ctx context.Context,
	manager *SafeCacheManager,
	identifier CacheIdentifier,
	queryFunc func(ctx context.Context) (data T, dependencies []CacheIdentifier, err error),
) (T, error) {
	var result T

	fetcher := func(ctx context.Context) (any, []CacheIdentifier, error) {
		data, dependencies, err := queryFunc(ctx)
		if err != nil {
			return nil, nil, err
		}

		return data, dependencies, nil
	}

	err := manager.Get(ctx, identifier, &result, fetcher)

	return result, err
}

// GetWithEncoder retrieves an item with explicit encoder type specification.
// This allows per-request encoder selection instead of using the manager's default.
func GetWithEncoder[T any](
	ctx context.Context,
	cacheManager *CacheManager,
	config SafeCacheManagerConfig,
	identifier CacheIdentifier,
	fetcher TypedFetcherFunc[T],
) (T, error) {
	var result T

	// This per-request path builds the manager directly instead of going
	// through NewSafeCacheManager, so apply the same defaults the constructor
	// would: a non-zero query timeout (a zero timeout yields an
	// already-expired context and forces every read down the fallback path)
	// and a non-nil logger.
	if config.QueryTimeout == 0 {
		config.QueryTimeout = DefaultSafeCacheManagerQueryTimeout
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Create a temporary manager with the specific encoder
	tempManager := &SafeCacheManager{
		cache:  cacheManager,
		sfg:    &singleflight.Group{},
		logger: logger,
		cfg:    config,
		hooks:  config.Hooks,
	}

	untypedFetcher := func(ctx context.Context) (any, []CacheIdentifier, error) {
		return fetcher(ctx)
	}

	err := tempManager.Get(ctx, identifier, &result, untypedFetcher)
	return result, err
}

// SetWithEncoder sets a value in cache with explicit encoder type.
func SetWithEncoder[T any](
	ctx context.Context,
	cacheManager *CacheManager,
	encoderType CacheEncoderType,
	identifier CacheIdentifier,
	value T,
	dependencies []CacheIdentifier,
	ttl time.Duration,
) error {
	var serializedData []byte
	var err error

	// Encode based on type
	switch encoderType {
	case CacheEncoderTypeGob:
		serializedData, err = EncodeGob(value)
	case CacheEncoderTypeJSON:
		fallthrough
	default:
		serializedData, err = EncodeJSON(value)
	}

	if err != nil {
		return fmt.Errorf("failed to encode value: %w", err)
	}

	// Wrap in CachedItem
	item := CachedItem{
		Data:      serializedData,
		RefreshAt: time.Now().Add(ttl / 2).Unix(), // Default soft TTL is half of hard TTL
	}

	wrapperData, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to marshal wrapper: %w", err)
	}

	return cacheManager.Set(ctx, identifier, wrapperData, dependencies, ttl)
}

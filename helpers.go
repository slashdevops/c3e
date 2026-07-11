package c3e

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// Cache-specific errors
var (
	// ErrCacheMiss is a specific error returned when an item is not in the cache.
	ErrCacheMiss = errors.New("cache: item not found")

	// ErrCommandExecution is returned when a cache command fails to execute.
	ErrCommandExecution = errors.New("cache: command execution failed")

	// ErrGetOldDependencies is returned when fetching old dependencies fails.
	ErrGetOldDependencies = errors.New("cache: failed to get old dependencies")

	// ErrGetDependents is returned when fetching dependents fails.
	ErrGetDependents = errors.New("cache: failed to get dependents")
)

// CacheEncoderType is the type of encoder to use when encoding and decoding values in cache
type CacheEncoderType string

const (
	// CacheEncoderTypeJSON uses the standard encoding/json package.
	// This is the default and produces human-readable cache values.
	CacheEncoderTypeJSON CacheEncoderType = "json"

	// CacheEncoderTypeGob uses the encoding/gob package.
	// This is more efficient for Go-specific types but less portable.
	CacheEncoderTypeGob CacheEncoderType = "gob"
)

func (e CacheEncoderType) String() string {
	return string(e)
}

// CacheIdentifier represents a cache entity with its type and ID.
// This is used to uniquely identify cached items.
// Example:
//
//	CacheIdentifier{Type: "project", ID: "123"}
//	CacheIdentifier{Type: "user", ID: "44b1dfa5-b15e-4f03-944f-cbf61dfca144"}
type CacheIdentifier struct {
	Type string
	ID   string
}

// String returns the string representation of the CacheIdentifier.
// e.g., "project:123"
func (e CacheIdentifier) String() string {
	return fmt.Sprintf("%s:%s", e.Type, e.ID)
}

// cacheKey generates the key for the actual cached data.
// e.g., "cache:project:123"
func cacheKey(identifier CacheIdentifier) string {
	return fmt.Sprintf("cache:%s:%s", identifier.Type, identifier.ID)
}

// depKey generates the key for the reverse dependency Set.
// e.g., "dep:user:456"
func depKey(identifier CacheIdentifier) string {
	return fmt.Sprintf("dep:%s:%s", identifier.Type, identifier.ID)
}

// depsForKey generates the key for the forward dependency Set.
// e.g., "deps-for:cache:project:123"
func depsForKey(cKey string) string { return fmt.Sprintf("deps-for:%s", cKey) }

// entityKeyFromDepKey converts a dep key back into an entity key.
// e.g., "dep:project:123" -> "project:123"
func entityKeyFromDepKey(dKey string) string {
	return strings.TrimPrefix(dKey, "dep:")
}

// depKeyFromCacheKey converts a cache key into its corresponding dep key.
// e.g., "cache:project:123" -> "dep:project:123"
func depKeyFromCacheKey(cKey string) string {
	return strings.Replace(cKey, "cache:", "dep:", 1)
}

// CachedItem is the wrapper struct we store in Valkey to manage stale-while-revalidate.
type CachedItem struct {
	// Data holds the serialized object.
	// We use []byte instead of json.RawMessage to ensure safe marshaling of both
	// JSON (text) and Gob (binary) data. This means the data will be base64 encoded
	// in the JSON wrapper, which adds some overhead but guarantees correctness.
	Data []byte `json:"data"`

	// RefreshAt is the Unix timestamp when this item becomes "stale"
	RefreshAt int64 `json:"refresh_at"`
}

// jitterTTL applies a random +/- jitter to a base TTL.
func jitterTTL(baseTTL time.Duration, jitterPercent float64) time.Duration {
	if jitterPercent <= 0 || baseTTL == 0 {
		return baseTTL
	}

	jitter := float64(baseTTL) * jitterPercent
	randomJitter := (rand.Float64() * 2) - 1 // -1.0 to +1.0

	finalJitter := time.Duration(randomJitter * jitter)
	finalTTL := baseTTL + finalJitter

	// Ensure it's at least a reasonable minimum (e.g., 1s)
	if finalTTL < time.Second {
		return time.Second
	}

	return finalTTL
}

// EncodeJSON encodes a value to JSON bytes
func EncodeJSON[T any](value T) ([]byte, error) {
	return json.Marshal(value)
}

// DecodeJSON decodes JSON bytes into a value
func DecodeJSON[T any](data []byte) (T, error) {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return value, err
	}

	return value, nil
}

// EncodeGob encodes a value to Gob bytes
func EncodeGob[T any](value T) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(value); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// DecodeGob decodes Gob bytes into a value
func DecodeGob[T any](data []byte) (T, error) {
	var value T
	buf := bytes.NewReader(data)
	if err := gob.NewDecoder(buf).Decode(&value); err != nil {
		return value, err
	}

	return value, nil
}

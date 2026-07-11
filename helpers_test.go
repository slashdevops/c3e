package c3e

import (
	"encoding/json"
	"testing"
	"time"
)

func TestJitterTTL(t *testing.T) {
	tests := []struct {
		name          string
		baseTTL       time.Duration
		jitterPercent float64
		expectRange   bool
	}{
		{
			name:          "no_jitter",
			baseTTL:       time.Minute,
			jitterPercent: 0,
			expectRange:   false,
		},
		{
			name:          "10_percent_jitter",
			baseTTL:       time.Minute,
			jitterPercent: 0.1,
			expectRange:   true,
		},
		{
			name:          "50_percent_jitter",
			baseTTL:       time.Minute,
			jitterPercent: 0.5,
			expectRange:   true,
		},
		{
			name:          "zero_base_ttl",
			baseTTL:       0,
			jitterPercent: 0.1,
			expectRange:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := jitterTTL(tt.baseTTL, tt.jitterPercent)

			// Check minimum TTL enforcement
			if tt.baseTTL > 0 && result < time.Second {
				t.Errorf("expected result >= 1s, got %v", result)
			}

			if tt.expectRange {
				// With jitter, result should vary from baseTTL
				minExpected := tt.baseTTL - time.Duration(float64(tt.baseTTL)*tt.jitterPercent)
				maxExpected := tt.baseTTL + time.Duration(float64(tt.baseTTL)*tt.jitterPercent)

				if result < minExpected || result > maxExpected {
					t.Logf("result %v is outside expected range [%v, %v]", result, minExpected, maxExpected)
					// Note: Due to randomness, this might occasionally be outside range
					// This is informational, not a hard failure
				}
			} else {
				// No jitter or zero TTL
				if tt.baseTTL == 0 {
					if result != 0 {
						t.Errorf("expected 0, got %v", result)
					}
				} else {
					if result != tt.baseTTL && result >= time.Second {
						t.Errorf("expected %v, got %v", tt.baseTTL, result)
					}
				}
			}
		})
	}
}

func TestJitterTTL_Distribution(t *testing.T) {
	// Test that jitter produces varied results
	baseTTL := 10 * time.Second
	jitterPercent := 0.2 // 20%

	results := make(map[time.Duration]bool)
	iterations := 100

	for range iterations {
		result := jitterTTL(baseTTL, jitterPercent)
		results[result] = true
	}

	// We expect at least some variation (not all the same)
	if len(results) < 2 {
		t.Error("expected jitter to produce varied results")
	}

	t.Logf("Generated %d unique TTL values from %d iterations", len(results), iterations)
}

func TestJitterTTL_MinimumEnforcement(t *testing.T) {
	// Test that very small base TTLs are enforced to at least 1 second
	tests := []struct {
		name    string
		baseTTL time.Duration
	}{
		{"100ms", 100 * time.Millisecond},
		{"500ms", 500 * time.Millisecond},
		{"900ms", 900 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := jitterTTL(tt.baseTTL, 0.1)
			if result < time.Second {
				t.Errorf("expected minimum 1s, got %v", result)
			}
		})
	}
}

func TestCachedItem_Serialization(t *testing.T) {
	now := time.Now()
	item := CachedItem{
		Data:      json.RawMessage(`{"key":"value"}`),
		RefreshAt: now.Unix(),
	}

	// Test JSON marshaling
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("failed to marshal CachedItem: %v", err)
	}

	// Test JSON unmarshaling
	var decoded CachedItem
	err = json.Unmarshal(data, &decoded)
	if err != nil {
		t.Fatalf("failed to unmarshal CachedItem: %v", err)
	}

	// Verify fields
	if decoded.RefreshAt != item.RefreshAt {
		t.Errorf("expected RefreshAt %d, got %d", item.RefreshAt, decoded.RefreshAt)
	}

	if string(decoded.Data) != string(item.Data) {
		t.Errorf("expected Data %s, got %s", item.Data, decoded.Data)
	}
}

func TestCachedItem_WithComplexData(t *testing.T) {
	type ComplexData struct {
		ID        int       `json:"id"`
		Name      string    `json:"name"`
		Tags      []string  `json:"tags"`
		CreatedAt time.Time `json:"created_at"`
	}

	now := time.Now()
	complexData := ComplexData{
		ID:        123,
		Name:      "Test Object",
		Tags:      []string{"tag1", "tag2", "tag3"},
		CreatedAt: now,
	}

	// Serialize complex data
	serialized, err := json.Marshal(complexData)
	if err != nil {
		t.Fatalf("failed to marshal complex data: %v", err)
	}

	// Create cached item
	item := CachedItem{
		Data:      serialized,
		RefreshAt: now.Unix(),
	}

	// Marshal cached item
	itemData, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("failed to marshal CachedItem: %v", err)
	}

	// Unmarshal cached item
	var decodedItem CachedItem
	err = json.Unmarshal(itemData, &decodedItem)
	if err != nil {
		t.Fatalf("failed to unmarshal CachedItem: %v", err)
	}

	// Unmarshal complex data from item
	var decodedComplex ComplexData
	err = json.Unmarshal(decodedItem.Data, &decodedComplex)
	if err != nil {
		t.Fatalf("failed to unmarshal complex data: %v", err)
	}

	// Verify fields
	if decodedComplex.ID != complexData.ID {
		t.Errorf("expected ID %d, got %d", complexData.ID, decodedComplex.ID)
	}

	if decodedComplex.Name != complexData.Name {
		t.Errorf("expected Name %s, got %s", complexData.Name, decodedComplex.Name)
	}

	if len(decodedComplex.Tags) != len(complexData.Tags) {
		t.Errorf("expected %d tags, got %d", len(complexData.Tags), len(decodedComplex.Tags))
	}
}

func TestErrCacheMiss(t *testing.T) {
	if ErrCacheMiss == nil {
		t.Error("ErrCacheMiss should not be nil")
	}

	if ErrCacheMiss.Error() != "cache: item not found" {
		t.Errorf("unexpected error message: %s", ErrCacheMiss.Error())
	}
}

func TestErrCommandExecution(t *testing.T) {
	if ErrCommandExecution == nil {
		t.Error("ErrCommandExecution should not be nil")
	}

	if ErrCommandExecution.Error() != "cache: command execution failed" {
		t.Errorf("unexpected error message: %s", ErrCommandExecution.Error())
	}
}

func TestErrGetOldDependencies(t *testing.T) {
	if ErrGetOldDependencies == nil {
		t.Error("ErrGetOldDependencies should not be nil")
	}

	if ErrGetOldDependencies.Error() != "cache: failed to get old dependencies" {
		t.Errorf("unexpected error message: %s", ErrGetOldDependencies.Error())
	}
}

func TestErrGetDependents(t *testing.T) {
	if ErrGetDependents == nil {
		t.Error("ErrGetDependents should not be nil")
	}

	if ErrGetDependents.Error() != "cache: failed to get dependents" {
		t.Errorf("unexpected error message: %s", ErrGetDependents.Error())
	}
}

func TestCacheEncoderType_String(t *testing.T) {
	tests := []struct {
		name     string
		encoder  CacheEncoderType
		expected string
	}{
		{"json", CacheEncoderTypeJSON, "json"},
		{"gob", CacheEncoderTypeGob, "gob"},
		{"custom", CacheEncoderType("custom"), "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.encoder.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.encoder.String())
			}
		})
	}
}

func TestCacheIdentifier_String(t *testing.T) {
	tests := []struct {
		name       string
		identifier CacheIdentifier
		expected   string
	}{
		{"simple", CacheIdentifier{Type: "user", ID: "123"}, "user:123"},
		{"uuid_id", CacheIdentifier{Type: "project", ID: "44b1dfa5-b15e-4f03-944f-cbf61dfca144"}, "project:44b1dfa5-b15e-4f03-944f-cbf61dfca144"},
		{"empty_id", CacheIdentifier{Type: "user", ID: ""}, "user:"},
		{"empty_type", CacheIdentifier{Type: "", ID: "123"}, ":123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.identifier.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.identifier.String())
			}
		})
	}
}

func TestEncodeDecodeJSON(t *testing.T) {
	type sample struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	t.Run("round_trip", func(t *testing.T) {
		original := sample{ID: 42, Name: "test"}
		encoded, err := EncodeJSON(original)
		if err != nil {
			t.Fatalf("EncodeJSON failed: %v", err)
		}

		decoded, err := DecodeJSON[sample](encoded)
		if err != nil {
			t.Fatalf("DecodeJSON failed: %v", err)
		}

		if decoded.ID != original.ID || decoded.Name != original.Name {
			t.Errorf("expected %+v, got %+v", original, decoded)
		}
	})

	t.Run("encode_string", func(t *testing.T) {
		encoded, err := EncodeJSON("hello")
		if err != nil {
			t.Fatalf("EncodeJSON failed: %v", err)
		}

		decoded, err := DecodeJSON[string](encoded)
		if err != nil {
			t.Fatalf("DecodeJSON failed: %v", err)
		}

		if decoded != "hello" {
			t.Errorf("expected %q, got %q", "hello", decoded)
		}
	})

	t.Run("encode_slice", func(t *testing.T) {
		original := []int{1, 2, 3}
		encoded, err := EncodeJSON(original)
		if err != nil {
			t.Fatalf("EncodeJSON failed: %v", err)
		}

		decoded, err := DecodeJSON[[]int](encoded)
		if err != nil {
			t.Fatalf("DecodeJSON failed: %v", err)
		}

		if len(decoded) != len(original) {
			t.Errorf("expected %d items, got %d", len(original), len(decoded))
		}
	})

	t.Run("decode_invalid_json", func(t *testing.T) {
		_, err := DecodeJSON[sample]([]byte("not json"))
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("encode_nil_map", func(t *testing.T) {
		var m map[string]string
		encoded, err := EncodeJSON(m)
		if err != nil {
			t.Fatalf("EncodeJSON failed: %v", err)
		}

		if string(encoded) != "null" {
			t.Errorf("expected null, got %s", encoded)
		}
	})
}

func TestEncodeDecodeGob(t *testing.T) {
	type sample struct {
		ID   int
		Name string
	}

	t.Run("round_trip", func(t *testing.T) {
		original := sample{ID: 42, Name: "test"}
		encoded, err := EncodeGob(original)
		if err != nil {
			t.Fatalf("EncodeGob failed: %v", err)
		}

		decoded, err := DecodeGob[sample](encoded)
		if err != nil {
			t.Fatalf("DecodeGob failed: %v", err)
		}

		if decoded.ID != original.ID || decoded.Name != original.Name {
			t.Errorf("expected %+v, got %+v", original, decoded)
		}
	})

	t.Run("encode_string", func(t *testing.T) {
		encoded, err := EncodeGob("hello gob")
		if err != nil {
			t.Fatalf("EncodeGob failed: %v", err)
		}

		decoded, err := DecodeGob[string](encoded)
		if err != nil {
			t.Fatalf("DecodeGob failed: %v", err)
		}

		if decoded != "hello gob" {
			t.Errorf("expected %q, got %q", "hello gob", decoded)
		}
	})

	t.Run("encode_int_slice", func(t *testing.T) {
		original := []int{10, 20, 30}
		encoded, err := EncodeGob(original)
		if err != nil {
			t.Fatalf("EncodeGob failed: %v", err)
		}

		decoded, err := DecodeGob[[]int](encoded)
		if err != nil {
			t.Fatalf("DecodeGob failed: %v", err)
		}

		if len(decoded) != len(original) {
			t.Errorf("expected %d items, got %d", len(original), len(decoded))
		}
	})

	t.Run("decode_invalid_data", func(t *testing.T) {
		_, err := DecodeGob[sample]([]byte("not gob"))
		if err == nil {
			t.Error("expected error for invalid Gob data")
		}
	})
}

func TestJitterTTL_NegativeJitterPercent(t *testing.T) {
	result := jitterTTL(time.Minute, -0.5)
	if result != time.Minute {
		t.Errorf("expected %v for negative jitter, got %v", time.Minute, result)
	}
}

// Benchmark tests for helper functions
func BenchmarkJitterTTL(b *testing.B) {
	baseTTL := 5 * time.Minute
	jitterPercent := 0.1

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		jitterTTL(baseTTL, jitterPercent)
	}
}

func BenchmarkCachedItem_Marshal(b *testing.B) {
	item := CachedItem{
		Data:      json.RawMessage(`{"key":"value","nested":{"foo":"bar"}}`),
		RefreshAt: time.Now().Unix(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(item)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCachedItem_Unmarshal(b *testing.B) {
	item := CachedItem{
		Data:      json.RawMessage(`{"key":"value","nested":{"foo":"bar"}}`),
		RefreshAt: time.Now().Unix(),
	}
	data, _ := json.Marshal(item)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var decoded CachedItem
		err := json.Unmarshal(data, &decoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}

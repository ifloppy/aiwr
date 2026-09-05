package core

import "testing"

func TestConfiguredInputLimits(t *testing.T) {
	t.Setenv("WATERMARKS_MAX_INPUT_BYTES", "123")
	if got := MaxInputBytes(); got != 123 {
		t.Fatalf("MaxInputBytes() = %d, want 123", got)
	}
	t.Setenv("WATERMARKS_MAX_INPUT_BYTES", "not-a-size")
	if got := MaxInputBytes(); got != DefaultMaxInputBytes {
		t.Fatalf("invalid input cap = %d, want default %d", got, DefaultMaxInputBytes)
	}

	t.Setenv("WATERMARKS_MAX_STDIN_BYTES", "321")
	if got := MaxStdinBytes(); got != 321 {
		t.Fatalf("MaxStdinBytes() = %d, want 321", got)
	}
	t.Setenv("WATERMARKS_MAX_STDIN_BYTES", "0")
	if got := MaxStdinBytes(); got != DefaultMaxStdinBytes {
		t.Fatalf("invalid stdin cap = %d, want default %d", got, DefaultMaxStdinBytes)
	}
}

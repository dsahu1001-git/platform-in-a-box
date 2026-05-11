package main

import "testing"

func TestEnvReturnsFallback(t *testing.T) {
	if got := env("SAMPLE_PLATFORM_APP_TEST_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
}

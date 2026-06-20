package personality

import "testing"

func TestLoadReturnsNonEmpty(t *testing.T) {
	got := Load()
	if got == "" {
		t.Fatal("personality.Load() returned empty string")
	}
}

func TestLoadContainsIdentity(t *testing.T) {
	got := Load()
	// The personality file should always start with the identity declaration
	if len(got) < 20 {
		t.Fatalf("personality seems too short (%d bytes)", len(got))
	}
}

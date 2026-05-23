package labels

import "testing"

func TestShortLabel(t *testing.T) {
	if got := ShortLabel(); got != "Save" {
		t.Fatalf("ShortLabel() = %q, want %q", got, "Save")
	}
}

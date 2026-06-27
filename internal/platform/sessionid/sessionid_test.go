package sessionid

import "testing"

func TestValidateAcceptsGeneratedShape(t *testing.T) {
	id := New()
	if err := Validate(id); err != nil {
		t.Fatalf("Validate(New()) = %v for %q", err, id)
	}
}

func TestValidateRejectsPathComponentsAndDots(t *testing.T) {
	for _, id := range []string{"", " ", ".", "..", "a/b", `a\b`, "a..b", "with.dot", "with space", "with@sign"} {
		t.Run(id, func(t *testing.T) {
			if Validate(id) == nil {
				t.Fatalf("Validate(%q) succeeded, want rejection", id)
			}
			if got, ok := Normalize(id); ok || got != "" {
				t.Fatalf("Normalize(%q) = %q %v, want empty false", id, got, ok)
			}
		})
	}
}

func TestNormalizeTrimsValidID(t *testing.T) {
	got, ok := Normalize(" session_123 ")
	if !ok || got != "session_123" {
		t.Fatalf("Normalize = %q %v, want session_123 true", got, ok)
	}
}

package agent

import "testing"

func TestDefaultModesValidate(t *testing.T) {
	for _, mode := range DefaultModes() {
		if err := mode.Validate(); err != nil {
			t.Errorf("mode %q failed validation: %v", mode.Name, err)
		}
	}
}

func TestModeByNameRoundTrip(t *testing.T) {
	for _, name := range ModeNames() {
		mode, ok := ModeByName(name)
		if !ok {
			t.Errorf("ModeByName(%q) = !ok, want ok", name)
			continue
		}
		if mode.Name != name {
			t.Errorf("ModeByName(%q).Name = %q", name, mode.Name)
		}
	}
	if _, ok := ModeByName("not-a-mode"); ok {
		t.Error("ModeByName(unknown) should be !ok")
	}
}

func TestModeOrDefaultFallsBackToBuild(t *testing.T) {
	if got := ModeOrDefault(""); got.Name != "build" {
		t.Errorf("ModeOrDefault empty = %q, want build", got.Name)
	}
	if got := ModeOrDefault("plan"); got.Name != "plan" {
		t.Errorf("ModeOrDefault plan = %q, want plan", got.Name)
	}
	if got := ModeOrDefault("nonsense"); got.Name != "build" {
		t.Errorf("ModeOrDefault nonsense = %q, want build fallback", got.Name)
	}
}

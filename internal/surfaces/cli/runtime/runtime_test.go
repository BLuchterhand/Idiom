package cmdruntime

import "testing"

func TestNewApplicationRequestProjectsPromptMode(t *testing.T) {
	req, err := newApplicationRequest(applicationRequestFlags{
		ConfigPath:    "config.json",
		Prompt:        "explain routing",
		JSONEvents:    true,
		Resume:        "prior-session",
		ModelOverride: "model-alias",
	})
	if err != nil {
		t.Fatalf("newApplicationRequest returned error: %v", err)
	}

	if req.Mode != ApplicationRequestPrompt {
		t.Fatalf("mode = %q, want prompt", req.Mode)
	}
	if req.Prompt != "explain routing" || req.ConfigPath != "config.json" || req.ModelOverride != "model-alias" {
		t.Fatalf("projection lost request fields: %#v", req)
	}
	if !req.Options.JSONEvents || req.Options.Resume != "prior-session" {
		t.Fatalf("projection lost run options: %#v", req.Options)
	}
}

func TestNewApplicationRequestRejectsPromptTurnsConflict(t *testing.T) {
	_, err := newApplicationRequest(applicationRequestFlags{
		Prompt:    "one",
		TurnsFile: "turns.json",
	})
	if err == nil {
		t.Fatal("expected conflict error")
	}
}

func TestNewApplicationRequestProjectsServerModeWithoutPromptOptions(t *testing.T) {
	req, err := newApplicationRequest(applicationRequestFlags{
		ServerOnly: true,
		JSONEvents: true,
		Resume:     "prior-session",
	})
	if err != nil {
		t.Fatalf("newApplicationRequest returned error: %v", err)
	}

	if req.Mode != ApplicationRequestServer {
		t.Fatalf("mode = %q, want server", req.Mode)
	}
	if req.Options.JSONEvents || req.Options.Resume != "" {
		t.Fatalf("server request should not carry prompt-only options: %#v", req.Options)
	}
}

func TestNewApplicationRequestProjectsVersionBeforeConfigBoundModes(t *testing.T) {
	req, err := newApplicationRequest(applicationRequestFlags{
		ShowVersion:      true,
		ListSessionsFlag: true,
		ServerOnly:       true,
	})
	if err != nil {
		t.Fatalf("newApplicationRequest returned error: %v", err)
	}

	if req.Mode != ApplicationRequestVersion {
		t.Fatalf("mode = %q, want version", req.Mode)
	}
}

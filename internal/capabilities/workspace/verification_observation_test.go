package workspace

import (
	"context"
	"testing"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
)

func TestShellToolAttachesCommandFacts(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(config.Workspace{
		Root:        root,
		CWD:         root,
		Shell:       "/bin/sh",
		EnableShell: true,
	})

	result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "printf ok"})
	if result.Error != "" {
		t.Fatalf("shell command failed: %#v", result)
	}

	if result.Facts == nil {
		t.Fatalf("shell result did not attach facts: %#v", result)
	}
	if result.Facts.ShellCommand != "printf ok" {
		t.Fatalf("ShellCommand = %q, want command from request", result.Facts.ShellCommand)
	}
}

func TestAnalyzeVerificationRequestClassifiesVerificationTools(t *testing.T) {
	tests := []struct {
		name      string
		req       ToolRequest
		attempted bool
	}{
		{
			name:      "shell check command",
			req:       ToolRequest{Tool: ToolNameShell, Command: "git diff --check"},
			attempted: true,
		},
		{
			name:      "full suite command",
			req:       ToolRequest{Tool: ToolNameShell, Command: "go test ./..."},
			attempted: true,
		},
		{
			name:      "read only shell is not verification",
			req:       ToolRequest{Tool: ToolNameShell, Command: "git status --short"},
			attempted: false,
		},
		{
			name:      "source sanity",
			req:       ToolRequest{Tool: ToolNameSourceSanity, Path: "internal/app.go"},
			attempted: true,
		},
		{
			name:      "diagnostic tool",
			req:       ToolRequest{Tool: "go_diagnostics", Path: "internal/app.go"},
			attempted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AnalyzeVerificationRequest(tt.req)
			if got.Attempted != tt.attempted {
				t.Fatalf("Attempted = %v, want %v", got.Attempted, tt.attempted)
			}
		})
	}
}

func TestAnalyzeVerificationResultClassifiesObservedEvidence(t *testing.T) {
	tests := []struct {
		name         string
		result       ToolResult
		attempted    bool
		successful   bool
		command      string
		patchHygiene bool
	}{
		{
			name:         "successful shell verification",
			result:       ToolResult{Tool: ToolNameShell, Output: "$ git diff --check\n", Facts: &toolfacts.ToolResultFacts{ShellCommand: "git diff --check"}},
			attempted:    true,
			successful:   true,
			command:      "git diff --check",
			patchHygiene: true,
		},
		{
			name:       "failed shell verification attempt",
			result:     ToolResult{Tool: ToolNameShell, Output: "$ go test ./...\nFAIL", Error: "exit status 1", Facts: &toolfacts.ToolResultFacts{ShellCommand: "go test ./..."}},
			attempted:  true,
			successful: false,
			command:    "go test ./...",
		},
		{
			name:       "successful non verification shell",
			result:     ToolResult{Tool: ToolNameShell, Output: "$ git status --short\n M file.go", Facts: &toolfacts.ToolResultFacts{ShellCommand: "git status --short"}},
			attempted:  false,
			successful: false,
		},
		{
			name:       "rendered shell prefix is not authority without facts",
			result:     ToolResult{Tool: ToolNameShell, Output: "$ go test ./...\nok"},
			attempted:  false,
			successful: false,
		},
		{
			name:       "facts override stale rendered shell prefix",
			result:     ToolResult{Tool: ToolNameShell, Output: "$ git status --short\n", Facts: &toolfacts.ToolResultFacts{ShellCommand: "go test ./..."}},
			attempted:  true,
			successful: true,
			command:    "go test ./...",
		},
		{
			name:       "compiler backed source sanity",
			result:     ToolResult{Tool: ToolNameSourceSanity, Summary: "Source sanity passed", Output: "internal/app.go: no warnings via go_diagnostics"},
			attempted:  true,
			successful: true,
		},
		{
			name:       "generic source sanity fallback is insufficient",
			result:     ToolResult{Tool: ToolNameSourceSanity, Summary: "Source sanity passed for 1 file(s) (1 generic fallback)", Output: "internal/app.go: generic delimiter sanity passed via generic_source_sanity"},
			attempted:  true,
			successful: false,
		},
		{
			name:       "diagnostic tool",
			result:     ToolResult{Tool: "go_diagnostics", Summary: "Collected diagnostics"},
			attempted:  true,
			successful: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AnalyzeVerificationResult(tt.result)
			if got.Attempted != tt.attempted {
				t.Fatalf("Attempted = %v, want %v", got.Attempted, tt.attempted)
			}
			if got.Successful != tt.successful {
				t.Fatalf("Successful = %v, want %v", got.Successful, tt.successful)
			}
			if got.Command != tt.command {
				t.Fatalf("Command = %q, want %q", got.Command, tt.command)
			}
			if got.PatchHygieneOnly != tt.patchHygiene {
				t.Fatalf("PatchHygieneOnly = %v, want %v", got.PatchHygieneOnly, tt.patchHygiene)
			}
		})
	}
}

func TestAnalyzePostWriteVerificationResultClassifiesHTMLSmokeCheck(t *testing.T) {
	result := ToolResult{
		Tool:   ToolNameShell,
		Output: "$ python3 -c \"import html.parser; html.parser.HTMLParser().feed(open('index.html').read()); print('HTML parsed OK: index.html')\"\nHTML parsed OK: index.html",
		Facts:  &toolfacts.ToolResultFacts{ShellCommand: "python3 -c \"import html.parser; html.parser.HTMLParser().feed(open('index.html').read()); print('HTML parsed OK: index.html')\""},
	}

	got := AnalyzePostWriteVerificationResult(result, []string{"index.html"})
	if !got.Successful || !got.HTMLSmokeForPendingHTMLWrites {
		t.Fatalf("HTML smoke check for pending HTML write should count, got %#v", got)
	}

	got = AnalyzePostWriteVerificationResult(result, []string{"main.go"})
	if got.Successful || got.HTMLSmokeForPendingHTMLWrites {
		t.Fatalf("HTML smoke check must not satisfy non-HTML writes, got %#v", got)
	}
}

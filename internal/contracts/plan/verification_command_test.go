package plancontract

import "testing"

func TestVerificationCommandArgsAllowsCanonicalTestCommands(t *testing.T) {
	tests := []string{
		"go test ./...",
		"go vet ./internal/...",
		"pytest tests/test_brain.py",
		"python -m pytest tests/test_brain.py",
	}

	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			args, ok := VerificationCommandArgs(command)
			if !ok {
				t.Fatalf("VerificationCommandArgs(%q) rejected command", command)
			}
			if got := CanonicalVerificationCommand(command, args); got != command {
				t.Fatalf("canonical command = %q, want %q", got, command)
			}
		})
	}
}

func TestVerificationCommandArgsRejectsShellControlOperators(t *testing.T) {
	for _, command := range []string{
		"go test ./... && rm -rf tmp",
		"pytest tests/test.py; cat secret",
		"python -m pytest tests/test.py > out.txt",
	} {
		t.Run(command, func(t *testing.T) {
			if _, ok := VerificationCommandArgs(command); ok {
				t.Fatalf("VerificationCommandArgs(%q) accepted unsafe command", command)
			}
		})
	}
}

func TestVerificationCommandEvidenceKeyCanonicalizesArgvAndWorkingDir(t *testing.T) {
	fromText := VerificationCommandEvidenceKey("python -m pytest tests/test_brain.py", nil, ".")
	fromArgv := VerificationCommandEvidenceKey("", []string{"python", "-m", "pytest", "tests/test_brain.py"}, "")
	if fromText == "" || fromText != fromArgv {
		t.Fatalf("canonical keys differ: text=%q argv=%q", fromText, fromArgv)
	}

	subdir := VerificationCommandEvidenceKey("python -m pytest tests/test_brain.py", nil, "src")
	if subdir == "" || subdir == fromText {
		t.Fatalf("working directory should be part of evidence identity: root=%q subdir=%q", fromText, subdir)
	}
}

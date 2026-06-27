package workspace

import (
	"testing"

	planartifact "idiom/internal/contracts/planartifact"
)

func TestSanitizePlanArtifactPathValueUsesPlanArtifactPathContract(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "relative", path: "pkg/file.go", want: "pkg/file.go"},
		{name: "dot relative", path: "./pkg/file.go", want: "pkg/file.go"},
		{name: "absolute", path: "/tmp/file.go", want: ""},
		{name: "parent", path: "../file.go", want: ""},
		{name: "nested parent", path: "pkg/../../file.go", want: ""},
		{name: "empty", path: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizePlanArtifactPathValue(tt.path)
			if got != tt.want {
				t.Fatalf("sanitizePlanArtifactPathValue(%q) = %q, want %q", tt.path, got, tt.want)
			}

			if tt.want != "" && !planartifact.PathIsSafe(tt.path) {
				t.Fatalf("test expectation drift: accepted path %q must be safe under planartifact.PathIsSafe", tt.path)
			}
			if tt.want == "" && tt.path != "" && planartifact.PathIsSafe(tt.path) {
				t.Fatalf("test expectation drift: rejected path %q is now safe under planartifact.PathIsSafe", tt.path)
			}
		})
	}
}

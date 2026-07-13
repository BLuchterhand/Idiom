package workspace

import (
	"fmt"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	planartifact "idiom/internal/contracts/planartifact"
)

var _ = ginkgo.Describe("sanitize plan artifact path value uses plan artifact path contract", func() {

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
		ginkgo.It(tt.name, func() {
			got := sanitizePlanArtifactPathValue(tt.path)
			gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("sanitizePlanArtifactPathValue(%q) = %q, want %q", tt.path, got, tt.want))
			if tt.want != "" {
				gomega.Expect(planartifact.PathIsSafe(tt.path)).To(gomega.BeTrue(), fmt.Sprintf("test expectation drift: accepted path %q must be safe under planartifact.PathIsSafe", tt.path))
			}
			if tt.want == "" && tt.path != "" {
				gomega.Expect(planartifact.PathIsSafe(tt.path)).To(gomega.BeFalse(), fmt.Sprintf("test expectation drift: rejected path %q is now safe under planartifact.PathIsSafe", tt.path))
			}

		})
	}
})

package workspace

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"runtime"
)

var _ = ginkgo.Describe("search backend", func() {
	ginkgo.Describe("find gnu style grep probes version output", func() {
		tests := []struct {
			name string
			body string
			want bool
		}{
			{
				name: "accepts GNU grep marker on stdout",
				body: "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11'\n",
				want: true,
			},
			{
				name: "accepts GNU grep marker on stderr",
				body: "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11' >&2\n",
				want: true,
			},
			{
				name: "rejects non-GNU grep output",
				body: "#!/bin/sh\nprintf '%s\\n' 'grep (BSD grep) 2.6'\n",
				want: false,
			},
			{
				name: "rejects failing probe even with GNU output",
				body: "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11'\nexit 2\n",
				want: false,
			},
		}

		for _, tt := range tests {
			ginkgo.It(tt.name, func() {
				if runtime.GOOS == "windows" {
					ginkgo.Skip("executable shell fixture uses POSIX mode bits")
				}
				path := writeExecutableSearchBackendProbe(tt.body)

				gotPath, got := findGNUStyleGrep(path)
				gomega.Expect(got).To(gomega.Equal(tt.want), fmt.Sprintf("findGNUStyleGrep ok = %v, want %v", got, tt.want))
				if got {
					gomega.Expect(gotPath).To(gomega.Equal(path), fmt.Sprintf("findGNUStyleGrep path = %q, want %q", gotPath, path))
				}

			})
		}
	})
	ginkgo.Describe("resolve content search backend uses host tool lookup", func() {
		tests := []struct {
			name        string
			executables map[string]string
			wantBin     string
			wantKind    string
		}{
			{
				name: "prefers ripgrep",
				executables: map[string]string{
					"rg":   "#!/bin/sh\nexit 0\n",
					"grep": "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11'\n",
				},
				wantBin:  "rg",
				wantKind: contentSearchRipgrep,
			},
			{
				name: "accepts GNU grep",
				executables: map[string]string{
					"grep": "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11'\n",
				},
				wantBin:  "grep",
				wantKind: contentSearchGrep,
			},
			{
				name: "falls back to ggrep when plain grep is not GNU",
				executables: map[string]string{
					"grep":  "#!/bin/sh\nprintf '%s\\n' 'grep (BSD grep) 2.6'\n",
					"ggrep": "#!/bin/sh\nprintf '%s\\n' 'grep (GNU grep) 3.11'\n",
				},
				wantBin:  "ggrep",
				wantKind: contentSearchGrep,
			},
		}

		for _, tt := range tests {
			ginkgo.It(tt.name, func() {
				if runtime.GOOS == "windows" {
					ginkgo.Skip("executable shell fixture uses POSIX mode bits")
				}
				binDir := ginkgo.GinkgoT().TempDir()
				for name, body := range tt.executables {
					path := filepath.Join(binDir, name)
					gomega.Expect(os.WriteFile(path, []byte(body), 0o755)).To(gomega.Succeed())

				}
				ginkgo.GinkgoT().Setenv("PATH", binDir)

				got, err := resolveContentSearchBackend()
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("resolveContentSearchBackend: %v", err))
				gomega.Expect(got.kind).To(gomega.Equal(tt.wantKind), fmt.Sprintf("backend = %#v, want bin %q kind %q", got, tt.wantBin, tt.wantKind))
				gomega.Expect(filepath.Base(got.bin)).To(gomega.Equal(tt.wantBin), fmt.Sprintf("backend = %#v, want bin %q kind %q", got, tt.wantBin, tt.wantKind))

			})
		}
	})
})

func writeExecutableSearchBackendProbe(body string) string {
	ginkgo.GinkgoHelper()

	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "grep")
	gomega.Expect(os.WriteFile(path, []byte(body), 0o755)).To(gomega.Succeed())

	return path
}

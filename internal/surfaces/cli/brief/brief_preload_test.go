package brief

import (
	"bytes"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = ginkgo.Describe("brief preload", func() {
	ginkgo.It("brief preload emits canonical generic kind", func() {
		var stdout, stderr bytes.Buffer

		code := runBriefPreload([]string{"--kind", "generic"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("preload generic must exit 0; got %d (stderr=%s)", code, stderr.String()))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("Atom-Spec Drafting Preload"), fmt.Sprintf("expected canonical preload header in stdout: %s", stdout.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("source=canonical"), fmt.Sprintf("expected canonical source tag in stderr: %s", stderr.String()))

	})
	ginkgo.It("brief preload project overlay wins", func() {
		root := ginkgo.GinkgoT().TempDir()
		dir := filepath.Join(root, ".idiom", "prompts", "brief", "preload")
		gomega.Expect(os.MkdirAll(dir, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "generic.md"), []byte("PROJECT-OWNED PRELOAD\n"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := runBriefPreload([]string{"--kind", "generic", "--project-root", root}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("overlay path must exit 0; got %d", code))
		gomega.Expect(stdout.String()).To(gomega.ContainSubstring("PROJECT-OWNED PRELOAD"), fmt.Sprintf("project overlay should win over canonical; got: %s", stdout.String()))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("source=overlay:"), fmt.Sprintf("source tag should mark the file as overlay: %s", stderr.String()))

	})
	ginkgo.It("brief preload unknown kind exits one", func() {
		var stdout, stderr bytes.Buffer

		code := runBriefPreload([]string{"--kind", "no-such-kind"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("unknown kind must exit 1; got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("use --list"), fmt.Sprintf("error should hint at --list: %s", stderr.String()))

	})
	ginkgo.It("brief preload list shows canonical and overlay kinds", func() {
		root := ginkgo.GinkgoT().TempDir()
		dir := filepath.Join(root, ".idiom", "prompts", "brief", "preload")
		gomega.Expect(os.MkdirAll(dir, 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(filepath.Join(dir, "scenario-output.md"), []byte("x"), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		code := runBriefPreload([]string{"--list", "--project-root", root}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("--list must exit 0; got %d", code))

		out := stdout.String()
		gomega.Expect(out).To(gomega.ContainSubstring("generic\tcanonical"), fmt.Sprintf("missing canonical generic row: %s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("scenario-output\toverlay"), fmt.Sprintf("missing overlay row: %s", out))

	})
	ginkgo.It("brief preload rejects path traversal kind", func() {
		cases := []string{"../../etc/passwd", "/etc/passwd", "..", "a/b", "a\x00b"}
		for _, kind := range cases {
			var stdout, stderr bytes.Buffer

			code := runBriefPreload([]string{"--kind", kind}, &stdout, &stderr)
			gomega.Expect(code).To(gomega.Equal(1), fmt.Sprintf("kind %q must be rejected with exit 1; got %d (stderr=%s)", kind, code, stderr.String()))
			gomega.Expect(stderr.String()).To(gomega.ContainSubstring("invalid kind"), fmt.Sprintf("kind %q rejection should explain the cause: %s", kind, stderr.String()))

		}
	})
	ginkgo.It("brief preload help exits zero", func() {
		var stdout, stderr bytes.Buffer

		code := runBriefPreload([]string{"--help"}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(0), fmt.Sprintf("--help must exit 0; got %d", code))

	})
	ginkgo.It("brief preload requires kind or list", func() {
		var stdout, stderr bytes.Buffer

		code := runBriefPreload([]string{}, &stdout, &stderr)
		gomega.Expect(code).To(gomega.Equal(2), fmt.Sprintf("no flags must exit 2; got %d", code))
		gomega.Expect(stderr.String()).To(gomega.ContainSubstring("--kind is required"), fmt.Sprintf("error should name the missing flag: %s", stderr.String()))

	})
})

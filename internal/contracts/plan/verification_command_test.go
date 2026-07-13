package plan

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Verification commands", func() {
	DescribeTable("accepts and preserves canonical test commands",
		func(command string) {
			args, ok := VerificationCommandArgs(command)
			Expect(ok).To(BeTrue())
			Expect(CanonicalVerificationCommand(command, args)).To(Equal(command))
		},
		Entry("Go test", "go test ./..."),
		Entry("Go vet", "go vet ./internal/..."),
		Entry("pytest", "pytest tests/test_brain.py"),
		Entry("Python module pytest", "python -m pytest tests/test_brain.py"),
	)

	It("returns the matched allowed prefix", func() {
		args, ok := VerificationCommandArgs("python -m pytest tests/unit")
		Expect(ok).To(BeTrue())
		Expect(VerificationCommandPrefix(args)).To(Equal([]string{"python", "-m", "pytest"}))
	})

	DescribeTable("rejects shell control operators",
		func(command string) {
			_, ok := VerificationCommandArgs(command)
			Expect(ok).To(BeFalse())
		},
		Entry("and operator", "go test ./... && rm -rf tmp"),
		Entry("semicolon", "pytest tests/test.py; cat secret"),
		Entry("redirection", "python -m pytest tests/test.py > out.txt"),
	)

	It("canonicalizes argv and working-directory identity", func() {
		fromText := VerificationCommandEvidenceKey("python -m pytest tests/test_brain.py", nil, ".")
		fromArgv := VerificationCommandEvidenceKey("", []string{"python", "-m", "pytest", "tests/test_brain.py"}, "")
		Expect(fromText).NotTo(BeEmpty())
		Expect(fromArgv).To(Equal(fromText))

		subdir := VerificationCommandEvidenceKey("python -m pytest tests/test_brain.py", nil, "src")
		Expect(subdir).NotTo(BeEmpty())
		Expect(subdir).NotTo(Equal(fromText))
	})
})

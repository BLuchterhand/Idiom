package localops

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Local evidence sanitization", func() {
	It("allows privacy field names while rejecting true capture flags", func() {
		allowed := `{"privacy_redaction_status":{"secret_captured":false,"private_path_captured":false},"redacted_fields":["secret_value"]}`
		Expect(ForbiddenEvidenceLeak(allowed)).To(BeEmpty())

		blocked := `{"privacy_redaction_status":{"secret_captured":true}}`
		Expect(ForbiddenEvidenceLeak(blocked)).To(Equal("secret_captured"))
	})

	It("distinguishes absolute filesystem paths from URL paths", func() {
		Expect(ContainsAbsolutePath("workspace=/workspace/idiom")).To(BeTrue())
		Expect(ContainsAbsolutePath("workspace=//server/private-share")).To(BeTrue())
		Expect(ContainsAbsolutePath("workspace=file:///Users/alice/private-repo")).To(BeTrue())
		Expect(ContainsAbsolutePath("workspace=FILE://server/private-share")).To(BeTrue())
		Expect(ContainsAbsolutePath("workspace=file:/Users/alice/private-repo")).To(BeTrue())
		Expect(ContainsAbsolutePath("workspace=file:///")).To(BeTrue())
		Expect(ContainsAbsolutePath("accepts file://, symbol://")).To(BeFalse())
		Expect(ContainsAbsolutePath("accepts file://,symbol://")).To(BeFalse())
		Expect(ContainsAbsolutePath("schemes=(file://); symbol://")).To(BeFalse())
		Expect(ContainsAbsolutePath("docs=https://example.com/idiom/path")).To(BeFalse())
		Expect(ContainsAbsolutePath(`pattern=^uri:[A-Za-z][A-Za-z0-9+.-]*://.+$`)).To(BeFalse())
		Expect(ContainsAbsolutePath(`pattern=^uri:[A-Za-z][A-Za-z0-9+.-]*:/private/fixture`)).To(BeTrue())
		Expect(TokenNeedsRedaction(`/workspace/idiom`)).To(BeTrue())
		Expect(TokenNeedsRedaction(`C:\Users\alice\private-repo`)).To(BeTrue())
	})

	It("rejects local file URIs while allowing remote URL evidence", func() {
		Expect(ForbiddenEvidenceLeak(`{"description":"accepts file://, symbol://, and https://"}`)).To(BeEmpty())
		Expect(ForbiddenEvidenceLeak(`{"description":"file://"}`)).To(BeEmpty())
		Expect(ForbiddenEvidenceLeak(`{"source":"file:///Users/alice/private/repo"}`)).To(Equal("absolute_path"))
		Expect(ForbiddenEvidenceLeak(`{"source":"file://server/private-share"}`)).To(Equal("absolute_path"))
		Expect(ForbiddenEvidenceLeak(`{"source":"file://localhost"}`)).To(Equal("absolute_path"))
		Expect(ForbiddenEvidenceLeak(`{"source":"https://example.com/public/reference"}`)).To(BeEmpty())
	})

	It("does not treat a JSON schema URI pattern as emitted path evidence", func() {
		body := `{"input_schema":{"properties":{"id":{"pattern":"^uri:[A-Za-z][A-Za-z0-9+.-]*://.+$"}}}}`
		Expect(ForbiddenEvidenceLeak(body)).To(BeEmpty())
	})

	It("normalizes and bounds safe ASCII identifiers", func() {
		Expect(SafeToken(" No-Go Client Class ")).To(Equal("no_go_client_class"))
		Expect(SafeToken(strings.Repeat("a", 90))).To(HaveLen(80))
	})

	It("creates stable short content digests that change with input", func() {
		hash := ContentHash([]byte("same"))
		Expect(hash).To(Equal(ContentHash([]byte("same"))))
		Expect(hash).To(HaveLen(16))
		Expect(ContentHash([]byte("different"))).NotTo(Equal(hash))
	})

	It("trims, drops, and deduplicates compact strings", func() {
		Expect(CompactStrings(" alpha ", "", "beta", "alpha", " beta ")).To(Equal([]string{"alpha", "beta"}))
	})

	It("redacts sensitive relative paths from commands", func() {
		for _, command := range []string{
			"go test ./customer-private-repo",
			"go test ../secrets/token.txt",
		} {
			safe := SafeCommand(command)
			Expect(safe).NotTo(ContainSubstring("customer"))
			Expect(safe).NotTo(ContainSubstring("secret"))
			Expect(safe).NotTo(ContainSubstring("token"))
		}
		Expect(SafeCommand("go test ./...")).To(Equal("go test ./..."))
	})

	DescribeTable("rejects relative telemetry and conversation log paths",
		func(body string) {
			Expect(ForbiddenEvidenceLeak(body)).To(Equal("log_path"))
		},
		Entry("telemetry log", `{"path":"logs/telemetry/session.jsonl"}`),
		Entry("conversation log", `{"path":"logs/conversations/session.jsonl"}`),
		Entry("hidden local conversation log", `{"path":".idiom/logs/conversations/session.jsonl"}`),
	)

	It("redacts forbidden map values and keys recursively", func() {
		report := struct {
			Metadata map[string]any `json:"metadata"`
		}{
			Metadata: map[string]any{
				"debug_path":       "/home/example/private-repo",
				"private_customer": "safe",
				"nested":           map[string]any{"token_value": "token=abc123"},
				"safe_description": "metadata only",
			},
		}
		RedactForbiddenStrings(&report)

		body, err := json.Marshal(report)
		Expect(err).NotTo(HaveOccurred())
		for _, forbidden := range []string{"/home/example/private-repo", "private_customer", "token=abc123"} {
			Expect(string(body)).NotTo(ContainSubstring(forbidden))
		}
		Expect(ForbiddenEvidenceLeak(string(body))).To(BeEmpty())
	})

	It("classifies a sensitive task without capturing its raw text", func() {
		evidence := TaskEvidence("CustomerX-private-repo secret=abc123 /workspace/project")
		Expect(evidence.RawTaskCaptured).To(BeFalse())
		Expect(evidence.RedactionStatus).To(Equal("redacted"))
		Expect(evidence.TaskID).NotTo(BeEmpty())
		Expect(evidence.ContentClasses).To(ContainElements("absolute_path", "secret_marker", "sensitive_identifier"))
	})
})

package toolpolicy

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Tool policy", func() {
	ginkgo.DescribeTable("matches command patterns in declaration order",
		func(command string, want Decision) {
			rules := map[string][]Rule{"shell": {
				{Pattern: "rm *", Decision: Ask},
				{Pattern: "git *", Decision: Allow},
				{Pattern: "*", Decision: Allow},
			}}
			gomega.Expect(Match(commandInput("shell", command), rules)).To(gomega.Equal(want))
		},
		ginkgo.Entry("destructive command asks", "rm -rf /tmp/x", Ask),
		ginkgo.Entry("git command allows", "git status", Allow),
		ginkgo.Entry("fallback allows", "ls -la", Allow),
	)

	ginkgo.DescribeTable("matches recursive path globs",
		func(path string, want Decision) {
			rules := map[string][]Rule{"edit_file": {
				{Pattern: "**/secrets/**", Decision: Deny},
				{Pattern: "**/*.md", Decision: Allow},
				{Pattern: "*", Decision: Ask},
			}}
			gomega.Expect(Match(pathInput("edit_file", path), rules)).To(gomega.Equal(want))
		},
		ginkgo.Entry("root Markdown", "README.md", Allow),
		ginkgo.Entry("nested Markdown", "docs/plan.md", Allow),
		ginkgo.Entry("nested secret JSON", "app/secrets/keys.json", Deny),
		ginkgo.Entry("nested secret text", "internal/secrets/token.txt", Deny),
		ginkgo.Entry("ordinary source", "internal/surfaces/tui/app/app.go", Ask),
	)

	ginkgo.It("returns none when rules for the tool are absent", func() {
		gomega.Expect(Match(pathInput("edit_file", "app.go"), nil)).To(gomega.Equal(None))
		gomega.Expect(Match(pathInput("edit_file", "app.go"), map[string][]Rule{
			"shell": {{Pattern: "*", Decision: Allow}},
		})).To(gomega.Equal(None))
	})

	ginkgo.It("uses first-match semantics for mapped rules", func() {
		rules := map[string][]Rule{"edit_file": {
			{Pattern: "internal/surfaces/tui/app/*.go", Decision: Allow},
			{Pattern: "**/app.go", Decision: Deny},
		}}
		gomega.Expect(Match(pathInput("edit_file", "internal/surfaces/tui/app/app.go"), rules)).To(gomega.Equal(Allow))
	})

	ginkgo.It("uses caller-supplied fallback rule keys only when an exact key is absent", func() {
		rules := map[string][]Rule{"external:*": {{Pattern: "*", Decision: Ask}}}
		input := pathInput("external:search", "query")
		input.FallbackRuleKeys = []string{"external:*"}
		gomega.Expect(Match(input, rules)).To(gomega.Equal(Ask))

		rules[input.Tool] = []Rule{{Pattern: "*", Decision: Allow}}
		gomega.Expect(Match(input, rules)).To(gomega.Equal(Allow))
	})

	ginkgo.DescribeTable("lets a later flat-policy deny override an earlier allow",
		func(command string) {
			policy := Policy{Default: Deny, Rules: []Rule{
				{Tool: "shell", CommandPrefix: "git diff", Decision: Allow},
				{Tool: "shell", CommandContains: "--output", Decision: Deny},
			}}
			result := policy.Decide(Input{Tool: "shell", Command: command})
			gomega.Expect(result.Decision).To(gomega.Equal(Deny))
			gomega.Expect(result.Matched).To(gomega.BeTrue())
			gomega.Expect(result.Rule.CommandContains).To(gomega.Equal("--output"))
		},
		ginkgo.Entry("equals form", "git diff --output=/tmp/patch"),
		ginkgo.Entry("separate argument", "git diff --output /tmp/patch"),
	)

	ginkgo.It("matches command prefixes only at token boundaries", func() {
		policy := Policy{Default: Deny, Rules: []Rule{{Tool: "shell", CommandPrefix: "go test", Decision: Allow}}}
		gomega.Expect(policy.Decide(Input{Tool: "shell", Command: "go test ./..."}).Decision).To(gomega.Equal(Allow))
		gomega.Expect(policy.Decide(Input{Tool: "shell", Command: "go testdata"}).Decision).To(gomega.Equal(Deny))
	})

	ginkgo.It("matches path rules against the caller's precomputed target", func() {
		policy := Policy{Default: Allow, Rules: []Rule{{Tool: "edit_file", PathPattern: "README.md", Decision: Deny}}}
		result := policy.Decide(Input{Tool: "edit_file", PathTarget: "README.md"})
		gomega.Expect(result.Decision).To(gomega.Equal(Deny))
		gomega.Expect(result.Target).To(gomega.Equal("README.md"))
	})
})

func commandInput(tool, command string) Input {
	return Input{Tool: tool, Command: command, PatternTarget: command, PatternKind: PatternCommand}
}

func pathInput(tool, target string) Input {
	return Input{Tool: tool, PathTarget: target, PatternTarget: target, PatternKind: PatternPath}
}

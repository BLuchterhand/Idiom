package eval

import (
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("localization usage cases", func() {
	cfg := LocalizationUsageConfig{
		QueryRegex:      `(?s)idiom\.sh\s+(?:context_impact|context_query)\b(.*)`,
		PathRootRegex:   `(?i)^.*?/proj(?:/\.claude/worktrees/[^/]+)?/(.+)$`,
		LabelTools:      []string{"Read", "Edit", "Write"},
		WindowMinutes:   30,
		MainRef:         "main",
		DedupeJaccard:   0.8,
		HoldoutFraction: 0.5,
		DropQueryRegex:  `(?i)[\w.+-]+@[\w-]+\.[\w.]+|/users/|<[a-z ]+>`,
	}
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return t0.Add(time.Duration(minutes) * time.Minute) }

	ginkgo.It("reads the query from both argument forms", func() {
		gomega.Expect(usageQueryText(` task="wind sway canopy" 2>&1 | head`)).To(gomega.Equal("wind sway canopy"))
		gomega.Expect(usageQueryText(` '{"task":"paged store eviction","limit":10}'`)).To(gomega.Equal("paged store eviction"))
		gomega.Expect(usageQueryText(` query='levee height' path=core`)).To(gomega.Equal("levee height"))
	})

	ginkgo.It("labels files opened in the window, minus known, named and late files", func() {
		rules, err := compileUsageRules(cfg)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		events := []usageEvent{
			{at: at(0), text: "Brief: fix the bug in core/given.h"},
			{at: at(1), tool: "Read", path: `C:\x\proj\core\seen_before.cpp`},
			{at: at(2), tool: "Bash", command: `cd /c/x/proj/.claude/worktrees/w1 && bash scripts/idiom.sh context_impact task="canopy wind sway gust"`},
			{at: at(3), tool: "Read", path: "C:/x/proj/.claude/worktrees/w1/core/wind.cpp"},
			{at: at(4), tool: "Read", path: "C:/x/proj/core/seen_before.cpp"},
			{at: at(5), tool: "Edit", path: "C:/x/proj/core/given.h"},
			{at: at(6), tool: "Read", path: "C:/elsewhere/notes.txt"},
			{at: at(7), tool: "Edit", path: "C:/x/proj/core/wind.cpp"},
			{at: at(8), tool: "Write", path: "C:/x/proj/core/sway.h"},
			{at: at(40), tool: "Read", path: "C:/x/proj/core/late.cpp"},
			{at: at(41), tool: "Bash", command: `bash scripts/idiom.sh context_impact task="mail me at a@b.com"`},
		}

		qs := rules.sessionQueries("s1", events)
		gomega.Expect(qs).To(gomega.HaveLen(1))
		gomega.Expect(qs[0].query).To(gomega.Equal("canopy wind sway gust"))
		gomega.Expect(qs[0].opened).To(gomega.Equal([]string{"core/wind.cpp", "core/given.h", "core/sway.h"}))

		briefCfg := cfg
		briefCfg.DropBriefNamed = true
		briefRules, err := compileUsageRules(briefCfg)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(briefRules.sessionQueries("s1", events)[0].opened).To(gomega.Equal([]string{"core/wind.cpp", "core/sway.h"}))
	})

	ginkgo.It("ends a window at the next query in the session", func() {
		rules, err := compileUsageRules(cfg)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		qs := rules.sessionQueries("s1", []usageEvent{
			{at: at(0), tool: "Bash", command: `bash scripts/idiom.sh context_impact task="first query text here"`},
			{at: at(1), tool: "Read", path: "/c/x/proj/a.cpp"},
			{at: at(2), tool: "Bash", command: `bash scripts/idiom.sh context_query query="second query text here"`},
			{at: at(3), tool: "Read", path: "/c/x/proj/b.cpp"},
		})
		gomega.Expect(qs).To(gomega.HaveLen(2))
		gomega.Expect(qs[0].opened).To(gomega.Equal([]string{"a.cpp"}))
		gomega.Expect(qs[1].opened).To(gomega.Equal([]string{"b.cpp"}))
	})

	ginkgo.It("merges near-identical queries into the earliest, with the union of labels", func() {
		qs := dedupeUsageQueries([]usageQuery{
			{at: at(5), query: "canopy wind sway gust tree", opened: []string{"b.cpp"}},
			{at: at(1), query: "Canopy wind sway gust tree", opened: []string{"a.cpp"}},
			{at: at(9), query: "levee height channel depth", opened: []string{"c.cpp"}},
		}, 0.8)
		gomega.Expect(qs).To(gomega.HaveLen(2))
		gomega.Expect(qs[0].at).To(gomega.Equal(at(1)))
		gomega.Expect(qs[0].opened).To(gomega.Equal([]string{"a.cpp", "b.cpp"}))
	})

	ginkgo.It("names the checkout a query ran in and its branch", func() {
		wcfg := cfg
		wcfg.WorktreeRegex = `(?i)/proj/\.claude/worktrees/([^/\s"'&;]+)`
		rules, err := compileUsageRules(wcfg)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		branches, err := UsageWorktreeBranches("worktree C:/x/proj\nbranch refs/heads/main\n\nworktree C:/x/proj/.claude/worktrees/w1\nHEAD abc\nbranch refs/heads/pc/w1\n", wcfg)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(branches).To(gomega.Equal(map[string]string{"w1": "pc/w1"}))

		mainCwd := `C:\x\proj`
		cdInto := rules.whereOf(usageEvent{cwd: mainCwd, branch: "main", command: "cd /c/x/proj/.claude/worktrees/w1 && bash scripts/idiom.sh context_impact task=x"})
		gomega.Expect(usageBranch(cdInto, branches)).To(gomega.Equal("pc/w1"))

		inMain := rules.whereOf(usageEvent{cwd: mainCwd, branch: "main", command: "bash scripts/idiom.sh context_impact task=x"})
		gomega.Expect(usageBranch(inMain, branches)).To(gomega.Equal("main"))

		insideGone := rules.whereOf(usageEvent{cwd: "C:/x/proj/.claude/worktrees/gone", branch: "pc/gone", command: "bash scripts/idiom.sh context_impact task=x"})
		gomega.Expect(usageBranch(insideGone, branches)).To(gomega.Equal("pc/gone"))

		cdGone := rules.whereOf(usageEvent{cwd: mainCwd, branch: "main", command: "cd /c/x/proj/.claude/worktrees/gone && bash scripts/idiom.sh context_impact task=x"})
		gomega.Expect(usageBranch(cdGone, branches)).To(gomega.BeEmpty())
	})

	ginkgo.It("splits by time and the validator checks the same cut", func() {
		cases := []LocalizationEvalCase{
			{ID: "c", Merge: "c", Time: "2026-11-22T00:00:00Z"},
			{ID: "a", Merge: "a", Time: "2026-11-20T00:00:00Z"},
			{ID: "d", Merge: "d", Time: "2026-11-23T00:00:00Z"},
			{ID: "b", Merge: "b", Time: "2026-11-21T00:00:00Z"},
		}
		cut := usageHoldoutCut(cases, 0.5)
		splitUsageCasesAt(cases, cut)
		gomega.Expect(cut.Format(time.RFC3339)).To(gomega.Equal("2026-11-22T00:00:00Z"))

		splits := map[string]string{}
		for _, c := range cases {
			splits[c.ID] = c.Split
			gomega.Expect(localizationEvalWantSplit(cut.Format(time.RFC3339), c)).To(gomega.Equal(c.Split))
		}

		gomega.Expect(splits).To(gomega.Equal(map[string]string{"a": "dev", "b": "dev", "c": "holdout", "d": "holdout"}))
	})
})

package comprehension

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pack role and reason labels", func() {
	build := func() string {
		root := GinkgoT().TempDir()
		writeFile(filepath.Join(root, "go.mod"), "module example.com/explain\n\ngo 1.22\n")
		writeFile(filepath.Join(root, "pkg", "runner.go"), "package pkg\n\nfunc Run() string { return helper() }\n")
		writeFile(filepath.Join(root, "pkg", "helper.go"), "package pkg\n\nfunc helper() string { return \"ok\" }\n")
		writeFile(filepath.Join(root, "pkg", "runner_test.go"), "package pkg\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) { _ = Run() }\n")
		writeFile(filepath.Join(root, "cmd", "tool", "main.go"), "package main\n\nfunc main() {}\n")
		_, err := Build(context.Background(), BuildOptions{Root: root, Language: "go"})
		Expect(err).NotTo(HaveOccurred())

		return root
	}

	It("adds kind, subtopic and a bounded reason only when asked", func() {
		root := build()
		task := "change Run in the runner"
		plain, err := Pack(context.Background(), PackOptions{Root: root, Task: task, Mode: "plan", Now: testTime()})
		Expect(err).NotTo(HaveOccurred())

		data, err := json.Marshal(plain.CandidateFiles)
		Expect(err).NotTo(HaveOccurred())

		for _, field := range []string{`"kind"`, `"dir2"`, `"why"`, `"admitted_by"`} {
			Expect(string(data)).NotTo(ContainSubstring(field), "default packs must not carry explain fields")
		}

		explained, err := Pack(context.Background(), PackOptions{Root: root, Task: task, Mode: "plan", Explain: true, Now: testTime()})
		Expect(err).NotTo(HaveOccurred())
		Expect(explained.PackID).NotTo(Equal(plain.PackID), "explain is part of the cache key")
		Expect(explained.CandidateFiles).NotTo(BeEmpty())

		kinds := map[string]PackFileKind{}
		for _, file := range explained.CandidateFiles {
			kinds[file.Path] = file.Kind
			Expect(file.Why).NotTo(BeEmpty(), file.Path)
			Expect([]rune(file.Why)).To(HaveLen(len([]rune(file.Why))))
			Expect(len([]rune(file.Why))).To(BeNumerically("<=", PackWhyMaxChars))
			Expect(file.Dir2).To(Equal(PathDir2(file.Path)))
		}

		Expect(kinds).To(HaveKeyWithValue("pkg/runner.go", PackFileKindDef))

		if kind, ok := kinds["pkg/runner_test.go"]; ok {
			Expect(kind).To(Equal(PackFileKindTest))
		}
	})

	It("classifies path shapes and admitting edges", func() {
		Expect(packFileKind(PackFile{Path: "core/tests/test_x.cpp"})).To(Equal(PackFileKindTest))
		Expect(packFileKind(PackFile{Path: "docs/design/a.md"})).To(Equal(PackFileKindDoc))
		Expect(packFileKind(PackFile{Path: "content/worldgen/m.json"})).To(Equal(PackFileKindContent))
		Expect(packFileKind(PackFile{Path: "game/fx/water.gdshader"})).To(Equal(PackFileKindShader))
		Expect(packFileKind(PackFile{Path: "scripts/run_tests.sh"})).To(Equal(PackFileKindScript))
		Expect(packFileKind(PackFile{Path: "game/ui/start_menu.gd"})).To(Equal(PackFileKindUI))
		Expect(packFileKind(PackFile{Path: "tools/worldmap/sun.cpp"})).To(Equal(PackFileKindTool))
		Expect(packFileKind(PackFile{Path: "core/a.cpp", Role: "editable"})).To(Equal(PackFileKindDef))

		out := PackFile{Path: "client/s.cpp", Role: "read_only_evidence", AdmittedBy: &PackAdmission{EdgeKind: "calls", Direction: edgeDirectionOut}}
		in := PackFile{Path: "core/b.cpp", Role: "read_only_evidence", AdmittedBy: &PackAdmission{EdgeKind: "calls", Direction: edgeDirectionIn}}
		imp := PackFile{Path: "client/t.cpp", Role: "read_only_evidence", AdmittedBy: &PackAdmission{EdgeKind: "imports", Direction: edgeDirectionOut}}
		Expect(packFileKind(out)).To(Equal(PackFileKindCaller))
		Expect(packFileKind(in)).To(Equal(PackFileKindCallee))
		Expect(packFileKind(imp)).To(Equal(PackFileKindConsumer))
		Expect(packFileWhy(out, nil)).To(Equal("calls "))
	})

	It("truncates a long reason to the bound without cutting a rune", func() {
		file := PackFile{Path: "a/b.cpp", Role: "read_only_evidence", AdmittedBy: &PackAdmission{EdgeKind: "calls", Direction: edgeDirectionOut, Object: strings.Repeat("é", 200)}}
		why := packFileWhy(file, nil)
		Expect([]rune(why)).To(HaveLen(PackWhyMaxChars))
	})

	It("reports a malformed localization config in the pack unknowns", func() {
		root := build()
		writeFile(filepath.Join(root, ".idiom", "config.json"), `{"localization": {`)
		pack, err := Pack(context.Background(), PackOptions{Root: root, Task: "change Run", Mode: "plan", Now: testTime()})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Join(pack.Unknowns, "\n")).To(ContainSubstring("localization config"))
	})
})

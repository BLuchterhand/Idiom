package languagetools_test

import (
	"context"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GDScript language tools", func() {
	It("routes code navigation references and callers through the syntax index", func() {
		root := GinkgoT().TempDir()
		writePythonToolFixture(filepath.Join(root, "project.godot"), "config_version=5\n")
		writePythonToolFixture(filepath.Join(root, "scripts", "core", "director.gd"), `class_name Director
static func spawn_authored() -> void:
	pass
`)
		writePythonToolFixture(filepath.Join(root, "scripts", "repair", "trestle_repair_event.gd"), `extends Node
const DirectorScript = preload("res://scripts/core/director.gd")
func repair() -> void:
	Director.spawn_authored()
	DirectorScript.spawn_authored()
`)
		manager := newLanguageWorkspaceManager(root, 30)

		callers := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: "code_navigate", Capability: "calls_into", Path: "scripts/core/director.gd", Pattern: "spawn_authored",
		})
		expectToolSuccess(callers, "gdscript_calls_into", "trestle_repair_event.gd")
		Expect(strings.Count(callers.Output, "spawn_authored") >= 2).To(BeTrue())

		references := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: "code_navigate", Capability: "references", Path: "scripts/core/director.gd", Pattern: "Director",
		})
		expectToolSuccess(references, "gdscript_references", "trestle_repair_event.gd")
		Expect(references.PrecisionTier).To(Equal("syntax-index"))
	})
})

package gdscript_test

import (
	"fmt"
	"os"
	"path/filepath"

	"idiom/internal/repository/semantic/gdscript"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GDScript relationships", func() {
	It("skips a GDScript symlink whose target is outside the workspace", func() {
		root := GinkgoT().TempDir()
		externalRoot := GinkgoT().TempDir()
		externalFile := filepath.Join(externalRoot, "outside.gd")
		Expect(os.WriteFile(externalFile, []byte("class_name ExternalOnly\nfunc secret() -> void:\n\tpass\n"), 0o644)).To(Succeed())

		linkPath := filepath.Join(root, "linked.gd")
		if err := os.Symlink(externalFile, linkPath); err != nil {
			Skip(fmt.Sprintf("cannot create symlink in this environment: %v", err))
		}

		index, err := gdscript.BuildIndex(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(index.Files).To(BeEmpty())
	})

	It("indexes class names, calls, resource loads, and signal callbacks", func() {
		root := GinkgoT().TempDir()
		writeGDScriptFixture(root, "project.godot", "config_version=5\n")
		writeGDScriptFixture(root, "scripts/core/director.gd", `class_name Director
signal changed

static func spawn_authored() -> void:
	pass

func instance_method() -> void:
	pass

func notify_changed() -> void:
	changed.emit()
`)
		writeGDScriptFixture(root, "scripts/caller.gd", `extends Node
const DirectorScript = preload("res://scripts/core/director.gd")

func launch() -> void:
	Director.spawn_authored()
	var actor = Director.new()
	var director = Node.new()
	actor.instance_method()
	DirectorScript.spawn_authored()
	actor.changed.connect(_on_changed)
	load("res://scripts/core/director.gd")

func _on_changed() -> void:
	pass
`)

		index, err := gdscript.BuildIndex(root)
		Expect(err).NotTo(HaveOccurred())

		staticCalls := index.FindCallsInto("Director.spawn_authored", "", 10)
		Expect(staticCalls.Resolved).To(HaveLen(2))
		Expect(staticCalls.Resolved[0].Target).To(Equal("spawn_authored"))
		Expect(staticCalls.Resolved[1].Target).To(Equal("spawn_authored"))

		instanceCalls := index.FindCallsInto("Director.instance_method", "", 10)
		Expect(instanceCalls.Resolved).To(HaveLen(1))
		Expect(instanceCalls.Resolved[0].Qualifier).To(Equal("actor"))

		signalCalls := index.FindCallsInto("Director.changed", "", 10)
		Expect(signalCalls.Resolved).To(HaveLen(2))
		signalKinds := []string{signalCalls.Resolved[0].Kind, signalCalls.Resolved[1].Kind}
		Expect(signalKinds).To(ConsistOf("signal_emit", "signal_connect"))

		callbacks := index.FindCallsInto("_on_changed", "", 10)
		Expect(callbacks.UnresolvedCandidates).To(HaveLen(1))
		Expect(callbacks.Resolved).To(BeEmpty())
		Expect(callbacks.UnresolvedCandidates[0].Kind).To(Equal("signal_connect"))

		classReferences := index.FindReferences("Director", "", 10)
		Expect(classReferences).NotTo(BeEmpty())
		for _, reference := range classReferences {
			Expect(reference.Name).To(Equal("Director"))
		}
		resourceReferences := index.FindReferences("scripts/core/director.gd", "", 10)
		Expect(resourceReferences).To(HaveLen(2))
		Expect(resourceReferences[0].TargetPath).To(Equal("scripts/core/director.gd"))
	})

	It("filters duplicate methods by class and target file and separates unknown receivers", func() {
		root := GinkgoT().TempDir()
		writeGDScriptFixture(root, "scripts/director.gd", `class_name Director
func spawn_authored() -> void:
	pass
`)
		writeGDScriptFixture(root, "scripts/other.gd", `class_name Other
func spawn_authored() -> void:
	pass
`)
		writeGDScriptFixture(root, "scripts/caller.gd", `class_name Caller
const DirectorScript = preload("res://scripts/director.gd")
func call_targets() -> void:
	Director.spawn_authored()
	Other.spawn_authored()
	DirectorScript.spawn_authored()
	unknown.spawn_authored()
`)
		index, err := gdscript.BuildIndex(root)
		Expect(err).NotTo(HaveOccurred())

		director := index.FindCallsInto("Director.spawn_authored", "scripts/director.gd", 20)
		Expect(director.Resolved).To(HaveLen(2))
		for _, call := range director.Resolved {
			Expect(call.TargetClass).To(Equal("Director"))
		}
		Expect(director.UnresolvedCandidates).To(HaveLen(1))
		Expect(director.UnresolvedCandidates[0].Qualifier).To(Equal("unknown"))

		other := index.FindCallsInto("Other.spawn_authored", "scripts/other.gd", 20)
		Expect(other.Resolved).To(HaveLen(1))
		Expect(other.Resolved[0].TargetClass).To(Equal("Other"))
	})
})

func writeGDScriptFixture(root string, relative string, content string) {
	GinkgoHelper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}

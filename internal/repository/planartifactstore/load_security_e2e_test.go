package planartifactstore

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	planartifact "idiom/internal/contracts/planartifact"
)

var _ = Describe("Plan artifact reference loading", Label("e2e", "integration", "smoke", "security"), func() {
	It("leaves inline edits unchanged without opening a nonexistent artifact directory", func() {
		baseDir := filepath.Join(GinkgoT().TempDir(), "missing-plan-artifacts")
		artifact := planartifact.PlanArtifact{Edits: []planartifact.PlanEdit{{
			Path:       "target.go",
			NewText:    "package target\n",
			ReplaceAll: true,
		}}}
		original := artifact
		original.Edits = append([]planartifact.PlanEdit(nil), artifact.Edits...)

		Expect(HydrateRefs(baseDir, &artifact)).To(Succeed())
		Expect(artifact).To(Equal(original))
		_, err := os.Stat(baseDir)
		Expect(err).To(MatchError(os.ErrNotExist))
	})

	It("fails when an unresolved ref requires a nonexistent artifact directory", func() {
		baseDir := filepath.Join(GinkgoT().TempDir(), "missing-plan-artifacts")
		artifact := refBackedPlanArtifact("assets/body.txt")

		err := HydrateRefs(baseDir, &artifact)

		Expect(err).To(MatchError(ContainSubstring("open plan artifact directory")))
		Expect(artifact.Edits[0].NewText).To(BeEmpty())
	})

	It("reports malformed mixed inline and ref shapes before opening the artifact directory", func() {
		baseDir := filepath.Join(GinkgoT().TempDir(), "missing-plan-artifacts")
		artifact := planartifact.PlanArtifact{Edits: []planartifact.PlanEdit{{
			Path:       "target.go",
			NewText:    "package target\n",
			ContentRef: "assets/body.txt",
			ReplaceAll: true,
		}}}

		err := HydrateRefs(baseDir, &artifact)

		Expect(err).To(MatchError(ContainSubstring("mutually exclusive")))
		Expect(err.Error()).NotTo(ContainSubstring("open plan artifact directory"))
	})

	DescribeTable("rejects references that escape through symlinks without disclosing their targets",
		func(symlinkShape string, entrypoint string) {
			baseDir := GinkgoT().TempDir()
			outsideDir := GinkgoT().TempDir()
			const outsideSentinel = "outside-plan-reference-sentinel"

			Expect(os.WriteFile(filepath.Join(outsideDir, "body.txt"), []byte(outsideSentinel), 0o600)).To(Succeed())
			switch symlinkShape {
			case "leaf":
				Expect(os.MkdirAll(filepath.Join(baseDir, "assets"), 0o755)).To(Succeed())
				if err := os.Symlink(filepath.Join(outsideDir, "body.txt"), filepath.Join(baseDir, "assets", "body.txt")); err != nil {
					Skip("symlinks are unavailable: " + err.Error())
				}
			case "ancestor":
				outsideRelative, err := filepath.Rel(baseDir, outsideDir)
				Expect(err).NotTo(HaveOccurred())
				if err := os.Symlink(outsideRelative, filepath.Join(baseDir, "assets")); err != nil {
					Skip("symlinks are unavailable: " + err.Error())
				}
			default:
				Fail("unknown symlink shape: " + symlinkShape)
			}

			artifact := refBackedPlanArtifact("assets/body.txt")
			var err error
			switch entrypoint {
			case "hydrate":
				err = HydrateRefs(baseDir, &artifact)
				Expect(artifact.Edits[0].NewText).To(BeEmpty())
			case "load":
				planPath := filepath.Join(baseDir, "plan.json")
				body, marshalErr := json.Marshal(artifact)
				Expect(marshalErr).NotTo(HaveOccurred())
				Expect(os.WriteFile(planPath, body, 0o600)).To(Succeed())

				loaded, loadErr := Load(planPath)
				err = loadErr
				Expect(loaded.Edits).To(BeEmpty())
			default:
				Fail("unknown entrypoint: " + entrypoint)
			}

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).NotTo(ContainSubstring(outsideSentinel))
		},
		Entry("HydrateRefs rejects a leaf symlink", "leaf", "hydrate"),
		Entry("HydrateRefs rejects a symlinked ancestor", "ancestor", "hydrate"),
		Entry("Load rejects a leaf symlink", "leaf", "load"),
		Entry("Load rejects a symlinked ancestor", "ancestor", "load"),
	)

	It("hydrates an ordinary nested reference from the plan directory", func() {
		baseDir := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(baseDir, "assets"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(baseDir, "assets", "body.txt"), []byte("planned body\n"), 0o600)).To(Succeed())

		artifact := refBackedPlanArtifact("assets/body.txt")
		body, err := json.Marshal(artifact)
		Expect(err).NotTo(HaveOccurred())
		planPath := filepath.Join(baseDir, "plan.json")
		Expect(os.WriteFile(planPath, body, 0o600)).To(Succeed())

		loaded, err := Load(planPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(loaded.Edits).To(HaveLen(1))
		Expect(loaded.Edits[0].NewText).To(Equal("planned body\n"))
	})
})

func refBackedPlanArtifact(ref string) planartifact.PlanArtifact {
	return planartifact.NormalizeForSubmit(planartifact.PlanArtifact{
		SchemaVersion: planartifact.LegacySchemaVersion,
		Task: planartifact.PlanArtifactTask{
			Summary: "load a reference-backed edit",
		},
		Edits: []planartifact.PlanEdit{{
			Path:       "target.go",
			NewTextRef: ref,
			ReplaceAll: true,
			Purpose:    "Exercise the durable plan reference boundary.",
		}},
	}, planartifact.NormalizeForSubmitOptions{
		SessionID:     "reference-security-session",
		Created:       "2026-07-12T00:00:00Z",
		PlanID:        "reference-security-plan",
		LegacyRequest: true,
	})
}

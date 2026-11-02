package planartifactstore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/safepath"
)

func Load(workspaceRoot string, path string) (planartifact.PlanArtifact, error) {
	baseDir := filepath.Dir(path)
	root, err := safepath.OpenRootUnderRoot(workspaceRoot, baseDir, "plan artifact directory", safepath.Options{
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
	if err != nil {
		return planartifact.PlanArtifact{}, fmt.Errorf("open plan artifact directory %q: %w", baseDir, err)
	}
	defer root.Close()

	canonicalBaseDir, err := canonicalArtifactDir(workspaceRoot, baseDir)
	if err != nil {
		return planartifact.PlanArtifact{}, fmt.Errorf("resolve plan artifact directory %q: %w", baseDir, err)
	}
	file, err := safepath.OpenFileFromRoot(root, canonicalBaseDir, filepath.Base(path), "plan artifact", safepath.Options{
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
	if err != nil {
		return planartifact.PlanArtifact{}, err
	}
	defer file.Close()
	body, err := io.ReadAll(file)
	if err != nil {
		return planartifact.PlanArtifact{}, err
	}

	artifact, err := planartifact.ParseJSON(body)
	if err != nil {
		return planartifact.PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}

	if len(artifact.Edits) == 0 {
		return planartifact.PlanArtifact{}, fmt.Errorf("plan artifact %q is empty", path)
	}

	if _, err := validateEditsAndCheckRefHydration(&artifact); err != nil {
		return planartifact.PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}

	if err := hydrateRefs(root, canonicalBaseDir, &artifact); err != nil {
		return planartifact.PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}

	if err := planartifact.ValidatePlanArtifact(artifact); err != nil {
		return planartifact.PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}

	return artifact, nil
}

func HydrateRefs(workspaceRoot string, baseDir string, artifact *planartifact.PlanArtifact) error {
	if artifact == nil {
		return nil
	}

	needsHydration, err := validateEditsAndCheckRefHydration(artifact)
	if err != nil {
		return err
	}
	if !needsHydration {
		return nil
	}

	root, err := safepath.OpenRootUnderRoot(workspaceRoot, baseDir, "plan artifact directory", safepath.Options{
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
	if err != nil {
		return fmt.Errorf("open plan artifact directory %q: %w", baseDir, err)
	}
	defer root.Close()

	canonicalBaseDir, err := canonicalArtifactDir(workspaceRoot, baseDir)
	if err != nil {
		return fmt.Errorf("resolve plan artifact directory %q: %w", baseDir, err)
	}
	return hydrateRefs(root, canonicalBaseDir, artifact)
}

func canonicalArtifactDir(workspaceRoot string, baseDir string) (string, error) {
	if !filepath.IsAbs(baseDir) {
		baseDir = filepath.Join(workspaceRoot, baseDir)
	}
	return safepath.CanonicalExistingPath(baseDir)
}

func hydrateRefs(root *os.Root, canonicalBaseDir string, artifact *planartifact.PlanArtifact) error {
	for i := range artifact.Edits {
		edit := &artifact.Edits[i]
		if strings.TrimSpace(edit.NewText) == "" && strings.TrimSpace(edit.NewTextRef) != "" {
			content, err := readEditBodyRef(root, canonicalBaseDir, edit.NewTextRef)
			if err != nil {
				return fmt.Errorf("edits[%d].new_text_ref: %w", i, err)
			}

			edit.NewText = content
		}

		if strings.TrimSpace(edit.Content) == "" && strings.TrimSpace(edit.ContentRef) != "" {
			content, err := readEditBodyRef(root, canonicalBaseDir, edit.ContentRef)
			if err != nil {
				return fmt.Errorf("edits[%d].content_ref: %w", i, err)
			}

			edit.Content = content
		}

	}

	return nil
}

func validateEditsAndCheckRefHydration(artifact *planartifact.PlanArtifact) (bool, error) {
	needsHydration := false
	for i := range artifact.Edits {
		edit := artifact.Edits[i]
		if err := planartifact.ValidatePlanEditShape(edit); err != nil {
			return false, fmt.Errorf("edits[%d] path %q: %w", i, edit.Path, err)
		}

		needsHydration = needsHydration ||
			(strings.TrimSpace(edit.NewText) == "" && strings.TrimSpace(edit.NewTextRef) != "") ||
			(strings.TrimSpace(edit.Content) == "" && strings.TrimSpace(edit.ContentRef) != "")
	}

	return needsHydration, nil
}

func readEditBodyRef(root *os.Root, canonicalBaseDir string, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty ref")
	}

	if filepath.IsAbs(ref) {
		return "", fmt.Errorf("absolute refs are not allowed")
	}

	clean := filepath.Clean(filepath.FromSlash(ref))
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("ref escapes plan artifact directory")
	}

	file, err := safepath.OpenFileFromRoot(root, canonicalBaseDir, clean, "plan artifact reference", safepath.Options{
		RejectSymlinkAncestors: true,
		RejectSymlinkTarget:    true,
	})
	if err != nil {
		return "", err
	}
	defer file.Close()
	body, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

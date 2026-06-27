package planartifactstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	planartifact "idiom/internal/contracts/planartifact"
)

func Load(path string) (planartifact.PlanArtifact, error) {
	body, err := os.ReadFile(path)
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

	if err := HydrateRefs(filepath.Dir(path), &artifact); err != nil {
		return planartifact.PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}

	if err := planartifact.ValidatePlanArtifact(artifact); err != nil {
		return planartifact.PlanArtifact{}, fmt.Errorf("plan artifact %q: %w", path, err)
	}

	return artifact, nil
}

func HydrateRefs(baseDir string, artifact *planartifact.PlanArtifact) error {
	if artifact == nil {
		return nil
	}

	for i := range artifact.Edits {
		edit := &artifact.Edits[i]
		if strings.TrimSpace(edit.NewText) == "" && strings.TrimSpace(edit.NewTextRef) != "" {
			content, err := readEditBodyRef(baseDir, edit.NewTextRef)
			if err != nil {
				return fmt.Errorf("edits[%d].new_text_ref: %w", i, err)
			}

			edit.NewText = content
		}

		if strings.TrimSpace(edit.Content) == "" && strings.TrimSpace(edit.ContentRef) != "" {
			content, err := readEditBodyRef(baseDir, edit.ContentRef)
			if err != nil {
				return fmt.Errorf("edits[%d].content_ref: %w", i, err)
			}

			edit.Content = content
		}

		if err := planartifact.ValidatePlanEditShape(*edit); err != nil {
			return fmt.Errorf("edits[%d] path %q: %w", i, edit.Path, err)
		}
	}

	return nil
}

func readEditBodyRef(baseDir string, ref string) (string, error) {
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

	path := filepath.Join(baseDir, clean)
	rel, err := filepath.Rel(baseDir, path)

	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", fmt.Errorf("ref escapes plan artifact directory")
	}

	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

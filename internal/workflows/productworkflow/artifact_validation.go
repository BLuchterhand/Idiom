package productworkflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ProductArtifactValidationOptions struct {
	Kind          string
	Path          string
	PRDPath       string
	TDDLedgerPath string
}

type ProductArtifactValidationResult struct {
	Status          ProductWorkflowStatus `json:"status"`
	Kind            string                `json:"kind"`
	Path            string                `json:"path"`
	Error           string                `json:"error,omitempty"`
	NextValidAction string                `json:"next_valid_action"`
}

func ValidateProductArtifact(options ProductArtifactValidationOptions) ProductArtifactValidationResult {
	kind := strings.ToLower(strings.TrimSpace(options.Kind))
	path := filepath.Clean(options.Path)
	data, err := os.ReadFile(path)
	result := ProductArtifactValidationResult{
		Status:          ProductWorkflowStatusPass,
		Kind:            kind,
		Path:            path,
		NextValidAction: "artifact is parser-valid for product workflow intake",
	}

	if err == nil {
		switch kind {
		case "prd":
			err = validateProductPRDArtifact(data, options.TDDLedgerPath)
		case "adr":
			err = validateProductADRArtifact(data, path, options.PRDPath, options.TDDLedgerPath)
		default:
			err = fmt.Errorf("unsupported product artifact kind %q", kind)
		}
	} else {
		err = fmt.Errorf("read artifact: %w", err)
	}

	if err != nil {
		result.Status = ProductWorkflowStatusFail
		result.Error = err.Error()
		result.NextValidAction = "regenerate or repair the artifact before continuing"
	}

	return result
}

func validateProductPRDArtifact(data []byte, tddLedgerPath string) error {
	prd, err := ParsePRDMarkdown(data)
	if err != nil || strings.TrimSpace(tddLedgerPath) == "" {
		return err
	}

	tdd, err := readProductArtifactInput(tddLedgerPath, "TDD cross-link input", ParseTDDLedgerJSON)
	if err != nil {
		return err
	}
	if linkErrs := ValidatePRDTDDObligationCrossLinks(prd.Requirements, BuildKnownTDDObligationIDs(tdd, nil)); len(linkErrs) > 0 {
		return errors.New(linkErrs[0].Message)
	}
	return nil
}

func validateProductADRArtifact(data []byte, path string, prdPath string, tddLedgerPath string) error {
	adr, err := ParseADRMarkdown(data)
	if err != nil {
		return err
	}

	var prdIDs map[string]bool
	if strings.TrimSpace(prdPath) != "" {
		prd, err := readProductArtifactInput(prdPath, "PRD cross-link input", ParsePRDMarkdown)
		if err != nil {
			return err
		}
		prdIDs = PRDRequirementIDSet(prd.Requirements)
	}

	var tddIDs map[string]bool
	if strings.TrimSpace(tddLedgerPath) != "" {
		tdd, err := readProductArtifactInput(tddLedgerPath, "TDD cross-link input", ParseTDDLedgerJSON)
		if err != nil {
			return err
		}
		tddIDs = BuildKnownTDDObligationIDs(tdd, nil)
	}

	if prdIDs == nil && tddIDs == nil {
		return nil
	}

	if linkErrs := ValidateADRCrossLinks([]ParsedADR{{RelPath: path, Doc: adr}}, prdIDs, nil, tddIDs); len(linkErrs) > 0 {
		return errors.New(linkErrs[0].Message)
	}
	return nil
}

func readProductArtifactInput[T any](path string, label string, parse func([]byte) (T, error)) (T, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		var zero T
		return zero, fmt.Errorf("read %s: %w", label, err)
	}
	return parse(data)
}

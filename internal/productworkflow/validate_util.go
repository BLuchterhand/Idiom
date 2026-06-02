package productworkflow

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

func canonicalStringSet(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

type ArtifactState string

const (
	ArtifactStateDraft      ArtifactState = "draft"
	ArtifactStateAudited    ArtifactState = "audited"
	ArtifactStateAccepted   ArtifactState = "accepted"
	ArtifactStateBlocked    ArtifactState = "blocked"
	ArtifactStateDispatched ArtifactState = "dispatched"
)

var validArtifactStates = map[ArtifactState]bool{
	ArtifactStateDraft:      true,
	ArtifactStateAudited:    true,
	ArtifactStateAccepted:   true,
	ArtifactStateBlocked:    true,
	ArtifactStateDispatched: true,
}

func ValidateArtifactState(status string) error {
	if status == "" {
		return errors.New("artifact status is empty")
	}
	if !validArtifactStates[ArtifactState(status)] {
		return fmt.Errorf("invalid artifact status %q; valid states: draft, audited, accepted, blocked, dispatched", status)
	}
	return nil
}

func require(errs *[]error, value string, message string) {
	if strings.TrimSpace(value) == "" {
		*errs = append(*errs, errors.New(message))
	}
}

func isValidCommitHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func ClassifyReportFileName(name string) (reportType string, known bool) {
	if strings.HasSuffix(name, "-implementation.json") {
		return "implementation", true
	}
	if strings.HasSuffix(name, "-review.json") {
		return "review", true
	}
	if strings.HasSuffix(name, "-investigation.json") {
		return "investigation", true
	}
	return "", false
}

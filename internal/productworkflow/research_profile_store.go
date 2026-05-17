package productworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ProfileFinding struct {
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	File     string `json:"file"`
	Severity string `json:"severity"`
}

type ProfileStoreResult struct {
	Profiles []ResearchProfile `json:"profiles"`
	Findings []ProfileFinding  `json:"findings"`
}

func DiscoverProfiles(projectRoot string) (*ProfileStoreResult, error) {
	result := &ProfileStoreResult{
		Profiles: []ResearchProfile{},
		Findings: []ProfileFinding{},
	}
	profilesDir := filepath.Join(projectRoot, ".idiom", "research", "profiles")
	entries, err := os.ReadDir(profilesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, fmt.Errorf("read profiles directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.Contains(name, string(filepath.Separator)+".."+string(filepath.Separator)) ||
			name == ".." || strings.HasPrefix(name, "../") ||
			strings.HasSuffix(name, "/..") || strings.HasPrefix(name, "/") {
			result.Findings = append(result.Findings, ProfileFinding{
				Kind:     "path_traversal",
				Message:  fmt.Sprintf("filename %q contains path traversal sequence", name),
				File:     name,
				Severity: "error",
			})
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			result.Findings = append(result.Findings, ProfileFinding{
				Kind:     "non_json_file",
				Message:  fmt.Sprintf("skipping non-JSON file %q", name),
				File:     name,
				Severity: "warning",
			})
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(profilesDir, name))
		if readErr != nil {
			result.Findings = append(result.Findings, ProfileFinding{
				Kind:     "read_error",
				Message:  fmt.Sprintf("failed to read %q: %v", name, readErr),
				File:     name,
				Severity: "error",
			})
			continue
		}
		profile, parseErr := ParseResearchProfileJSON(data)
		if parseErr != nil {
			result.Findings = append(result.Findings, ProfileFinding{
				Kind:     "validation_error",
				Message:  fmt.Sprintf("profile %q failed validation: %v", name, parseErr),
				File:     name,
				Severity: "error",
			})
			continue
		}
		if strings.TrimSpace(profile.ProfileID) == "" {
			stem := strings.TrimSuffix(name, filepath.Ext(name))
			profile.ProfileID = stem
		}
		result.Profiles = append(result.Profiles, profile)
	}
	return result, nil
}

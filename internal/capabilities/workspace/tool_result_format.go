package workspace

import (
	"encoding/json"
	"strings"

	"idiom/internal/repository/comprehension"
)

func FormatToolResultForModel(result ToolResult) string {
	var b strings.Builder

	b.WriteString("[workspace tool result]\n")
	b.WriteString("tool: " + result.Tool + "\n")
	b.WriteString("summary: " + result.Summary + "\n")

	if strings.TrimSpace(result.PrecisionTier) != "" {
		b.WriteString("precision_tier: " + result.PrecisionTier + "\n")
	}

	if len(result.ChangedPaths) > 0 {
		b.WriteString("changed_paths:\n")
		for _, path := range result.ChangedPaths {
			if path = strings.TrimSpace(path); path != "" {
				b.WriteString("- " + path + "\n")
			}
		}
	}

	if result.Error != "" {
		b.WriteString("status: error\n")
		b.WriteString("error:\n" + result.Error + "\n")
	} else {
		b.WriteString("status: ok\n")
	}

	if strings.TrimSpace(result.Output) != "" {
		b.WriteString("output:\n")
		b.WriteString(formatToolResultOutputForModel(result))
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

func formatToolResultOutputForModel(result ToolResult) string {
	output := strings.TrimSpace(result.Output)
	if strings.TrimSpace(result.Tool) == ToolNameContextPack {
		if compact := compactContextPackOutputForModel(output); compact != "" {
			return compact
		}
	}

	return output
}

// compactContextPackOutputForModel renders a PackResult JSON output as
// model-facing compact text using the shared CompactPackProjection and
// RenderCompactPackProjection from the comprehension package.
func compactContextPackOutputForModel(output string) string {
	var result comprehension.PackResult

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return ""
	}

	if len(result.CandidateFiles) == 0 && len(result.CandidateSymbols) == 0 && len(result.Tests) == 0 {
		return ""
	}

	// Use the pre-computed CompactProjection from Pack() when available;
	// fall back to building a fresh projection for pre-Atom-5 cached
	// packs that lack the field.
	projection := result.CompactProjection
	if projection.PackID == "" {
		projection = comprehension.NewCompactPackProjection(result)
	}

	rendered, _ := comprehension.RenderCompactPackProjection(projection, result.BudgetTokens)

	// Preserve artifact_path from the full PackResult for operator
	// convenience. It is inserted after the pack_id line so it stays
	// close to the identity header.
	if artifact := strings.TrimSpace(result.ArtifactPath); artifact != "" {
		rendered = insertArtifactPathAfterPackID(rendered, artifact)
	}

	return rendered
}

// insertArtifactPathAfterPackID inserts an artifact_path line after the
// pack_id line in a rendered compact projection. If no pack_id line is
// found, the artifact path is prepended as a simple note.
func insertArtifactPathAfterPackID(rendered string, artifactPath string) string {
	const packIDPrefix = "pack_id: "

	idx := strings.Index(rendered, packIDPrefix)

	if idx < 0 {
		// No pack_id line; prepend as a note after the summary header.
		headerEnd := strings.Index(rendered, "\n")
		if headerEnd < 0 {
			return "artifact_path: " + artifactPath + "\n" + rendered
		}

		return rendered[:headerEnd+1] + "artifact_path: " + artifactPath + "\n" + rendered[headerEnd+1:]
	}

	// Find end of the pack_id line.
	endIdx := strings.Index(rendered[idx:], "\n")
	if endIdx < 0 {
		return rendered + "\nartifact_path: " + artifactPath
	}

	insertPos := idx + endIdx + 1

	return rendered[:insertPos] + "artifact_path: " + artifactPath + "\n" + rendered[insertPos:]
}

func FormatToolTrace(result ToolResult) string {
	var b strings.Builder

	b.WriteString(result.Tool)

	if result.Summary != "" {
		b.WriteString(" | " + result.Summary)
	}

	if strings.TrimSpace(result.PrecisionTier) != "" {
		b.WriteString(" | precision_tier=" + result.PrecisionTier)
	}

	if len(result.ChangedPaths) > 0 {
		b.WriteString(" | changed_paths=" + strings.Join(result.ChangedPaths, ","))
	}

	if result.Error != "" {
		b.WriteString("\nerror:\n" + result.Error)
	}

	if strings.TrimSpace(result.Output) != "" {
		b.WriteString("\noutput:\n" + strings.TrimSpace(result.Output))
	}

	return b.String()
}

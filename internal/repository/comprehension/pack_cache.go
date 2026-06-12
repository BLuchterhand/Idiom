package comprehension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func packWorkspaceStateFingerprints(root string, learnedWeights map[string]float64) ComprehensionStateFingerprintSet {
	state := ComprehensionStateFingerprints(root)
	if learnedWeights == nil {
		return state
	}

	data, _ := json.Marshal(learnedWeights)

	state.RankerWeightsFingerprint = shortSHA256Hex(append([]byte("learned_weights_override\x00"), data...))
	state.WorkspaceStateFingerprint = composeWorkspaceStateFingerprint(state.RankerWeightsFingerprint, state.RuntimeTracesFingerprint)

	return state
}

// viewKeyFromOptions builds the content-addressed cache key from
// PackOptions. Identical option sets produce identical keys across
// processes; the active graph index_id makes the key invalidate when
// the codebase content shifts under the harness.
func viewKeyFromOptions(opts PackOptions, indexID, mode string, intent Intent, workspaceStateFingerprints ComprehensionStateFingerprintSet) ViewKey {
	return ViewKey{
		IndexID: indexID,
		Task:    strings.TrimSpace(opts.Task),
		Mode:    mode,
		Query: ViewQueryInputs{
			Path:                      strings.TrimSpace(opts.Path),
			Scope:                     strings.TrimSpace(opts.Scope),
			Limit:                     opts.Limit,
			BudgetTokens:              opts.BudgetTokens,
			RequiredTests:             opts.RequiredTests,
			RequiredEditPaths:         opts.RequiredEditPaths,
			ForbiddenPaths:            opts.ForbiddenPaths,
			Intent:                    string(intent),
			IncludeStaleRuntime:       opts.IncludeStaleRuntime,
			AlgorithmVersion:          packAlgorithmVersion,
			WorkspaceStateFingerprint: workspaceStateFingerprints.WorkspaceStateFingerprint,
			RankerWeightsFingerprint:  workspaceStateFingerprints.RankerWeightsFingerprint,
			RuntimeTracesFingerprint:  workspaceStateFingerprints.RuntimeTracesFingerprint,
		},
	}
}

// savePack writes the human-readable JSON export under
// .idiom/comprehension/views/<pack_id>.json. The SQLite graph_views
// row is the source of truth; this file mirrors it for inspection.
func savePack(root string, result PackResult) error {
	path := filepath.Join(root, ".idiom", "comprehension", "views", result.PackID+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, append(data, '\n'), 0o644)
}

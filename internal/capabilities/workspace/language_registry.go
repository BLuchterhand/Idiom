package workspace

import "idiom/internal/repository/semantic/language"

type Capability = language.Capability

const (
	CapFileOutline     = language.CapFileOutline
	CapFindSymbol      = language.CapFindSymbol
	CapWorkspaceSymbol = language.CapWorkspaceSymbol
	CapDefinition      = language.CapDefinition
	CapReferences      = language.CapReferences
	CapCallsFrom       = language.CapCallsFrom
	CapCallsInto       = language.CapCallsInto
	CapReadSymbol      = language.CapReadSymbol
	CapExtractBody     = language.CapExtractBody
	CapRelatedFiles    = language.CapRelatedFiles
	CapImplementations = language.CapImplementations
	CapDiagnostics     = language.CapDiagnostics
	CapOverview        = language.CapOverview
	CapEntrypoints     = language.CapEntrypoints
)

type LanguageSupport = language.LanguageSupport

// ActiveLanguages returns languages whose semantic detection reports true
// against the workspace root. Kept on Manager because the workspace package
// owns active workspace state; the registry itself lives under semantic.
func (m *Manager) ActiveLanguages() []LanguageSupport {
	if !m.Enabled() {
		return nil
	}

	return language.ActiveLanguages(m.cfg.Root)
}

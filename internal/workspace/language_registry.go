package workspace

import "idiom/internal/semantic/language"

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
	CapRelatedFiles    = language.CapRelatedFiles
	CapImplementations = language.CapImplementations
	CapDiagnostics     = language.CapDiagnostics
	CapOverview        = language.CapOverview
	CapEntrypoints     = language.CapEntrypoints
)

type LanguageSupport = language.LanguageSupport

func LanguageRegistry() []LanguageSupport {
	return language.LanguageRegistry()
}

func LanguageForExtension(ext string) (LanguageSupport, bool) {
	return language.LanguageForExtension(ext)
}

func LanguageForFile(path string) (LanguageSupport, bool) {
	return language.LanguageForFile(path)
}

func LanguageForName(name string) (LanguageSupport, bool) {
	return language.LanguageForName(name)
}

func DiagnosticToolForLanguage(name string) string {
	return language.DiagnosticToolForLanguage(name)
}

func DiagnosticToolNames() []string {
	return language.DiagnosticToolNames()
}

func IsDiagnosticToolName(tool string) bool {
	return language.IsDiagnosticToolName(tool)
}

// ActiveLanguages returns languages whose semantic detection reports true
// against the workspace root. Kept on Manager because the workspace package
// owns active workspace state; the registry itself lives under semantic.
func (m *Manager) ActiveLanguages() []LanguageSupport {
	if !m.Enabled() {
		return nil
	}
	return language.ActiveLanguages(m.cfg.Root)
}

package workspace

import semanticlanguage "idiom/internal/repository/semantic/language"

// ActiveLanguages returns languages whose semantic detection reports true
// against the workspace root. Kept on Manager because the workspace package
// owns active workspace state; the registry itself lives under semantic.
func (m *Manager) ActiveLanguages() []semanticlanguage.LanguageSupport {
	if !m.Enabled() {
		return nil
	}

	return semanticlanguage.ActiveLanguages(m.cfg.Root)
}

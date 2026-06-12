package languagetools

import (
	"path/filepath"
	"strings"
)

func (m *Manager) languagePathFilterForRequest(req ToolRequest, explicitFilter func(string) string) (string, error) {
	if strings.TrimSpace(req.Path) != "" {
		return explicitFilter(req.Path), nil
	}

	if strings.TrimSpace(req.ID) == "" {
		return "", nil
	}

	path, err := m.pathForHandle(req.ID)
	if err != nil {
		return "", err
	}

	return filepath.ToSlash(m.displayPath(path)), nil
}

package app

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"idiom/internal/backends"
)

type modelPopupRole struct {
	name        string
	current     string
	aliases     []string
	description string
}

type modelPopupState struct {
	open     bool
	roles    []modelPopupRole
	cursor   int
	modelIdx map[string]int
}

func (m *Model) openModelPopup() {
	roles := m.buildPopupRoles()
	if len(roles) == 0 {
		m.status = "No models discovered. Check .idiom/backends.json or server status."
		return
	}
	modelIdx := make(map[string]int, len(roles))
	for _, r := range roles {
		idx := slices.Index(r.aliases, r.current)
		if idx < 0 {
			idx = 0
		}
		modelIdx[r.name] = idx
	}
	m.modelPopup = modelPopupState{
		open:     true,
		roles:    roles,
		cursor:   0,
		modelIdx: modelIdx,
	}
}

func (m *Model) closeModelPopup() {
	m.modelPopup = modelPopupState{}
}

func (m *Model) buildPopupRoles() []modelPopupRole {
	allAliases := m.collectAllAliases()
	if len(allAliases) == 0 {
		return nil
	}

	roleNames := []string{backends.RolePlanner, backends.RoleExecutor, backends.RoleClassifier}
	roleDescs := map[string]string{
		backends.RolePlanner:    "Strategy and planning decisions",
		backends.RoleExecutor:   "Code implementation and edits",
		backends.RoleClassifier: "Task classification and routing",
	}

	var roles []modelPopupRole
	for _, rn := range roleNames {
		current := m.currentAliasForRole(rn)
		roles = append(roles, modelPopupRole{
			name:        rn,
			current:     current,
			aliases:     allAliases,
			description: roleDescs[rn],
		})
	}
	return roles
}

func (m *Model) currentAliasForRole(role string) string {
	if file, found, err := backends.LoadFromProjectRoot(m.workspace.Root()); err == nil && found {
		if prof, ok := file.Profile(""); ok {
			if alias, ok := prof.AliasForRole(role); ok {
				return alias
			}
		}
	}
	return m.activeModelDisplayID()
}

func (m *Model) collectAllAliases() []string {
	seen := make(map[string]bool)
	var names []string

	if file, found, err := backends.LoadFromProjectRoot(m.workspace.Root()); err == nil && found {
		if prof, ok := file.Profile(""); ok {
			for _, model := range prof.Models {
				if alias := strings.TrimSpace(model.Alias); alias != "" && !seen[alias] {
					seen[alias] = true
					names = append(names, alias)
				}
			}
		}
		for _, prof := range file.Profiles {
			for _, model := range prof.Models {
				if alias := strings.TrimSpace(model.Alias); alias != "" && !seen[alias] {
					seen[alias] = true
					names = append(names, alias)
				}
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if models, err := m.client.ListModels(ctx); err == nil {
		for _, mi := range models {
			id := strings.TrimSpace(mi.ID)
			if id != "" && !seen[id] {
				seen[id] = true
				names = append(names, id)
			}
		}
	}

	return names
}

func (m *Model) handleModelPopupKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	p := &m.modelPopup
	switch msg.Type {
	case tea.KeyEsc:
		m.closeModelPopup()
		return nil, true
	case tea.KeyUp:
		if p.cursor > 0 {
			p.cursor--
		}
		return nil, true
	case tea.KeyDown:
		if p.cursor < len(p.roles)-1 {
			p.cursor++
		}
		return nil, true
	case tea.KeyTab:
		role := p.roles[p.cursor]
		idx := p.modelIdx[role.name]
		idx = (idx + 1) % len(role.aliases)
		p.modelIdx[role.name] = idx
		return nil, true
	case tea.KeyShiftTab:
		role := p.roles[p.cursor]
		idx := p.modelIdx[role.name]
		idx = (idx - 1 + len(role.aliases)) % len(role.aliases)
		p.modelIdx[role.name] = idx
		return nil, true
	case tea.KeyEnter:
		m.applyPopupSelections()
		m.closeModelPopup()
		return nil, true
	}
	return nil, false
}

func (m *Model) applyPopupSelections() {
	plannerRole := &m.modelPopup.roles[0]
	idx, ok := m.modelPopup.modelIdx[plannerRole.name]
	if ok && idx < len(plannerRole.aliases) {
		chosen := plannerRole.aliases[idx]
		if chosen != plannerRole.current {
			m.applyModelSwap(chosen)
		}
	}
}

func (m *Model) renderModelPopup(width, height int) string {
	p := &m.modelPopup
	if !p.open || len(p.roles) == 0 {
		return ""
	}

	popupW := min(52, max(36, width*2/3))
	maxModels := 0
	if len(p.roles) > 0 {
		for _, r := range p.roles {
			if len(r.aliases) > maxModels {
				maxModels = len(r.aliases)
			}
		}
	}
	popupH := len(p.roles)*3 + maxModels + 4

	var b strings.Builder
	b.WriteString(popupTitleStyle.Width(popupW).Render("◆ Models"))
	b.WriteString("\n")
	b.WriteString(popupHintStyle.Width(popupW).Render("↑↓ navigate · Tab cycle · Enter confirm · Esc cancel"))
	b.WriteString("\n\n")

	for i, role := range p.roles {
		selected := i == p.cursor
		modelIdx := p.modelIdx[role.name]
		chosenAlias := ""
		if modelIdx < len(role.aliases) {
			chosenAlias = role.aliases[modelIdx]
		}

		var nameStyle lipgloss.Style
		if selected {
			nameStyle = popupRoleActiveStyle
		} else {
			nameStyle = popupRoleInactiveStyle
		}

		prefix := "  "
		if selected {
			prefix = "▸ "
		}

		b.WriteString(nameStyle.Width(popupW).Render(prefix + role.name))
		b.WriteString("\n")

		if selected && len(role.aliases) > 0 {
			for j, alias := range role.aliases {
				marker := "  "
				label := alias
				if j == modelIdx {
					marker = "▸▸"
					label = popupModelActiveStyle.Render(alias)
				} else {
					label = popupModelInactiveStyle.Render(alias)
				}
				b.WriteString(popupModelRowStyle.Width(popupW).Render(marker + " " + label))
				b.WriteString("\n")
			}
		} else {
			b.WriteString(popupModelRowStyle.Width(popupW).Render("  " + chosenAlias))
			b.WriteString("\n")
		}

		b.WriteString(popupDescStyle.Width(popupW).Render("  " + role.description))
		b.WriteString("\n")
	}

	content := b.String()
	lines := strings.Split(content, "\n")
	for len(lines) < popupH {
		lines = append(lines, "")
		content += "\n"
	}

	boxW := popupW + 4
	popupBox := popupBoxStyle.Width(boxW).Render(content)

	lines = strings.Split(popupBox, "\n")
	boxH := len(lines)

	padTop := max(0, (height-boxH)/2)
	padLeft := max(0, (width-lipgloss.Width(popupBox))/2)

	indent := strings.Repeat(" ", padLeft)
	vertPad := strings.Repeat("\n", padTop)

	var overlay strings.Builder
	overlay.WriteString(vertPad)
	for _, line := range lines {
		overlay.WriteString(indent)
		overlay.WriteString(line)
		overlay.WriteString("\n")
	}
	return overlay.String()
}

package planexecute

import (
	"encoding/json"
	"os"
	"strings"

	planartifact "idiom/internal/contracts/planartifact"
	"idiom/internal/platform/atomicfile"
)

func inheritPlanExecutePromptPackID(plan *planartifact.PlanArtifact, prompt string) string {
	if plan == nil || strings.TrimSpace(plan.PackID) != "" {
		return ""
	}

	packID := ExtractPlanExecutePromptPackID(prompt)
	if packID == "" {
		return ""
	}

	plan.PackID = packID
	if strings.TrimSpace(plan.Packet.PackID) == "" {
		plan.Packet.PackID = packID
	}

	if status := strings.TrimSpace(string(plan.Packet.Status)); status == "" || status == string(planartifact.PacketStatusNotProvided) {
		plan.Packet.Status = planartifact.PacketStatusProvided
	}

	return packID
}

func ExtractPlanExecutePromptPackID(prompt string) string {
	for _, rawLine := range strings.Split(prompt, "\n") {
		line := strings.TrimSpace(rawLine)
		lower := strings.ToLower(line)

		if !strings.Contains(lower, "pack") {
			continue
		}

		for _, token := range strings.FieldsFunc(line, func(r rune) bool {
			return !((r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9'))
		}) {
			token = strings.ToLower(strings.TrimSpace(token))
			if isPlanExecutePackID(token) {
				return token
			}
		}
	}

	return ""
}

func isPlanExecutePackID(value string) bool {
	if len(value) != 16 {
		return false
	}

	for _, r := range value {
		if !((r >= 'a' && r <= 'f') || (r >= '0' && r <= '9')) {
			return false
		}
	}

	return true
}

func patchPlanArtifactPackID(path string, packID string) error {
	packID = strings.TrimSpace(packID)
	if path == "" || packID == "" {
		return nil
	}

	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var raw map[string]json.RawMessage

	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}

	encodedPackID, err := json.Marshal(packID)
	if err != nil {
		return err
	}

	raw["pack_id"] = encodedPackID

	var packet map[string]json.RawMessage

	if body, ok := raw["packet"]; ok {
		_ = json.Unmarshal(body, &packet)
	}

	if packet == nil {
		packet = map[string]json.RawMessage{}
	}

	if current, ok := packet["pack_id"]; !ok || strings.Trim(strings.TrimSpace(string(current)), `"`) == "" {
		packet["pack_id"] = encodedPackID
	}

	if current, ok := packet["status"]; !ok || strings.Trim(strings.TrimSpace(string(current)), `"`) == "" || strings.Trim(strings.TrimSpace(string(current)), `"`) == string(planartifact.PacketStatusNotProvided) {
		encodedStatus, err := json.Marshal(string(planartifact.PacketStatusProvided))
		if err != nil {
			return err
		}

		packet["status"] = encodedStatus
	}

	encodedPacket, err := json.Marshal(packet)
	if err != nil {
		return err
	}

	raw["packet"] = encodedPacket

	updated, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}

	updated = append(updated, '\n')

	return atomicfile.WriteFile(path, updated, 0o644)
}

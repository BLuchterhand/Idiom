package runprofile

// DebugLogMode selects whether conversation logs retain sensitive content or
// apply the normal redaction policy.
type DebugLogMode string

const (
	DebugLogModeRedacted DebugLogMode = "redacted"
	DebugLogModeFull     DebugLogMode = "full"
)

// ParseDebugLogMode validates the exact serialized vocabulary. Unlike agent
// mode selection, debug-log configuration has historically required an exact
// lowercase value; retaining that rule avoids silently enabling full logs.
func ParseDebugLogMode(value string) (DebugLogMode, bool) {
	mode := DebugLogMode(value)
	switch mode {
	case DebugLogModeRedacted, DebugLogModeFull:
		return mode, true
	default:
		return "", false
	}
}

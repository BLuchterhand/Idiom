package commandmeta

import "strings"

var telemetrySubcommandPrefixes = map[string]bool{
	"architect": true, "audit": true, "brief": true, "context": true,
	"lane": true, "local-ops": true, "mcp": true, "product": true,
	"product-evidence": true, "ranker": true, "recover": true, "release": true,
	"tool": true,
}

var telemetryDirectCommands = map[string]bool{
	"artifact":  true,
	"doctor":    true,
	"implement": true,
}

func TelemetryName(args []string) string {
	if len(args) == 0 {
		return "tui"
	}

	if telemetrySubcommandPrefixes[args[0]] {
		if len(args) > 1 {
			return args[0] + ":" + args[1]
		}
		return args[0]
	}

	if telemetryDirectCommands[args[0]] {
		return args[0]
	}

	for _, arg := range args {
		if strings.HasPrefix(arg, "-prompt=") || strings.HasPrefix(arg, "--prompt=") {
			return "prompt"
		}

		if strings.HasPrefix(arg, "-turns-file=") || strings.HasPrefix(arg, "--turns-file=") {
			return "turns_file"
		}

		switch arg {
		case "-server-only", "--server-only":
			return "server"
		case "-prompt", "--prompt":
			return "prompt"
		case "-turns-file", "--turns-file":
			return "turns_file"
		case "-list-sessions", "--list-sessions":
			return "list_sessions"
		}
	}

	return "tui"
}

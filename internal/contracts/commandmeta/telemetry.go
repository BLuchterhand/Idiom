package commandmeta

import "strings"

func TelemetryName(args []string) string {
	if len(args) == 0 {
		return "tui"
	}

	switch args[0] {
	case "architect", "audit", "brief", "context", "lane", "local-ops", "mcp",
		"product", "product-evidence", "ranker", "recover", "release", "tool":
		if len(args) > 1 {
			return args[0] + ":" + args[1]
		}
		return args[0]
	case "artifact", "doctor", "implement":
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

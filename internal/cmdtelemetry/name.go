package cmdtelemetry

import "strings"

func CommandName(args []string) string {
	if len(args) == 0 {
		return "tui"
	}
	switch args[0] {
	case "tool":
		if len(args) > 1 {
			return "tool:" + args[1]
		}
		return "tool"
	case "mcp":
		if len(args) > 1 {
			return "mcp:" + args[1]
		}
		return "mcp"
	case "context":
		if len(args) > 1 {
			return "context:" + args[1]
		}
		return "context"
	case "artifact", "implement", "doctor":
		return args[0]
	case "recover":
		if len(args) > 1 {
			return "recover:" + args[1]
		}
		return "recover"
	case "local-ops":
		if len(args) > 1 {
			return "local-ops:" + args[1]
		}
		return "local-ops"
	case "product":
		if len(args) > 1 {
			return "product:" + args[1]
		}
		return "product"
	case "product-evidence":
		if len(args) > 1 {
			return "product-evidence:" + args[1]
		}
		return "product-evidence"
	case "release":
		if len(args) > 1 {
			return "release:" + args[1]
		}
		return "release"
	case "lane":
		if len(args) > 1 {
			return "lane:" + args[1]
		}
		return "lane"
	case "architect":
		if len(args) > 1 {
			return "architect:" + args[1]
		}
		return "architect"
	case "audit":
		if len(args) > 1 {
			return "audit:" + args[1]
		}
		return "audit"
	case "ranker":
		if len(args) > 1 {
			return "ranker:" + args[1]
		}
		return "ranker"
	case "brief":
		if len(args) > 1 {
			return "brief:" + args[1]
		}
		return "brief"
	default:
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
}

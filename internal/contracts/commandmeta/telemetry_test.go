package commandmeta

import "testing"

func TestTelemetryNamesPreserveCurrentCommandBehavior(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "empty starts tui", args: nil, want: "tui"},
		{name: "tool subcommand", args: []string{"tool", "run"}, want: "tool:run"},
		{name: "tool root", args: []string{"tool"}, want: "tool"},
		{name: "mcp subcommand", args: []string{"mcp", "serve"}, want: "mcp:serve"},
		{name: "context subcommand", args: []string{"context", "index"}, want: "context:index"},
		{name: "direct implement", args: []string{"implement"}, want: "implement"},
		{name: "direct artifact", args: []string{"artifact"}, want: "artifact"},
		{name: "recover subcommand", args: []string{"recover", "resume"}, want: "recover:resume"},
		{name: "recover inspect subcommand", args: []string{"recover", "inspect"}, want: "recover:inspect"},
		{name: "local ops subcommand", args: []string{"local-ops", "doctor"}, want: "local-ops:doctor"},
		{name: "local ops benchmark subcommand", args: []string{"local-ops", "benchmark", "--json"}, want: "local-ops:benchmark"},
		{name: "product subcommand", args: []string{"product", "lint"}, want: "product:lint"},
		{name: "product lint flags", args: []string{"product", "lint", "--effort-root", "."}, want: "product:lint"},
		{name: "product audit flags", args: []string{"product", "audit", "--effort-root", "."}, want: "product:audit"},
		{name: "product status flags", args: []string{"product", "status", "--effort-root", "."}, want: "product:status"},
		{name: "product evidence subcommand", args: []string{"product-evidence", "collect"}, want: "product-evidence:collect"},
		{name: "release subcommand", args: []string{"release", "check"}, want: "release:check"},
		{name: "lane subcommand", args: []string{"lane", "implement"}, want: "lane:implement"},
		{name: "architect subcommand", args: []string{"architect", "review"}, want: "architect:review"},
		{name: "audit subcommand", args: []string{"audit", "events"}, want: "audit:events"},
		{name: "audit watch flags", args: []string{"audit", "watch", "--once"}, want: "audit:watch"},
		{name: "ranker subcommand", args: []string{"ranker", "train"}, want: "ranker:train"},
		{name: "brief subcommand", args: []string{"brief", "render"}, want: "brief:render"},
		{name: "doctor direct", args: []string{"doctor"}, want: "doctor"},
		{name: "doctor direct flags", args: []string{"doctor", "--json", "--profile", "implement"}, want: "doctor"},
		{name: "prompt equals fallback", args: []string{"run", "--prompt=hello"}, want: "prompt"},
		{name: "prompt flag fallback", args: []string{"run", "--prompt", "hello"}, want: "prompt"},
		{name: "turns file equals fallback", args: []string{"run", "-turns-file=turns.jsonl"}, want: "turns_file"},
		{name: "turns file flag fallback", args: []string{"run", "-turns-file", "turns.jsonl"}, want: "turns_file"},
		{name: "server only fallback", args: []string{"run", "--server-only"}, want: "server"},
		{name: "list sessions fallback", args: []string{"run", "-list-sessions"}, want: "list_sessions"},
		{name: "currently unclassified root command", args: []string{"plan-execute"}, want: "tui"},
		{name: "unknown fallback", args: []string{"unknown"}, want: "tui"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TelemetryName(tt.args); got != tt.want {
				t.Fatalf("TelemetryName(%#v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

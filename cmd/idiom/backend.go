package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"idiom/internal/backends"
	"idiom/internal/telemetry"
)

// Test-friendly indirections. Phase 4b's launcher functions take
// option-based DI, but the CLI plumbing wants a single-call shape;
// these vars let lane_postflight-style tests stub Launch/Stop/
// Status without spawning real processes. Production callers
// shouldn't reassign them. Phase 4c added backendServeProxy on the
// same pattern so the proxy CLI is testable without a real listener.
var (
	backendLaunch     = backends.Launch
	backendStop       = backends.Stop
	backendStatus     = backends.Status
	backendServeProxy = backends.ServeProxy
)

// runBackendMain dispatches `idiom backend <subcommand>`. Phase 4
// ships `list` (introspect the loaded backends.json); Phase 4b
// adds `start`, `stop`, `status`; Phase 4c adds `proxy`, the
// alias-routing front door that fans `model:` requests out to the
// per-alias backends started by `start`.
func runBackendMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runBackendMainWithTelemetry(args, stdout, stderr, nil)
}

func runBackendMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	logCLIPhase(rec, "backend_start", map[string]any{"argc": len(args)})
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printBackendUsage(stderr)
		return 0
	}
	switch args[0] {
	case "list":
		return runBackendList(args[1:], stdout, stderr)
	case "start":
		return runBackendStart(args[1:], stdout, stderr)
	case "stop":
		return runBackendStop(args[1:], stdout, stderr)
	case "status":
		return runBackendStatus(args[1:], stdout, stderr)
	case "proxy":
		return runBackendProxy(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "backend: unknown subcommand %q\n", args[0])
		printBackendUsage(stderr)
		return 2
	}
}

// resolveProjectRoot returns the project root the operator named
// (--project-root) or the current working directory when omitted.
// Used both to locate .idiom/backends.json and to derive the PID
// file directory (.idiom/run) so the two stay co-located.
func resolveProjectRoot(projectRoot string, stderr io.Writer) (string, int) {
	root := strings.TrimSpace(projectRoot)
	if root != "" {
		return root, 0
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return "", 1
	}
	return cwd, 0
}

// runDirFor returns the canonical PID file directory for a project.
// Centralized so start/stop/status all agree on where to look.
func runDirFor(projectRoot string) string {
	return filepath.Join(projectRoot, ".idiom", "run")
}

// loadProfileFromFlags resolves --project-root + --profile into a
// loaded backends.json profile and the resolved project root.
// Shared by start/stop/status so the flag set, error messages,
// and PID file directory all stay consistent across them.
func loadProfileFromFlags(projectRoot, profileName string, stderr io.Writer) (backends.Profile, string, int) {
	root, code := resolveProjectRoot(projectRoot, stderr)
	if code != 0 {
		return backends.Profile{}, "", code
	}
	file, ok, err := backends.LoadFromProjectRoot(root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return backends.Profile{}, root, 1
	}
	if !ok {
		fmt.Fprintf(stderr, "backend: no .idiom/backends.json under %s\n", root)
		return backends.Profile{}, root, 1
	}
	prof, found := file.Profile(profileName)
	if !found {
		if profileName == "" {
			fmt.Fprintln(stderr, "backend: backends.json has multiple profiles; pass --profile <name>")
		} else {
			fmt.Fprintf(stderr, "backend: profile %q not found\n", profileName)
		}
		return backends.Profile{}, root, 2
	}
	return prof, root, 0
}

// runBackendStart spawns each model in the selected profile,
// waits for each port to accept connections, and prints a status
// line per model. Exit code: 0 when every backend reaches Ready,
// non-zero when any spawn or readiness check fails.
func runBackendStart(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom backend start", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		projectRoot string
		profileName string
		logDir      string
		readyTO     time.Duration
	)
	fs.StringVar(&projectRoot, "project-root", "", "project root containing .idiom/backends.json (defaults to cwd)")
	fs.StringVar(&profileName, "profile", "", "named profile to launch (omit to use the lone profile)")
	fs.StringVar(&logDir, "log-dir", "", "directory for backend logs (defaults to OS temp dir)")
	fs.DurationVar(&readyTO, "ready-timeout", 90*time.Second, "how long to wait for each backend to start accepting connections")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	prof, root, code := loadProfileFromFlags(projectRoot, profileName, stderr)
	if code != 0 {
		return code
	}
	if len(prof.Models) == 0 {
		fmt.Fprintln(stderr, "backend: profile declares no models")
		return 1
	}

	results := backendLaunch(prof, backends.LauncherOptions{
		LogDir:       logDir,
		RunDir:       runDirFor(root),
		ReadyTimeout: readyTO,
	})
	exit := 0
	for _, r := range results {
		switch {
		case r.Err != nil && !r.Ready:
			fmt.Fprintf(stdout, "FAIL %s :%d — %v\n", r.Alias, r.Port, r.Err)
			exit = 1
		case r.Err != nil && r.Ready:
			// "already running" path: report as note, not failure.
			fmt.Fprintf(stdout, "NOTE %s :%d — %v\n", r.Alias, r.Port, r.Err)
		case !r.Ready:
			fmt.Fprintf(stdout, "TIMEOUT %s :%d (pid=%d) — log=%s\n", r.Alias, r.Port, r.PID, r.LogPath)
			exit = 1
		default:
			fmt.Fprintf(stdout, "READY %s :%d (pid=%d) — log=%s\n", r.Alias, r.Port, r.PID, r.LogPath)
		}
	}
	return exit
}

// runBackendStop signals each model's listening process. Exit
// code: 0 when every backend ends up stopped (whether it was
// running or already free), non-zero on signal failures.
func runBackendStop(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom backend stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		projectRoot string
		profileName string
		grace       time.Duration
	)
	fs.StringVar(&projectRoot, "project-root", "", "project root containing .idiom/backends.json (defaults to cwd)")
	fs.StringVar(&profileName, "profile", "", "named profile to stop (omit to use the lone profile)")
	fs.DurationVar(&grace, "grace", 5*time.Second, "wait between SIGTERM and SIGKILL")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	prof, root, code := loadProfileFromFlags(projectRoot, profileName, stderr)
	if code != 0 {
		return code
	}
	results := backendStop(prof, backends.StopOptions{
		RunDir:       runDirFor(root),
		GraceTimeout: grace,
	})
	exit := 0
	for _, r := range results {
		switch {
		case r.Err != nil:
			fmt.Fprintf(stdout, "FAIL %s :%d — %v\n", r.Alias, r.Port, r.Err)
			exit = 1
		case r.PID == 0:
			fmt.Fprintf(stdout, "FREE %s :%d\n", r.Alias, r.Port)
		default:
			fmt.Fprintf(stdout, "STOPPED %s :%d (pid=%d)\n", r.Alias, r.Port, r.PID)
		}
	}
	return exit
}

// runBackendStatus prints which backends in the selected profile
// are currently listening, with their PIDs.
func runBackendStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom backend status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		projectRoot string
		profileName string
	)
	fs.StringVar(&projectRoot, "project-root", "", "project root containing .idiom/backends.json (defaults to cwd)")
	fs.StringVar(&profileName, "profile", "", "named profile to inspect (omit to use the lone profile)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	prof, root, code := loadProfileFromFlags(projectRoot, profileName, stderr)
	if code != 0 {
		return code
	}
	results := backendStatus(prof, backends.StopOptions{RunDir: runDirFor(root)})
	for _, r := range results {
		if r.Up {
			fmt.Fprintf(stdout, "UP   %s :%d (pid=%d)\n", r.Alias, r.Port, r.PID)
		} else {
			fmt.Fprintf(stdout, "DOWN %s :%d\n", r.Alias, r.Port)
		}
	}
	return 0
}

// runBackendProxy starts the alias-routing HTTP front door. The
// proxy reads each /v1/* request, picks the backend that matches
// the request's `model` field (or DefaultAlias when missing), and
// streams the response back. This is the Go port of Colony's
// mlx_alias_proxy.py and replaces it for projects on the new
// `idiom backend` plumbing.
//
// Exit codes mirror start/stop/status: 0 on clean shutdown,
// 1 on bind / serve / config errors, 2 on flag parse errors.
func runBackendProxy(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom backend proxy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		projectRoot string
		profileName string
		listenFlag  int
		defaultFlag string
	)
	fs.StringVar(&projectRoot, "project-root", "", "project root containing .idiom/backends.json (defaults to cwd)")
	fs.StringVar(&profileName, "profile", "", "named profile to serve (omit to use the lone profile)")
	fs.IntVar(&listenFlag, "listen", 0, "override profile.proxy.listen with an explicit port")
	fs.StringVar(&defaultFlag, "default", "", "override profile.proxy.default_alias with this alias")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	prof, _, code := loadProfileFromFlags(projectRoot, profileName, stderr)
	if code != 0 {
		return code
	}
	cfg, err := backends.BuildProxyConfig(prof)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if listenFlag > 0 {
		cfg.Listen = listenFlag
	}
	if strings.TrimSpace(defaultFlag) != "" {
		if _, ok := cfg.Routes[defaultFlag]; !ok {
			fmt.Fprintf(stderr, "backend proxy: --default %q is not a registered alias\n", defaultFlag)
			return 2
		}
		cfg.DefaultAlias = defaultFlag
	}
	if cfg.Listen <= 0 {
		fmt.Fprintln(stderr, "backend proxy: profile has no proxy.listen; pass --listen <port>")
		return 2
	}

	// SIGINT/SIGTERM trigger a graceful shutdown so in-flight chat
	// completions don't get cut mid-stream when the operator hits
	// Ctrl-C or systemd sends a stop. Deferring stop() avoids
	// leaking the signal handler if BuildProxyConfig later returns
	// an error before Serve starts.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ready := func(addr string) {
		fmt.Fprintf(stdout, "LISTEN %s default=%q routes=%d\n", addr, cfg.DefaultAlias, len(cfg.Routes))
	}
	if err := backendServeProxy(ctx, cfg, backends.ProxyOptions{
		LogFunc: func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) },
	}, ready); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "STOPPED proxy")
	return 0
}

// runBackendList prints the parsed contents of .idiom/backends.json
// in a stable, human-readable form. It's the read-only escape hatch
// projects use to confirm idiom sees the file they expect. Exit
// code mirrors start/stop/status: 1 when the file is missing or
// malformed (the operator usually wants the same signal across
// subcommands so shell pipelines don't have to special-case list).
func runBackendList(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom backend list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		projectRoot string
		path        string
	)
	fs.StringVar(&projectRoot, "project-root", "", "project root containing .idiom/backends.json (defaults to cwd)")
	fs.StringVar(&path, "file", "", "explicit path to a backends.json (mutually exclusive with --project-root)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(path) != "" && strings.TrimSpace(projectRoot) != "" {
		fmt.Fprintln(stderr, "backend list: pass --file OR --project-root, not both")
		return 2
	}

	var (
		file backends.File
		ok   bool
		err  error
		src  string
	)
	switch {
	case strings.TrimSpace(path) != "":
		src = path
		file, err = backends.Load(path)
		if err == nil {
			ok = true
		}
	default:
		root := projectRoot
		if strings.TrimSpace(root) == "" {
			cwd, cwdErr := os.Getwd()
			if cwdErr != nil {
				fmt.Fprintln(stderr, cwdErr)
				return 1
			}
			root = cwd
		}
		src = filepath.Join(root, ".idiom", "backends.json")
		file, ok, err = backends.LoadFromProjectRoot(root)
	}

	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintf(stderr, "backend: no backends.json at %s\n", src)
		return 1
	}

	formatBackendsList(stdout, src, file)
	return 0
}

// formatBackendsList writes a deterministic, sorted summary of the
// loaded file. Profile names sort alphabetically so the output is
// stable across runs (Go map iteration is randomized).
func formatBackendsList(stdout io.Writer, src string, file backends.File) {
	fmt.Fprintf(stdout, "%s\n", src)
	if len(file.Profiles) == 0 {
		fmt.Fprintln(stdout, "  (no profiles declared)")
		return
	}
	names := make([]string, 0, len(file.Profiles))
	for name := range file.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		prof := file.Profiles[name]
		fmt.Fprintf(stdout, "profile %q\n", name)
		if prof.Proxy.Listen != 0 || prof.Proxy.DefaultAlias != "" || prof.Proxy.DefaultCoder != "" {
			fmt.Fprintf(stdout, "  proxy listen=%d default=%q default_coder=%q\n", prof.Proxy.Listen, prof.Proxy.DefaultAlias, prof.Proxy.DefaultCoder)
		}
		if coder := prof.CoderAlias(); coder != "" {
			fmt.Fprintf(stdout, "  coder_alias=%q\n", coder)
		}
		for _, m := range prof.Models {
			fmt.Fprintf(stdout, "  model alias=%q binary=%q port=%d max_tokens=%d\n", m.Alias, m.Binary, m.Port, m.MaxTokens)
		}
	}
}

func printBackendUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: idiom backend <subcommand> [flags]")
	fmt.Fprintln(stderr, "subcommands:")
	fmt.Fprintln(stderr, "  list            print the parsed .idiom/backends.json")
	fmt.Fprintln(stderr, "                  flags: --project-root <path> | --file <path>")
	fmt.Fprintln(stderr, "  start           launch each model in the selected profile")
	fmt.Fprintln(stderr, "                  flags: --profile <name> --log-dir <path> --ready-timeout <duration>")
	fmt.Fprintln(stderr, "  stop            signal each model's listening process (SIGTERM, then SIGKILL after --grace)")
	fmt.Fprintln(stderr, "                  flags: --profile <name> --grace <duration>")
	fmt.Fprintln(stderr, "  status          show which backends in the profile are listening")
	fmt.Fprintln(stderr, "                  flags: --profile <name>")
	fmt.Fprintln(stderr, "  proxy           run the alias-routing front door (Go port of mlx_alias_proxy.py)")
	fmt.Fprintln(stderr, "                  flags: --profile <name> --listen <port> --default <alias>")
}

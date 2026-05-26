package queuecmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"idiom/internal/runner"
	"idiom/internal/telemetry"
)

func RunMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runQueueMain(args, stdout, stderr)
}

func RunMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	return runQueueMainWithTelemetry(args, stdout, stderr, rec)
}

// runQueueMain dispatches `idiom queue <subcommand>`. The subcommands
// split into three lifecycle roles: `serve` runs the daemon; `run`
// is the workhorse that any project invokes to get a slot before its
// command starts; `status`, `ping`, and `cancel` are read/manage
// surfaces for visibility and recovery.
func runQueueMain(args []string, stdout io.Writer, stderr io.Writer) int {
	return runQueueMainWithTelemetry(args, stdout, stderr, nil)
}

func runQueueMainWithTelemetry(args []string, stdout io.Writer, stderr io.Writer, rec *telemetry.Recorder) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printQueueUsage(stderr)
		return 0
	}
	logQueuePhase(rec, "queue_start", map[string]any{"subcommand": args[0]})
	switch args[0] {
	case "serve":
		return runQueueServe(args[1:], stdout, stderr)
	case "run":
		return runQueueRunWithTelemetry(args[1:], stdout, stderr, rec)
	case "status":
		return runQueueStatus(args[1:], stdout, stderr)
	case "ping":
		return runQueuePing(args[1:], stdout, stderr)
	case "cancel":
		return runQueueCancel(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "queue: unknown subcommand %q\n", args[0])
		printQueueUsage(stderr)
		return 2
	}
}

func runQueueServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom queue serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", runner.DefaultSocketPath(), "Unix socket path the daemon listens on")
	configPath := fs.String("config", runner.DefaultConfigPath(), "runner config (defaults to $IDIOM_RUNNER_CONFIG or ~/.idiom/runner.json)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	cfg, err := runner.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	srv := runner.NewServer(cfg, *socketPath)
	if err := srv.Start(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "idiom queue daemon listening on %s\n", *socketPath)
	for _, ps := range srv.Snapshot().Pools {
		fmt.Fprintf(stdout, "  pool %s: max_in_flight=%d\n", ps.Name, ps.MaxInFlight)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	fmt.Fprintln(stdout, "shutting down...")
	srv.Stop()
	return 0
}

// runQueueRun is the project-side wrapper. It enqueues, blocks for a
// slot, then execs the wrapped command, and releases the slot on
// exit (success or failure). Exit code mirrors the wrapped command,
// so this composes with shell-level error handling.
func runQueueRun(args []string, stdout, stderr io.Writer) int {
	return runQueueRunWithTelemetry(args, stdout, stderr, nil)
}

func runQueueRunWithTelemetry(args []string, stdout, stderr io.Writer, rec *telemetry.Recorder) int {
	fs := flag.NewFlagSet("idiom queue run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", runner.DefaultSocketPath(), "Unix socket path of the daemon")
	pool := fs.String("pool", "default", "pool to acquire a slot in")
	label := fs.String("label", "", "human-readable label shown in `queue status`")
	clientID := fs.String("client", defaultClientID(), "client identifier shown in `queue status` (defaults to project basename + pid)")
	quiet := fs.Bool("quiet", false, "suppress queue progress messages on stderr")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	cmdArgs := fs.Args()
	if len(cmdArgs) == 0 {
		fmt.Fprintln(stderr, "queue run: command required (use `--` to separate flags from command)")
		return 2
	}

	ctx, cancel := signalContext()
	defer cancel()

	// Reentrancy guard: if an ancestor process already holds a lease
	// in ANY pool (signaled via IDIOM_QUEUE_HELD_POOLS), skip our
	// own Acquire and exec the child directly. Same-pool nesting
	// would self-deadlock on single-cap pools (the daemon doesn't
	// know the second request is from inside the first lease's
	// lifetime — see BUG_QUEUE_DUAL_ACQUIRE_DEADLOCK.md). Cross-pool
	// nesting created a dining-philosophers deadlock when two
	// sessions each held one pool and waited for the other (observed
	// 2026-05-10: Colony held qwen-35b waiting on qwen-coder for its
	// nested plan-execute; idiom held qwen-coder waiting on qwen-35b
	// for its nested refine review). Treat any held lease as enough
	// serialization for this session and skip nested acquires
	// regardless of pool.
	var lease *runner.Lease
	resolvedPool := *pool
	heldPoolsEnv := os.Getenv(runner.HeldPoolsEnv)
	if runner.PoolHeldByAncestor(heldPoolsEnv, *pool) {
		logQueuePhase(rec, "queue_admission", map[string]any{"state": "nested_same_pool", "pool": *pool, "degraded": false})
		if !*quiet {
			fmt.Fprintf(stderr, "[idiom queue %s] ancestor holds slot in same pool; skipping nested acquire\n", *pool)
		}
	} else if strings.TrimSpace(heldPoolsEnv) != "" {
		logQueuePhase(rec, "queue_admission", map[string]any{"state": "nested_cross_pool", "pool": *pool, "degraded": true})
		if !*quiet {
			fmt.Fprintf(stderr, "[idiom queue %s] ancestor holds slot in different pool(s) (%s); skipping nested acquire to prevent cross-pool deadlock — wrap the outer command with the pool the inner inference actually uses\n", *pool, heldPoolsEnv)
		}
	} else {
		startedAt := time.Now()
		l, err := runner.Acquire(ctx, *socketPath, *pool, *label, *clientID)
		if err != nil {
			logQueuePhase(rec, "queue_admission", map[string]any{"state": "failed", "pool": *pool, "wait_ms": time.Since(startedAt).Milliseconds(), "reason_class": queueReasonClass(err.Error()), "degraded": true})
			if errors.Is(err, runner.ErrDaemonUnreachable) {
				fmt.Fprintf(stderr, "queue run: %v\nstart the daemon with `idiom queue serve` (or set IDIOM_RUNNER_SOCKET).\n", err)
			} else {
				fmt.Fprintf(stderr, "queue run: %v\n", err)
			}
			return 1
		}
		lease = l
		resolvedPool = lease.Pool
		defer lease.Close()

		if !*quiet {
			go func() {
				for pos := range lease.Updates() {
					fmt.Fprintf(stderr, "[idiom queue %s] waiting, position %d\n", lease.Pool, pos)
				}
			}()
		}

		select {
		case <-lease.Granted():
			logQueuePhase(rec, "queue_admission", map[string]any{"state": "granted", "pool": lease.Pool, "wait_ms": time.Since(startedAt).Milliseconds(), "degraded": false})
		case err := <-lease.Err():
			logQueuePhase(rec, "queue_admission", map[string]any{"state": "failed_before_grant", "pool": resolvedPool, "wait_ms": time.Since(startedAt).Milliseconds(), "reason_class": queueReasonClass(err.Error()), "degraded": true})
			fmt.Fprintf(stderr, "queue run: lease error before grant: %v\n", err)
			return 1
		case <-ctx.Done():
			logQueuePhase(rec, "queue_admission", map[string]any{"state": "canceled_before_grant", "pool": resolvedPool, "wait_ms": time.Since(startedAt).Milliseconds(), "reason_class": "timeout_or_canceled", "degraded": true})
			fmt.Fprintln(stderr, "queue run: canceled before grant")
			return 130
		}

		if !*quiet {
			fmt.Fprintf(stderr, "[idiom queue %s] slot granted (job %s) — running\n", lease.Pool, lease.JobID)
		}
	}

	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Lease propagation: tell descendant idiom processes that this
	// pool is already held by an ancestor. Any nested
	// `runner.MaybeAcquire` call for the same pool short-circuits to
	// a no-op release. Multiple held pools are colon-separated;
	// descendants append rather than replace, so chained
	// `idiom queue run` wrappers remain composable.
	heldPools := strings.TrimSpace(os.Getenv(runner.HeldPoolsEnv))
	if heldPools == "" {
		heldPools = resolvedPool
	} else if !runner.PoolHeldByAncestor(heldPools, resolvedPool) {
		heldPools = heldPools + ":" + resolvedPool
	}
	cmd.Env = append(os.Environ(), runner.HeldPoolsEnv+"="+heldPools)

	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "queue run: %v\n", err)
		return 1
	}
	return 0
}

func runQueueStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom queue status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", runner.DefaultSocketPath(), "Unix socket path of the daemon")
	jsonOut := fs.Bool("json", false, "emit the status payload as JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	snap, err := runner.Status(*socketPath)
	if err != nil {
		if errors.Is(err, runner.ErrDaemonUnreachable) {
			fmt.Fprintf(stderr, "queue status: %v\nstart the daemon with `idiom queue serve`.\n", err)
		} else {
			fmt.Fprintf(stderr, "queue status: %v\n", err)
		}
		return 1
	}
	if *jsonOut {
		return emitStatusJSON(stdout, snap)
	}
	emitStatusTable(stdout, snap)
	return 0
}

func runQueuePing(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom queue ping", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", runner.DefaultSocketPath(), "Unix socket path of the daemon")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	uptime, err := runner.Ping(*socketPath)
	if err != nil {
		fmt.Fprintf(stderr, "queue ping: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "ok (uptime %s)\n", uptime.Round(time.Second))
	return 0
}

func runQueueCancel(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom queue cancel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socketPath := fs.String("socket", runner.DefaultSocketPath(), "Unix socket path of the daemon")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "queue cancel: exactly one job_id required")
		return 2
	}
	if err := runner.Cancel(*socketPath, fs.Arg(0)); err != nil {
		fmt.Fprintf(stderr, "queue cancel: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cancelled %s\n", fs.Arg(0))
	return 0
}

func emitStatusJSON(w io.Writer, snap runner.StatusPayload) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		fmt.Fprintln(w, err)
		return 1
	}
	return 0
}

func emitStatusTable(w io.Writer, snap runner.StatusPayload) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "POOL\tIN-FLIGHT\tWAITING\tCAP")
	for _, p := range snap.Pools {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\n", p.Name, p.InFlight, p.Waiting, p.MaxInFlight)
	}
	tw.Flush()

	if len(snap.InFlight) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "IN FLIGHT")
		jw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(jw, "POOL\tJOB\tCLIENT\tLABEL\tSTARTED\tAGE")
		for _, j := range snap.InFlight {
			fmt.Fprintf(jw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				j.Pool, j.ID, orDash(j.Client), orDash(j.Label),
				orDash(j.StartedAt), formatJobAge(j.AgeSeconds, j.Idle))
		}
		jw.Flush()
	}
	if len(snap.Waiting) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "WAITING")
		jw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(jw, "POOL\tPOS\tJOB\tCLIENT\tLABEL\tSUBMITTED")
		for _, j := range snap.Waiting {
			fmt.Fprintf(jw, "%s\t%d\t%s\t%s\t%s\t%s\n", j.Pool, j.Position, j.ID, orDash(j.Client), orDash(j.Label), orDash(j.SubmittedAt))
		}
		jw.Flush()
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// formatJobAge renders an in-flight job's age in a compact form,
// appending IDLE when the daemon flagged it past the pool's idle
// threshold. Zero ages (status arrived faster than 1s resolution)
// render as "0s" rather than dash so the column stays comparable.
func formatJobAge(seconds int, idle bool) string {
	if seconds < 0 {
		seconds = 0
	}
	var s string
	switch {
	case seconds >= 3600:
		s = fmt.Sprintf("%dh%dm", seconds/3600, (seconds%3600)/60)
	case seconds >= 60:
		s = fmt.Sprintf("%dm%ds", seconds/60, seconds%60)
	default:
		s = fmt.Sprintf("%ds", seconds)
	}
	if idle {
		s += " IDLE"
	}
	return s
}

func defaultClientID() string {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Sprintf("pid-%d", os.Getpid())
	}
	base := wd
	if i := strings.LastIndex(wd, string(os.PathSeparator)); i >= 0 && i+1 < len(wd) {
		base = wd[i+1:]
	}
	return fmt.Sprintf("%s/%d", base, os.Getpid())
}

// signalContext returns a context that's canceled when the user
// sends SIGINT or SIGTERM. We install our own handler instead of
// using signal.NotifyContext so the second signal force-exits — a
// stuck wrapped command shouldn't trap the user.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
			// Second signal → exit immediately.
			<-sigCh
			os.Exit(130)
		case <-ctx.Done():
			signal.Stop(sigCh)
		}
	}()
	return ctx, cancel
}

func printQueueUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: idiom queue <subcommand> [flags]")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "subcommands:")
	fmt.Fprintln(stderr, "  serve              run the local job-queue daemon (foreground)")
	fmt.Fprintln(stderr, "                     flags: --socket <path>  --config <path>")
	fmt.Fprintln(stderr, "  run -- <cmd>       acquire a slot, run <cmd>, release on exit")
	fmt.Fprintln(stderr, "                     flags: --pool <name>  --label <text>  --client <id>  --quiet  --socket <path>")
	fmt.Fprintln(stderr, "  status             print queue snapshot")
	fmt.Fprintln(stderr, "                     flags: --json  --socket <path>")
	fmt.Fprintln(stderr, "  ping               check daemon liveness and uptime")
	fmt.Fprintln(stderr, "                     flags: --socket <path>")
	fmt.Fprintln(stderr, "  cancel <job_id>    remove a waiting job from the queue")
	fmt.Fprintln(stderr, "                     flags: --socket <path>")
	fmt.Fprintln(stderr)
	fmt.Fprintln(stderr, "Defaults: socket from $IDIOM_RUNNER_SOCKET, $XDG_RUNTIME_DIR/idiom-runner.sock,")
	fmt.Fprintln(stderr, "          or $TMPDIR/idiom-runner.sock; config from $IDIOM_RUNNER_CONFIG or")
	fmt.Fprintln(stderr, "          ~/.idiom/runner.json (single 'default' pool, max_in_flight=1 if absent).")
}

func logQueuePhase(rec *telemetry.Recorder, eventType string, fields map[string]any) {
	if rec == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	_ = rec.Log(eventType, fields)
}

func queueReasonClass(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(reason, "unreachable"), strings.Contains(reason, "connection refused"), strings.Contains(reason, "no such file"):
		return "daemon_unreachable"
	case strings.Contains(reason, "timeout"), strings.Contains(reason, "deadline"), strings.Contains(reason, "canceled"):
		return "timeout_or_canceled"
	case strings.Contains(reason, "unknown pool"):
		return "unknown_pool"
	case strings.Contains(reason, "held"):
		return "nested_lease"
	case reason == "":
		return "unknown"
	default:
		return "queue_error"
	}
}

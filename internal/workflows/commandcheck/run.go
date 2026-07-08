package commandcheck

import (
	"context"
	"strconv"
	"strings"
	"time"

	"idiom/internal/platform/commandexec"
)

type FailureKind string

const (
	FailureNone           FailureKind = ""
	FailureUnsupported    FailureKind = "unsupported"
	FailureMissingOperand FailureKind = "missing_operand"
	FailureTimeout        FailureKind = "timeout"
	FailureExit           FailureKind = "exit"
	FailureSpawn          FailureKind = "spawn"
)

type TruncationStyle string

const (
	TruncatePlanExecute TruncationStyle = "plan_execute"
	TruncatePostflight  TruncationStyle = "postflight"
)

const DiagnosticCaptureBytes = 1024 * 1024

type Request struct {
	Argv                       []string
	ShellCommand, Shell, Dir   string
	Env                        []string
	Timeout                    time.Duration
	AllowedArgv                func([]string) bool
	PrefixArgv                 func([]string) []string
	RequireOperandAfterPrefix  bool
	CaptureBytes, DisplayBytes int
	Truncation                 TruncationStyle
	MergeOutput                bool
}

type Result struct {
	StartedAt, CompletedAt time.Time
	Duration               time.Duration
	ExitCode               int
	Failure                FailureKind
	Err                    error
	Stdout, Stderr, Output string
	DisplayOutput          string
}

func Run(parent context.Context, req Request) (result Result) {
	if parent == nil {
		parent = context.Background()
	}

	result.StartedAt = time.Now().UTC()
	result.ExitCode = -1
	defer func() {
		result.CompletedAt = time.Now().UTC()
		if result.Duration == 0 {
			result.Duration = result.CompletedAt.Sub(result.StartedAt)
		}
	}()

	if len(req.Argv) > 0 || req.AllowedArgv != nil || req.PrefixArgv != nil || req.RequireOperandAfterPrefix {
		if len(req.Argv) == 0 || (req.AllowedArgv != nil && !req.AllowedArgv(req.Argv)) {
			result.Failure = FailureUnsupported
			return result
		}
		if req.RequireOperandAfterPrefix && req.PrefixArgv != nil && len(req.Argv) == len(req.PrefixArgv(req.Argv)) {
			result.Failure = FailureMissingOperand
			return result
		}
	}

	spec := commandexec.Spec{
		Dir:            req.Dir,
		Env:            append([]string(nil), req.Env...),
		Timeout:        req.Timeout,
		MaxOutputBytes: req.CaptureBytes,
		MergeOutput:    req.MergeOutput,
	}
	if len(req.Argv) > 0 {
		spec.Mode = commandexec.ModeArgv
		spec.Name = req.Argv[0]
		spec.Args = append([]string(nil), req.Argv[1:]...)
	} else {
		spec.Mode = commandexec.ModePlainShell
		spec.Shell = req.Shell
		spec.Command = req.ShellCommand
	}

	run := commandexec.Run(parent, spec)
	result.Duration = run.Duration
	result.Stdout = run.Stdout
	result.Stderr = run.Stderr
	result.Output = commandexec.CombineOutput(run.Stdout, run.Stderr)
	result.DisplayOutput = TrimDisplay(result.Output, req.DisplayBytes, req.Truncation)

	if run.Err == nil {
		result.ExitCode = 0
		return result
	}

	result.Err = run.Err
	switch {
	case run.TimedOut:
		result.Failure = FailureTimeout
	case run.ExitError:
		result.Failure = FailureExit
		result.ExitCode = run.ExitCode
	default:
		result.Failure = FailureSpawn
	}

	return result
}

func TrimDisplay(text string, maxBytes int, style TruncationStyle) string {
	text = strings.TrimSpace(text)
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}

	switch style {
	case TruncatePlanExecute:
		for i := range text {
			if i >= maxBytes {
				return text[:i] + "\n\n[truncated " + strconv.Itoa(len(text)-i) + " bytes]\n"
			}
		}

		return text
	case TruncatePostflight:
		return text[:maxBytes] + "... (truncated; rerun the check for full output)"
	default:
		return text[:maxBytes]
	}
}

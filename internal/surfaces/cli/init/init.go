package init

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	canonicalprompts "idiom/prompts"
)

// initSeedCodingProfile / initSeedClaudeQwenWorkerProfile are the
// canonical chat-profile templates `idiom init` seeds under
// .idiom/prompts/profiles/.
var initSeedCodingProfile = canonicalprompts.MustProfile("coding.md")

var initSeedClaudeQwenWorkerProfile = canonicalprompts.MustProfile("claude-qwen-worker.md")

var initSeedBasePrompt = canonicalprompts.BasePrompt()

// runInitMain creates the .idiom/ skeleton in the target directory, or the
// user-global ~/.idiom/ skeleton with --global. With --merge, only files that
// don't already exist are written; the root being present is not itself a fatal
// condition. Without --merge, an existing root is treated as "already
// initialized" and the operator must either re-run with --merge or remove it
// first.
//
// Exit codes: 0 success, 1 IO failure, 2 flag/usage error.
func RunMain(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom init", flag.ContinueOnError)
	fs.SetOutput(stderr)

	merge := fs.Bool("merge", false, "add only missing files; do not overwrite existing ones (use to upgrade an existing .idiom/ layout)")
	global := fs.Bool("global", false, "initialize the user-global ~/.idiom/ config directory instead of a project .idiom/ directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	tail := fs.Args()
	target := "."

	if len(tail) == 1 {
		if *global {
			fmt.Fprintln(stderr, "init: --global does not accept a target directory")

			return 2
		}

		target = tail[0]
	} else if len(tail) > 1 {
		fmt.Fprintln(stderr, "init: too many positional arguments (expected at most one directory)")

		return 2
	}

	if *global {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			fmt.Fprintf(stderr, "init: resolve home directory: %v\n", err)

			return 1
		}

		target = filepath.Join(home, ".idiom")
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(stderr, "init: resolve target: %v\n", err)

		return 1
	}

	if *global {
		if err := os.MkdirAll(abs, 0o755); err != nil {
			fmt.Fprintf(stderr, "init: mkdir %q: %v\n", abs, err)

			return 1
		}
	}

	info, err := os.Stat(abs)
	if err != nil {
		fmt.Fprintf(stderr, "init: target directory does not exist: %s\n", abs)

		return 2
	}

	if !info.IsDir() {
		fmt.Fprintf(stderr, "init: target %q is not a directory\n", abs)

		return 2
	}

	idiomDir := filepath.Join(abs, ".idiom")
	if *global {
		idiomDir = abs
	}

	if _, err := os.Stat(idiomDir); err == nil && !*merge {
		fmt.Fprintf(stderr, "init: .idiom/ already exists at %s (use --merge to add only missing files)\n", idiomDir)

		return 2
	}

	for _, f := range initSeedFiles {
		path := filepath.Join(idiomDir, f.relPath)
		if *merge {
			if _, err := os.Stat(path); err == nil {
				fmt.Fprintf(stdout, "(skipped existing %s)\n", path)

				continue
			}
		}

		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintf(stderr, "init: mkdir %q: %v\n", filepath.Dir(path), err)

			return 1
		}

		if err := os.WriteFile(path, []byte(f.body), 0o644); err != nil {
			fmt.Fprintf(stderr, "init: write %q: %v\n", path, err)

			return 1
		}

		fmt.Fprintf(stdout, "created %s\n", path)
	}

	if !*merge {
		fmt.Fprintf(stdout, "Initialized .idiom/ at %s\n", idiomDir)
	}

	return 0
}

// initSeedFile pairs a relative path under .idiom/ with the body
// runInitMain writes there. Centralizing the table keeps the
// runner small and makes the seeded layout obvious at a glance.
type initSeedFile struct {
	relPath string
	body    string
}

var initSeedFiles = []initSeedFile{
	{relPath: "config.json", body: initSeedConfigJSON},
	{relPath: "backends.json", body: initSeedBackendsJSON},
	{relPath: "external_tools/README.md", body: initSeedExternalToolsReadme},
	{relPath: "postflight/checks/README.md", body: initSeedPostflightChecksReadme},
	{relPath: "prompts/base.md", body: initSeedBasePrompt},
	{relPath: "prompts/lanes/README.md", body: initSeedPromptsLanesReadme},
	{relPath: "prompts/rules/README.md", body: initSeedPromptsRulesReadme},
	{relPath: "prompts/profiles/coding.md", body: initSeedCodingProfile},
	{relPath: "prompts/profiles/claude-qwen-worker.md", body: initSeedClaudeQwenWorkerProfile},
}

const initSeedConfigJSON = `{
  "_comment": "Idiom config. The hosted default needs DEEPSEEK_API_KEY. For local use, edit llama_server to point at a real local model and keep private paths out of version control.",
  "app_name": "Idiom",
  "llama_server": {
    "kind": "deepseek",
    "model": "deepseek-v4-flash",
    "api_key_env": "DEEPSEEK_API_KEY",
    "reasoning_effort": "high"
  },
  "chat": {
    "auto_start_server": false,
    "save_dir": "transcripts",
    "debug_log_dir": "logs/conversations",
    "debug_log_mode": "redacted",
    "system_prompt_files": ["prompts/base.md"],
    "profiles_dir": "prompts/profiles",
    "default_profiles": ["coding"],
    "project_root": "",
    "project_prompt_files": []
  },
  "route_classifier": {
    "enabled": true,
    "kind": "deepseek",
    "model": "deepseek-v4-flash",
    "api_key_env": "DEEPSEEK_API_KEY",
    "reasoning_effort": "high",
    "always_on": true,
    "max_tokens": 512,
    "timeout_seconds": 20,
    "auto_start_server": false
  },
  "workspace": {
    "root": "",
    "cwd": "",
    "command_timeout_seconds": 180,
    "max_output_bytes": 262144,
    "max_file_bytes": 262144,
    "max_directory_entries": 400,
    "enable_shell": true,
    "enable_writes": true,
    "allow_private_network": false
  }
}
`

const initSeedBackendsJSON = `{
  "_comment": "Portable backend examples. The default hosted profile needs DEEPSEEK_API_KEY. The Anthropic example needs ANTHROPIC_API_KEY. The local example uses a placeholder model path under ignored models/; replace it with your local model.",
  "default_profile": "deepseek-v4-flash",
  "profiles": {
    "deepseek-v4-flash": {
      "models": [
        {
          "alias": "deepseek-v4-flash",
          "kind": "deepseek",
          "model": "deepseek-v4-flash",
          "api_key_env": "DEEPSEEK_API_KEY",
          "reasoning_effort": "high",
          "roles": ["planner", "executor", "classifier"]
        }
      ],
      "proxy": {
        "default_alias": "deepseek-v4-flash",
        "default_planner": "deepseek-v4-flash",
        "default_executor": "deepseek-v4-flash",
        "default_coder": "deepseek-v4-flash",
        "default_classifier": "deepseek-v4-flash"
      }
    },
    "claude-sonnet-example": {
      "models": [
        {
          "alias": "claude-sonnet",
          "kind": "anthropic",
          "model": "claude-sonnet-4-6",
          "api_key_env": "ANTHROPIC_API_KEY",
          "roles": ["planner", "executor", "classifier"]
        }
      ],
      "proxy": {
        "default_alias": "claude-sonnet",
        "default_planner": "claude-sonnet",
        "default_executor": "claude-sonnet",
        "default_coder": "claude-sonnet",
        "default_classifier": "claude-sonnet"
      }
    },
    "local-llama-example": {
      "models": [
        {
          "alias": "local-coder",
          "binary": "llama-server",
          "model_path": "models/local-model.gguf",
          "host": "127.0.0.1",
          "port": 47780,
          "kind": "llama",
          "max_tokens": 8192,
          "roles": ["planner", "executor", "classifier"]
        }
      ],
      "proxy": {
        "listen": 47680,
        "default_alias": "local-coder",
        "default_planner": "local-coder",
        "default_executor": "local-coder",
        "default_coder": "local-coder",
        "default_classifier": "local-coder"
      }
    }
  }
}
`

const initSeedExternalToolsReadme = `# Project-specific external tools

Drop one JSON file per project tool here. Each file declares
` + "`command`" + `, ` + "`args`" + `, and an ` + "`input_schema`" + ` describing the shape
idiom passes the model. Set ` + "`workspace.external_tools_dir`" + `
in ` + "`.idiom/config.json`" + ` to enable directory scanning.

Set both ` + "`read_only: true`" + ` and ` + "`discovery: true`" + ` for project tools
that only return evidence. Scoped implementation lanes keep those tools
available for gap-driven lookup/trace work while opaque external tools stay
hidden behind the write-scope contract.

See the idiom source for the full schema (internal/platform/config/config.go's
` + "`ExternalToolDef`" + ` type and internal/capabilities/workspace/manager.go's
` + "`ExternalTool`" + ` consumer).
`

const initSeedPostflightChecksReadme = `# Project-specific postflight checks

Drop one JSON file per check here. Each file declares
` + "`name`" + `, ` + "`kind`" + ` (` + "`shell`" + ` or ` + "`ast_grep`" + `), and the runner-
specific fields. ` + "`idiom lane postflight`" + ` and
` + "`idiom lane <mode> --postflight`" + ` load these alongside the
canonical generic checks.

See internal/workflows/postflight/ for the supported check shapes.
`

const initSeedPromptsLanesReadme = `# Project lane preamble overrides

Files at ` + "`<mode>/preamble.md`" + ` and ` + "`<mode>/postamble.md`" + `
extend (concatenate with) the canonical lane preambles idiom embeds.
Used to layer project-specific guidance onto the canonical lane
shape without forking idiom.

Modes today: ` + "`plan-execute`" + `, ` + "`implement`" + `.
`

const initSeedPromptsRulesReadme = `# Project rule overlays

Files here extend or replace canonical generic rules embedded in
idiom. Same-name files (e.g. ` + "`loop-protocol.md`" + `) replace the
canonical entry; new names append after the canonical block as
project-specific extras.

Canonical rules today: ` + "`loop-protocol.md`" + `,
` + "`brief-skeleton.md`" + `, ` + "`known-traps-generic.md`" + `.
`

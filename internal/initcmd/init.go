package initcmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"idiom/internal/cliassets"
)

// initSeedCodingProfile / initSeedClaudeQwenWorkerProfile are the
// canonical chat-profile templates `idiom init` seeds under
// .idiom/prompts/profiles/. Embedded assets live in internal/cliassets;
// the canonical reference copies live at prompts/profiles/<name>.md and
// tests keep both locations in sync.
var initSeedCodingProfile = cliassets.MustProfile("coding.md")

var initSeedClaudeQwenWorkerProfile = cliassets.MustProfile("claude-qwen-worker.md")

// runInitMain creates the .idiom/ skeleton in the target directory
// with template config files and README placeholders. Phase 7.1 of
// the reusability migration. With --merge, only files that don't
// already exist are written; the .idiom/ root being present is not
// itself a fatal condition. Without --merge, an existing .idiom/
// dir is treated as "already initialized" and the operator must
// either re-run with --merge or rm -rf .idiom/ first.
//
// Exit codes: 0 success, 1 IO failure, 2 flag/usage error.
func RunMain(args []string, stdout io.Writer, stderr io.Writer) int {
	fs := flag.NewFlagSet("idiom init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	merge := fs.Bool("merge", false, "add only missing files; do not overwrite existing ones (use to upgrade an existing .idiom/ layout)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	tail := fs.Args()
	target := "."
	if len(tail) == 1 {
		target = tail[0]
	} else if len(tail) > 1 {
		fmt.Fprintln(stderr, "init: too many positional arguments (expected at most one directory)")
		return 2
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(stderr, "init: resolve target: %v\n", err)
		return 1
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
	{relPath: "prompts/lanes/README.md", body: initSeedPromptsLanesReadme},
	{relPath: "prompts/rules/README.md", body: initSeedPromptsRulesReadme},
	{relPath: "prompts/profiles/coding.md", body: initSeedCodingProfile},
	{relPath: "prompts/profiles/claude-qwen-worker.md", body: initSeedClaudeQwenWorkerProfile},
}

const initSeedConfigJSON = `{
  "_comment": "Idiom project config. Edit endpoint, workspace.root, and prompts to match this project. See cmd/idiom/main.go for full schema.",
  "app_name": "Project Name Here",
  "llama_server": {
    "kind": "mlx",
    "host": "127.0.0.1",
    "port": 47680,
    "model": "qwen3.6-35b"
  },
  "chat": {
    "auto_start_server": false,
    "save_dir": ".idiom/transcripts",
    "system_prompt_files": [".idiom/prompts/base.md"]
  },
  "workspace": {
    "root": "",
    "cwd": "",
    "shell": "/bin/bash",
    "command_timeout_seconds": 60,
    "max_output_bytes": 65536,
    "enable_writes": true
  }
}
`

const initSeedBackendsJSON = `{
  "_comment": "Local LLM backend declarations. Uncomment and edit a profile for the models you have on disk; idiom backend start --profile <name> launches them.",
  "default_profile": "qwen-pair-example",
  "profiles": {
    "qwen-pair-example": {
      "models": [
        {
          "alias": "qwen3.6-35b",
          "binary": "/path/to/mlx_lm.server",
          "model_path": "/path/to/qwen3.6-35b",
          "port": 47780
        }
      ],
      "proxy": {
        "listen": 47680,
        "default_alias": "qwen3.6-35b"
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

See the idiom source for the full schema (internal/config/config.go's
` + "`ExternalToolDef`" + ` type and internal/workspace/manager.go's
` + "`ExternalTool`" + ` consumer).
`

const initSeedPostflightChecksReadme = `# Project-specific postflight checks

Drop one JSON file per check here. Each file declares
` + "`name`" + `, ` + "`kind`" + ` (` + "`shell`" + ` or ` + "`ast_grep`" + `), and the runner-
specific fields. ` + "`idiom lane postflight`" + ` and
` + "`idiom lane <mode> --postflight`" + ` load these alongside the
canonical generic checks.

See internal/postflight/ for the supported check shapes.
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

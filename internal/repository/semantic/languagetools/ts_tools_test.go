package languagetools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"idiom/internal/capabilities/workspace"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TypeScript workspace tools", func() {
	It("provides syntax-index discovery and navigation", func() {
		root := buildTypeScriptWorkspace()
		manager := newLanguageWorkspaceManager(root, 30)
		Expect(languageDetects(root, "typescript")).To(BeTrue())

		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_overview"}), "TypeScript/JavaScript files")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_find_symbol", Pattern: "greet"}), "greeter.ts")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_find_symbol", Pattern: "export interface Greeter"}), "Greeter")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_related_files", Pattern: "greeter"}), "greeter.ts")
		outline := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_file_outline", Path: filepath.Join("src", "greeter.ts")})
		expectToolSuccess(outline, "file: src/greeter.ts", "greet", "Shouter")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_read_symbol", Pattern: "Shouter.shout"}), "toUpperCase")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_read_symbol", Pattern: "export class Shouter"}), "export class Shouter")

		queries := []struct{ tool, pattern, output string }{
			{"ts_find_symbol", "export default class DefaultGreeter", "default_class.ts"},
			{"ts_find_symbol", "export default function defaultGreet", "default_function.ts"},
			{"ts_read_symbol", "export default class DefaultGreeter", "export default class DefaultGreeter"},
			{"ts_read_symbol", "export default function defaultGreet", "export default function defaultGreet"},
			{"ts_find_symbol", "export function* greetNames", "greetNames"},
			{"ts_read_symbol", "export async function* streamNames", "export async function* streamNames"},
			{"ts_find_symbol", "export declare class ExternalClient", "external.d.ts"},
			{"ts_read_symbol", "export declare class ExternalClient", "export declare class ExternalClient"},
			{"ts_read_symbol", "export declare const defaultEndpoint", "export declare const defaultEndpoint"},
			{"ts_find_symbol", "export enum GreetMode", "GreetMode"},
			{"ts_read_symbol", "export namespace GreeterTools", "export namespace GreeterTools"},
		}
		for _, query := range queries {
			expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{
				Tool: query.tool, Pattern: query.pattern,
			}), query.output)
		}

		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_entrypoints"}), "index.ts")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_calls_from", Pattern: "main"}), "greet")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_find_symbol", Pattern: "Panel"}), "util.jsx")
	})

	It("uses typescript-language-server for precise operations when available", func() {
		if _, err := exec.LookPath("typescript-language-server"); err != nil {
			Skip("typescript-language-server not installed")
		}
		manager := newLanguageWorkspaceManager(buildTypeScriptWorkspace(), 60)
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_workspace_symbol", Pattern: "Shouter"}), "greeter.ts")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "ts_definition", Pattern: "greet"}), "greeter.ts")
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: "ts_references", Pattern: "greet", Path: ".",
		}).Error).To(BeEmpty())
	})
})

func buildTypeScriptWorkspace() string {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	writeTsTestFixture(filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020","module":"commonjs","strict":true}}`)
	writeTsTestFixture(filepath.Join(root, "src", "greeter.ts"), `
export interface Greeter { name: string }

export function greet(name: string): void {
  console.log(name);
}

export function* greetNames(): Iterable<string> {
  yield "world";
}

export async function* streamNames(): AsyncIterable<string> {
  yield "world";
}

export class Shouter {
  shout(name: string): void {
    console.log(name.toUpperCase());
  }
}

export enum GreetMode {
  Loud,
  Quiet,
}

export namespace GreeterTools {
  export function label(name: string): string {
    return name;
  }
}
`)
	writeTsTestFixture(filepath.Join(root, "src", "index.ts"), `
import { greet, Shouter } from "./greeter";

export function main(): void {
  greet("world");
  const shouter = new Shouter();
  shouter.shout("hi");
}
`)
	writeTsTestFixture(filepath.Join(root, "src", "util.jsx"), `export function Panel(props) { return null; }`)
	writeTsTestFixture(filepath.Join(root, "src", "default_class.ts"), `
export default class DefaultGreeter {
  greet(name: string): void {
    console.log(name);
  }
}`)
	writeTsTestFixture(filepath.Join(root, "src", "default_function.ts"), `
export default function defaultGreet(name: string): void {
  console.log(name.toUpperCase());
}`)
	writeTsTestFixture(filepath.Join(root, "types", "external.d.ts"), `
export declare class ExternalClient {
  connect(): void;
}

export declare const defaultEndpoint: string;`)
	return root
}

func writeTsTestFixture(path string, content string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644)).To(Succeed())
}

package languagetools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/platform/config"
)

func TestTsWorkspaceTools(t *testing.T) {
	root := t.TempDir()
	writeTsTestFixture(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020","module":"commonjs","strict":true}}`)
	writeTsTestFixture(t, filepath.Join(root, "src", "greeter.ts"), `
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
	writeTsTestFixture(t, filepath.Join(root, "src", "index.ts"), `
import { greet, Shouter } from "./greeter";

export function main(): void {
  greet("world");
  const shouter = new Shouter();
  shouter.shout("hi");
}
`)
	writeTsTestFixture(t, filepath.Join(root, "src", "util.jsx"), `
export function Panel(props) { return null; }
`)
	writeTsTestFixture(t, filepath.Join(root, "src", "default_class.ts"), `
export default class DefaultGreeter {
  greet(name: string): void {
    console.log(name);
  }
}
`)
	writeTsTestFixture(t, filepath.Join(root, "src", "default_function.ts"), `
export default function defaultGreet(name: string): void {
  console.log(name.toUpperCase());
}
`)
	writeTsTestFixture(t, filepath.Join(root, "types", "external.d.ts"), `
export declare class ExternalClient {
  connect(): void;
}

export declare const defaultEndpoint: string;
`)

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		EnableShell:           true,
		EnableWrites:          true,
		CommandTimeoutSeconds: 30,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   50,
	})

	if !languageDetects(root, "typescript") {
		t.Fatal("expected temp fixture to count as TypeScript workspace")
	}
	overview := executeTool(mgr, "ts_overview", ToolRequest{})
	if overview.Error != "" || !strings.Contains(overview.Output, "TypeScript/JavaScript files") {
		t.Fatalf("unexpected tsOverview result: %#v", overview)
	}
	find := executeTool(mgr, "ts_find_symbol", ToolRequest{Tool: "ts_find_symbol", Pattern: "greet"})
	if find.Error != "" || !strings.Contains(find.Output, "greeter.ts") {
		t.Fatalf("unexpected tsFindSymbol result: %#v", find)
	}
	findDecl := executeTool(mgr, "ts_find_symbol", ToolRequest{Tool: "ts_find_symbol", Pattern: "export interface Greeter"})
	if findDecl.Error != "" || !strings.Contains(findDecl.Output, "Greeter") {
		t.Fatalf("expected declaration-shaped tsFindSymbol query to find Greeter, got %#v", findDecl)
	}
	related := executeTool(mgr, "ts_related_files", ToolRequest{Tool: "ts_related_files", Pattern: "greeter"})
	if related.Error != "" || !strings.Contains(related.Output, "greeter.ts") {
		t.Fatalf("unexpected tsRelatedFiles result: %#v", related)
	}
	outline := executeTool(mgr, "ts_file_outline", ToolRequest{Tool: "ts_file_outline", Path: filepath.Join("src", "greeter.ts")})
	if outline.Error != "" {
		t.Fatalf("unexpected tsFileOutline error: %#v", outline)
	}
	if !strings.Contains(outline.Output, "file: src/greeter.ts") {
		t.Fatalf("tsFileOutline missing file header: %#v", outline)
	}
	if !strings.Contains(outline.Output, "greet") || !strings.Contains(outline.Output, "Shouter") {
		t.Fatalf("tsFileOutline missing symbols: %#v", outline)
	}
	read := executeTool(mgr, "ts_read_symbol", ToolRequest{Tool: "ts_read_symbol", Pattern: "Shouter.shout"})
	if read.Error != "" || !strings.Contains(read.Output, "toUpperCase") {
		t.Fatalf("unexpected tsReadSymbol result: %#v", read)
	}
	readDecl := executeTool(mgr, "ts_read_symbol", ToolRequest{Tool: "ts_read_symbol", Pattern: "export class Shouter"})
	if readDecl.Error != "" || !strings.Contains(readDecl.Output, "export class Shouter") {
		t.Fatalf("expected declaration-shaped tsReadSymbol to return Shouter class, got %#v", readDecl)
	}
	// Default-export declaration-shaped queries.
	findDefaultClass := executeTool(mgr, "ts_find_symbol", ToolRequest{Tool: "ts_find_symbol", Pattern: "export default class DefaultGreeter"})
	if findDefaultClass.Error != "" || !strings.Contains(findDefaultClass.Output, "default_class.ts") {
		t.Fatalf("expected declaration-shaped tsFindSymbol for 'export default class DefaultGreeter', got %#v", findDefaultClass)
	}
	findDefaultFunc := executeTool(mgr, "ts_find_symbol", ToolRequest{Tool: "ts_find_symbol", Pattern: "export default function defaultGreet"})
	if findDefaultFunc.Error != "" || !strings.Contains(findDefaultFunc.Output, "default_function.ts") {
		t.Fatalf("expected declaration-shaped tsFindSymbol for 'export default function defaultGreet', got %#v", findDefaultFunc)
	}
	readDefaultClass := executeTool(mgr, "ts_read_symbol", ToolRequest{Tool: "ts_read_symbol", Pattern: "export default class DefaultGreeter"})
	if readDefaultClass.Error != "" || !strings.Contains(readDefaultClass.Output, "export default class DefaultGreeter") {
		t.Fatalf("expected declaration-shaped tsReadSymbol for 'export default class DefaultGreeter', got %#v", readDefaultClass)
	}
	readDefaultFunc := executeTool(mgr, "ts_read_symbol", ToolRequest{Tool: "ts_read_symbol", Pattern: "export default function defaultGreet"})
	if readDefaultFunc.Error != "" || !strings.Contains(readDefaultFunc.Output, "export default function defaultGreet") {
		t.Fatalf("expected declaration-shaped tsReadSymbol for 'export default function defaultGreet', got %#v", readDefaultFunc)
	}
	findGenerator := executeTool(mgr, "ts_find_symbol", ToolRequest{Tool: "ts_find_symbol", Pattern: "export function* greetNames"})
	if findGenerator.Error != "" || !strings.Contains(findGenerator.Output, "greetNames") {
		t.Fatalf("expected declaration-shaped tsFindSymbol for 'export function* greetNames', got %#v", findGenerator)
	}
	readAsyncGenerator := executeTool(mgr, "ts_read_symbol", ToolRequest{Tool: "ts_read_symbol", Pattern: "export async function* streamNames"})
	if readAsyncGenerator.Error != "" || !strings.Contains(readAsyncGenerator.Output, "export async function* streamNames") {
		t.Fatalf("expected declaration-shaped tsReadSymbol for 'export async function* streamNames', got %#v", readAsyncGenerator)
	}
	findDeclareClass := executeTool(mgr, "ts_find_symbol", ToolRequest{Tool: "ts_find_symbol", Pattern: "export declare class ExternalClient"})
	if findDeclareClass.Error != "" || !strings.Contains(findDeclareClass.Output, "external.d.ts") {
		t.Fatalf("expected declaration-shaped tsFindSymbol for 'export declare class ExternalClient', got %#v", findDeclareClass)
	}
	readDeclareClass := executeTool(mgr, "ts_read_symbol", ToolRequest{Tool: "ts_read_symbol", Pattern: "export declare class ExternalClient"})
	if readDeclareClass.Error != "" || !strings.Contains(readDeclareClass.Output, "export declare class ExternalClient") {
		t.Fatalf("expected declaration-shaped tsReadSymbol for 'export declare class ExternalClient', got %#v", readDeclareClass)
	}
	readDeclareConst := executeTool(mgr, "ts_read_symbol", ToolRequest{Tool: "ts_read_symbol", Pattern: "export declare const defaultEndpoint"})
	if readDeclareConst.Error != "" || !strings.Contains(readDeclareConst.Output, "export declare const defaultEndpoint") {
		t.Fatalf("expected declaration-shaped tsReadSymbol for 'export declare const defaultEndpoint', got %#v", readDeclareConst)
	}
	findEnum := executeTool(mgr, "ts_find_symbol", ToolRequest{Tool: "ts_find_symbol", Pattern: "export enum GreetMode"})
	if findEnum.Error != "" || !strings.Contains(findEnum.Output, "GreetMode") {
		t.Fatalf("expected declaration-shaped tsFindSymbol for 'export enum GreetMode', got %#v", findEnum)
	}
	readNamespace := executeTool(mgr, "ts_read_symbol", ToolRequest{Tool: "ts_read_symbol", Pattern: "export namespace GreeterTools"})
	if readNamespace.Error != "" || !strings.Contains(readNamespace.Output, "export namespace GreeterTools") {
		t.Fatalf("expected declaration-shaped tsReadSymbol for 'export namespace GreeterTools', got %#v", readNamespace)
	}

	entry := executeTool(mgr, "ts_entrypoints", ToolRequest{Tool: "ts_entrypoints"})
	if entry.Error != "" || !strings.Contains(entry.Output, "index.ts") {
		t.Fatalf("unexpected tsEntrypoints result: %#v", entry)
	}
	// calls_from via semantic index
	callsFrom := executeToolCtx(context.Background(), mgr, "ts_calls_from", ToolRequest{Tool: "ts_calls_from", Pattern: "main"})
	if callsFrom.Error != "" || !strings.Contains(callsFrom.Output, "greet") {
		t.Fatalf("unexpected tsCallsFrom result: %#v", callsFrom)
	}

	// Ensure .jsx is indexed too.
	findJsx := executeTool(mgr, "ts_find_symbol", ToolRequest{Tool: "ts_find_symbol", Pattern: "Panel"})
	if findJsx.Error != "" || !strings.Contains(findJsx.Output, "util.jsx") {
		t.Fatalf("unexpected tsFindSymbol for jsx: %#v", findJsx)
	}
}

func TestTsWorkspaceToolsWithTsserver(t *testing.T) {
	if _, err := exec.LookPath("typescript-language-server"); err != nil {
		t.Skip("typescript-language-server not installed")
	}

	root := t.TempDir()
	writeTsTestFixture(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020","module":"commonjs","strict":true}}`)
	writeTsTestFixture(t, filepath.Join(root, "src", "greeter.ts"), `
export interface Greeter { name: string }

export function greet(name: string): void {
  console.log(name);
}

export class Shouter {
  shout(name: string): void {
    console.log(name.toUpperCase());
  }
}
`)
	writeTsTestFixture(t, filepath.Join(root, "src", "index.ts"), `
import { greet, Shouter } from "./greeter";

export function main(): void {
  greet("world");
  const shouter = new Shouter();
  shouter.shout("hi");
}
`)

	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		EnableShell:           true,
		EnableWrites:          true,
		CommandTimeoutSeconds: 60,
		MaxFileBytes:          65536,
		MaxDirectoryEntries:   50,
	})

	ws := executeToolCtx(context.Background(), mgr, "ts_workspace_symbol", ToolRequest{Tool: "ts_workspace_symbol", Pattern: "Shouter"})
	if ws.Error != "" || !strings.Contains(ws.Output, "greeter.ts") {
		t.Fatalf("unexpected tsWorkspaceSymbol result: %#v", ws)
	}

	def := executeToolCtx(context.Background(), mgr, "ts_definition", ToolRequest{Tool: "ts_definition", Pattern: "greet"})
	if def.Error != "" || !strings.Contains(def.Output, "greeter.ts") {
		t.Fatalf("unexpected tsDefinition result: %#v", def)
	}

	refs := executeToolCtx(context.Background(), mgr, "ts_references", ToolRequest{Tool: "ts_references", Pattern: "greet", Path: "."})
	if refs.Error != "" {
		t.Fatalf("unexpected tsReferences error: %#v", refs)
	}
}

func writeTsTestFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

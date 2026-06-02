package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildTsIndex(t *testing.T) {
	root := t.TempDir()
	writeTsFixture(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020"}}`)
	writeTsFixture(t, filepath.Join(root, "src", "index.ts"), `
import { greet } from "./greeter";

export function main(): void {
  greet("world");
}
`)
	writeTsFixture(t, filepath.Join(root, "src", "greeter.ts"), `
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

export const enum WireFormat {
  Json,
  Binary,
}

export namespace GreeterTools {
  export function label(name: string): string {
    return name;
  }
}
`)
	writeTsFixture(t, filepath.Join(root, "src", "default_class.ts"), `
export default class DefaultShouter {}
`)
	writeTsFixture(t, filepath.Join(root, "src", "default_function.ts"), `
export default function defaultGreet(): void {}
`)

	idx, err := BuildTsIndex(root)
	if err != nil {
		t.Fatalf("BuildTsIndex: %v", err)
	}
	if idx == nil {
		t.Fatal("expected non-nil index")
	}
	if len(idx.Files) != 4 {
		t.Fatalf("expected 4 files, got %d", len(idx.Files))
	}

	greeter := idx.FileByPath("src/greeter.ts")
	if greeter == nil {
		t.Fatal("missing greeter.ts")
	}
	if greeter.Funcs < 1 || greeter.Classes < 1 || greeter.Types < 1 {
		t.Fatalf("greeter.ts counts: %#v", greeter)
	}

	// Import graph: index.ts -> greeter.ts
	if deps := idx.Forward["src/index.ts"]; len(deps) == 0 || deps[0] != "src/greeter.ts" {
		t.Fatalf("expected src/index.ts to import src/greeter.ts, got %v", deps)
	}

	// Symbol search
	matches := idx.FindSymbols("greet", 8)
	if len(matches) == 0 {
		t.Fatal("expected FindSymbols to return greet")
	}
	if matches := idx.FindSymbols("export interface Greeter", 8); len(matches) == 0 || matches[0].Name != "Greeter" {
		t.Fatalf("expected declaration-shaped interface search to find Greeter, got %#v", matches)
	}
	if matches := idx.FindSymbols("export class Shouter", 8); len(matches) == 0 || matches[0].Name != "Shouter" {
		t.Fatalf("expected declaration-shaped class search to find Shouter, got %#v", matches)
	}
	if matches := idx.FindSymbols("export default class DefaultShouter", 8); len(matches) == 0 || matches[0].Name != "DefaultShouter" {
		t.Fatalf("expected default class search to find DefaultShouter, got %#v", matches)
	}
	if matches := idx.FindSymbols("export default function defaultGreet", 8); len(matches) == 0 || matches[0].Name != "defaultGreet" {
		t.Fatalf("expected default function search to find defaultGreet, got %#v", matches)
	}
	if matches := idx.FindSymbols("export function* greetNames", 8); len(matches) == 0 || matches[0].Name != "greetNames" {
		t.Fatalf("expected declaration-shaped generator function search to find greetNames, got %#v", matches)
	}
	if matches := idx.FindSymbols("export async function* streamNames", 8); len(matches) == 0 || matches[0].Name != "streamNames" {
		t.Fatalf("expected declaration-shaped async generator search to find streamNames, got %#v", matches)
	}
	if matches := idx.FindSymbols("export enum GreetMode", 8); len(matches) == 0 || matches[0].Name != "GreetMode" {
		t.Fatalf("expected declaration-shaped enum search to find GreetMode, got %#v", matches)
	}
	if matches := idx.FindSymbols("export const enum WireFormat", 8); len(matches) == 0 || matches[0].Name != "WireFormat" {
		t.Fatalf("expected declaration-shaped const enum search to find WireFormat, got %#v", matches)
	}
	if matches := idx.FindSymbols("export namespace GreeterTools", 8); len(matches) == 0 || matches[0].Name != "GreeterTools" {
		t.Fatalf("expected declaration-shaped namespace search to find GreeterTools, got %#v", matches)
	}

	// Call graph: main -> greet
	outgoing := idx.FindCallsFrom("main", 8)
	foundGreet := false
	for _, call := range outgoing {
		if call.CalleeName == "greet" {
			foundGreet = true
			break
		}
	}
	if !foundGreet {
		t.Fatalf("expected main to call greet, got %+v", outgoing)
	}

	// Incoming: greet is called by main
	incoming := idx.FindCallsInto("greet", 8)
	if len(incoming) == 0 {
		t.Fatalf("expected at least one caller of greet")
	}

	// Reverse import graph: greeter.ts is imported by index.ts
	importers := idx.Reverse["src/greeter.ts"]
	if len(importers) == 0 || !strings.Contains(strings.Join(importers, ","), "src/index.ts") {
		t.Fatalf("expected greeter.ts to be imported by src/index.ts, got %v", importers)
	}
}

func TestTsIndexSkipsNodeModules(t *testing.T) {
	root := t.TempDir()
	writeTsFixture(t, filepath.Join(root, "src", "app.ts"), "export function app() {}\n")
	writeTsFixture(t, filepath.Join(root, "node_modules", "lodash", "index.js"), "module.exports = {};\n")

	idx, err := BuildTsIndex(root)
	if err != nil {
		t.Fatalf("BuildTsIndex: %v", err)
	}
	if idx == nil || len(idx.Files) != 1 {
		t.Fatalf("expected 1 indexed file, got %d", len(idx.Files))
	}
	if idx.Files[0].Path != "src/app.ts" {
		t.Fatalf("indexed wrong file: %s", idx.Files[0].Path)
	}
}

func TestTsIndexDetectsClassMethod(t *testing.T) {
	root := t.TempDir()
	writeTsFixture(t, filepath.Join(root, "cls.ts"), `
export class Foo {
  bar(): number { return 1; }
  baz(): number { return this.bar(); }
}
`)
	idx, err := BuildTsIndex(root)
	if err != nil || idx == nil {
		t.Fatalf("BuildTsIndex: %v", err)
	}
	file := idx.FileByPath("cls.ts")
	if file == nil {
		t.Fatal("missing cls.ts")
	}
	var names []string
	for _, sym := range file.Symbols {
		names = append(names, sym.Qualified)
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "Foo.bar") || !strings.Contains(joined, "Foo.baz") {
		t.Fatalf("expected Foo.bar and Foo.baz methods, got %s", joined)
	}
}

func TestBuildTsIndexDeclarationSyntax(t *testing.T) {
	root := t.TempDir()
	writeTsFixture(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020"}}`)
	writeTsFixture(t, filepath.Join(root, "types", "external.d.ts"), `
export declare class ExternalClient {
  connect(): void;
  disconnect(): void;
}

export declare function connect(endpoint: string): ExternalClient;

export declare const defaultEndpoint: string;
export const exportedEndpoint: string;
declare const ambientEndpoint: string;

export interface Config {
  timeout: number;
}

export declare type Result<T> = T | Error;
`)

	idx, err := BuildTsIndex(root)
	if err != nil {
		t.Fatalf("BuildTsIndex: %v", err)
	}
	if idx == nil {
		t.Fatal("expected non-nil index")
	}

	declFile := idx.FileByPath("types/external.d.ts")
	if declFile == nil {
		t.Fatal("missing types/external.d.ts")
	}

	// Verify symbols from declaration syntax are indexed
	var foundClass, foundFunc, foundConst, foundExportConst, foundAmbientConst, foundInterface bool
	var foundType bool
	for _, sym := range declFile.Symbols {
		switch sym.Name {
		case "ExternalClient":
			foundClass = sym.Kind == "class"
		case "connect":
			foundFunc = sym.Kind == "function"
		case "defaultEndpoint":
			foundConst = sym.Kind == "const"
		case "exportedEndpoint":
			foundExportConst = sym.Kind == "const"
		case "ambientEndpoint":
			foundAmbientConst = sym.Kind == "const"
		case "Config":
			foundInterface = sym.Kind == "interface"
		case "Result":
			foundType = sym.Kind == "type"
		}
	}

	if !foundClass {
		t.Error("expected to find ExternalClient class")
	}
	if !foundFunc {
		t.Error("expected to find connect function")
	}
	if !foundConst {
		t.Error("expected to find defaultEndpoint const")
	}
	if !foundExportConst {
		t.Error("expected to find exportedEndpoint const")
	}
	if !foundAmbientConst {
		t.Error("expected to find ambientEndpoint const")
	}
	if !foundInterface {
		t.Error("expected to find Config interface")
	}
	if !foundType {
		t.Error("expected to find Result type")
	}

	// Verify declaration-shaped search patterns work for declaration syntax
	if matches := idx.FindSymbols("export declare class ExternalClient", 8); len(matches) == 0 || matches[0].Name != "ExternalClient" {
		t.Fatalf("expected declaration-shaped class search to find ExternalClient, got %#v", matches)
	}
	if matches := idx.FindSymbols("export declare function connect", 8); len(matches) == 0 || matches[0].Name != "connect" {
		t.Fatalf("expected declaration-shaped function search to find connect, got %#v", matches)
	}
	if matches := idx.FindSymbols("export declare const defaultEndpoint", 8); len(matches) == 0 || matches[0].Name != "defaultEndpoint" {
		t.Fatalf("expected declaration-shaped const search to find defaultEndpoint, got %#v", matches)
	}
	if matches := idx.FindSymbols("declare const ambientEndpoint", 8); len(matches) == 0 || matches[0].Name != "ambientEndpoint" {
		t.Fatalf("expected bare declare const search to find ambientEndpoint, got %#v", matches)
	}
}

func writeTsFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

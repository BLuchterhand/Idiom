package typescript

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TypeScript semantic index", func() {
	It("indexes declarations, calls, imports, and modern export syntax", func() {
		root := GinkgoT().TempDir()
		writeTsFixture(filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020"}}`)
		writeTsFixture(filepath.Join(root, "src", "index.ts"), `
import { greet } from "./greeter";

export function main(): void {
  greet("world");
}
`)
		writeTsFixture(filepath.Join(root, "src", "greeter.ts"), `
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
		writeTsFixture(filepath.Join(root, "src", "default_class.ts"), `
export default class DefaultShouter {}
`)
		writeTsFixture(filepath.Join(root, "src", "default_function.ts"), `
export default function defaultGreet(): void {}
`)

		idx, err := BuildTsIndex(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).NotTo(BeNil())
		Expect(idx.Files).To(HaveLen(4))

		greeter := idx.FileByPath("src/greeter.ts")
		Expect(greeter).NotTo(BeNil())
		Expect(greeter.Funcs).To(BeNumerically(">=", 1))
		Expect(greeter.Classes).To(BeNumerically(">=", 1))
		Expect(greeter.Types).To(BeNumerically(">=", 1))
		Expect(idx.Forward["src/index.ts"]).NotTo(BeEmpty())
		Expect(idx.Forward["src/index.ts"][0]).To(Equal("src/greeter.ts"))
		Expect(idx.FindSymbols("greet", 8)).NotTo(BeEmpty())

		for query, expected := range map[string]string{
			"export interface Greeter":             "Greeter",
			"export class Shouter":                 "Shouter",
			"export default class DefaultShouter":  "DefaultShouter",
			"export default function defaultGreet": "defaultGreet",
			"export function* greetNames":          "greetNames",
			"export async function* streamNames":   "streamNames",
			"export enum GreetMode":                "GreetMode",
			"export const enum WireFormat":         "WireFormat",
			"export namespace GreeterTools":        "GreeterTools",
		} {
			matches := idx.FindSymbols(query, 8)
			Expect(matches).NotTo(BeEmpty(), "query %q", query)
			Expect(matches[0].Name).To(Equal(expected))
		}

		foundGreet := false
		for _, call := range idx.FindCallsFrom("main", 8) {
			if call.CalleeName == "greet" {
				foundGreet = true
				break
			}
		}
		Expect(foundGreet).To(BeTrue())
		Expect(idx.FindCallsInto("greet", 8)).NotTo(BeEmpty())
		Expect(idx.Reverse["src/greeter.ts"]).To(ContainElement("src/index.ts"))
	})

	It("skips node_modules", func() {
		root := GinkgoT().TempDir()
		writeTsFixture(filepath.Join(root, "src", "app.ts"), "export function app() {}\n")
		writeTsFixture(filepath.Join(root, "node_modules", "lodash", "index.js"), "module.exports = {};\n")

		idx, err := BuildTsIndex(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).NotTo(BeNil())
		Expect(idx.Files).To(HaveLen(1))
		Expect(idx.Files[0].Path).To(Equal("src/app.ts"))
	})

	It("tracks class method qualification", func() {
		root := GinkgoT().TempDir()
		writeTsFixture(filepath.Join(root, "cls.ts"), `
export class Foo {
  bar(): number { return 1; }
  baz(): number { return this.bar(); }
}
`)

		idx, err := BuildTsIndex(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).NotTo(BeNil())
		file := idx.FileByPath("cls.ts")
		Expect(file).NotTo(BeNil())

		var names []string
		for _, symbol := range file.Symbols {
			names = append(names, symbol.Qualified)
		}
		Expect(names).To(And(ContainElement("Foo.bar"), ContainElement("Foo.baz")))
	})

	It("indexes ambient declaration syntax", func() {
		root := GinkgoT().TempDir()
		writeTsFixture(filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"target":"es2020"}}`)
		writeTsFixture(filepath.Join(root, "types", "external.d.ts"), `
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
		Expect(err).NotTo(HaveOccurred())
		Expect(idx).NotTo(BeNil())
		declarationFile := idx.FileByPath("types/external.d.ts")
		Expect(declarationFile).NotTo(BeNil())

		kinds := map[string]string{}
		for _, symbol := range declarationFile.Symbols {
			kinds[symbol.Name] = symbol.Kind
		}
		Expect(kinds).To(HaveKeyWithValue("ExternalClient", "class"))
		Expect(kinds).To(HaveKeyWithValue("connect", "function"))
		Expect(kinds).To(HaveKeyWithValue("defaultEndpoint", "const"))
		Expect(kinds).To(HaveKeyWithValue("exportedEndpoint", "const"))
		Expect(kinds).To(HaveKeyWithValue("ambientEndpoint", "const"))
		Expect(kinds).To(HaveKeyWithValue("Config", "interface"))
		Expect(kinds).To(HaveKeyWithValue("Result", "type"))

		for query, expected := range map[string]string{
			"export declare class ExternalClient":  "ExternalClient",
			"export declare function connect":      "connect",
			"export declare const defaultEndpoint": "defaultEndpoint",
			"declare const ambientEndpoint":        "ambientEndpoint",
		} {
			matches := idx.FindSymbols(query, 8)
			Expect(matches).NotTo(BeEmpty(), "query %q", query)
			Expect(matches[0].Name).To(Equal(expected))
		}
	})
})

func writeTsFixture(path string, content string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644)).To(Succeed())
}

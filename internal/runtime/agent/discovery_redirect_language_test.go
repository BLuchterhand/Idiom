package agent

import (
	"testing"

	"idiom/internal/capabilities/workspace"
)

func TestSearchContentRedirectsAdditionalLanguageSyntaxPatterns(t *testing.T) {
	cases := []struct {
		name    string
		files   map[string]string
		pattern string
		want    string
	}{
		{
			name: "python_class",
			files: map[string]string{
				"pyproject.toml": "[project]\nname='x'\n",
				"pkg/model.py":   "class Widget:\n    pass\n",
			},
			pattern: "class Widget",
			want:    "python",
		},
		{
			name: "python_function",
			files: map[string]string{
				"pyproject.toml": "[project]\nname='x'\n",
				"pkg/model.py":   "def build_widget():\n    pass\n",
			},
			pattern: "def build_widget",
			want:    "python",
		},
		{
			name: "python_async_function",
			files: map[string]string{
				"pyproject.toml": "[project]\nname='x'\n",
				"pkg/model.py":   "async def load_widget():\n    pass\n",
			},
			pattern: "async def load_widget",
			want:    "python",
		},
		{
			name: "typescript_interface",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export interface Widget {}\n",
			},
			pattern: "export interface Widget",
			want:    "typescript",
		},
		{
			name: "typescript_class",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export class Widget {}\n",
			},
			pattern: "export class Widget",
			want:    "typescript",
		},
		{
			name: "typescript_default_class",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export default class Widget {}\n",
			},
			pattern: "export default class Widget",
			want:    "typescript",
		},
		{
			name: "typescript_async_function",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export async function loadWidget() {}\n",
			},
			pattern: "export async function loadWidget",
			want:    "typescript",
		},
		{
			name: "typescript_default_function",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export default function loadWidget() {}\n",
			},
			pattern: "export default function loadWidget",
			want:    "typescript",
		},
		{
			name: "typescript_generator_function",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export function* loadWidgets() {}\n",
			},
			pattern: "export function* loadWidgets",
			want:    "typescript",
		},
		{
			name: "typescript_async_generator_function",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export async function* streamWidgets() {}\n",
			},
			pattern: "export async function* streamWidgets",
			want:    "typescript",
		},
		{
			name: "typescript_const",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export const widgetFactory = () => {};\n",
			},
			pattern: "export const widgetFactory",
			want:    "typescript",
		},
		{
			name: "typescript_declare_class",
			files: map[string]string{
				"package.json":        `{"type":"module"}`,
				"types/external.d.ts": "export declare class ExternalClient {}\n",
			},
			pattern: "export declare class ExternalClient",
			want:    "typescript",
		},
		{
			name: "typescript_declare_function",
			files: map[string]string{
				"package.json":        `{"type":"module"}`,
				"types/external.d.ts": "declare function connect(): void;\n",
			},
			pattern: "declare function connect",
			want:    "typescript",
		},
		{
			name: "typescript_declare_const",
			files: map[string]string{
				"package.json":        `{"type":"module"}`,
				"types/external.d.ts": "export declare const defaultEndpoint: string;\n",
			},
			pattern: "export declare const defaultEndpoint",
			want:    "typescript",
		},
		{
			name: "typescript_enum",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export enum WidgetKind { Primary }\n",
			},
			pattern: "export enum WidgetKind",
			want:    "typescript",
		},
		{
			name: "typescript_const_enum",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export const enum WireFormat { Json }\n",
			},
			pattern: "export const enum WireFormat",
			want:    "typescript",
		},
		{
			name: "typescript_namespace",
			files: map[string]string{
				"package.json": `{"type":"module"}`,
				"src/model.ts": "export namespace WidgetTools {}\n",
			},
			pattern: "export namespace WidgetTools",
			want:    "typescript",
		},
		{
			name: "rust_struct",
			files: map[string]string{
				"Cargo.toml": "[package]\nname='x'\nversion='0.0.0'\n",
				"src/lib.rs": "pub struct Widget;\n",
			},
			pattern: "pub struct Widget",
			want:    "rust",
		},
		{
			name: "rust_enum",
			files: map[string]string{
				"Cargo.toml": "[package]\nname='x'\nversion='0.0.0'\n",
				"src/lib.rs": "pub enum WidgetKind { A, B }\n",
			},
			pattern: "pub enum WidgetKind",
			want:    "rust",
		},
		{
			name: "rust_trait",
			files: map[string]string{
				"Cargo.toml": "[package]\nname='x'\nversion='0.0.0'\n",
				"src/lib.rs": "pub trait Runnable {\n    fn run(&self);\n}\n",
			},
			pattern: "pub trait Runnable",
			want:    "rust",
		},
		{
			name: "rust_async_function",
			files: map[string]string{
				"Cargo.toml": "[package]\nname='x'\nversion='0.0.0'\n",
				"src/lib.rs": "pub async fn load_widget() {}\n",
			},
			pattern: "pub async fn load_widget",
			want:    "rust",
		},
		{
			name: "rust_const_function",
			files: map[string]string{
				"Cargo.toml": "[package]\nname='x'\nversion='0.0.0'\n",
				"src/lib.rs": "pub const fn default_widget_id() -> u32 { 0 }\n",
			},
			pattern: "pub const fn default_widget_id",
			want:    "rust",
		},
		{
			name: "rust_unsafe_trait",
			files: map[string]string{
				"Cargo.toml": "[package]\nname='x'\nversion='0.0.0'\n",
				"src/lib.rs": "pub unsafe trait Runnable {}\n",
			},
			pattern: "pub unsafe trait Runnable",
			want:    "rust",
		},
		{
			name: "rust_type_alias",
			files: map[string]string{
				"Cargo.toml": "[package]\nname='x'\nversion='0.0.0'\n",
				"src/lib.rs": "pub type WidgetId = u32;\n",
			},
			pattern: "pub type WidgetId",
			want:    "rust",
		},
		{
			name: "rust_static_mut",
			files: map[string]string{
				"Cargo.toml": "[package]\nname='x'\nversion='0.0.0'\n",
				"src/lib.rs": "pub static mut DEFAULT_ID: u32 = 0;\n",
			},
			pattern: "pub static mut DEFAULT_ID",
			want:    "rust",
		},
		{
			name: "rust_macro_rules",
			files: map[string]string{
				"Cargo.toml": "[package]\nname='x'\nversion='0.0.0'\n",
				"src/lib.rs": "macro_rules! widget { () => {} }\n",
			},
			pattern: "macro_rules! widget",
			want:    "rust",
		},
		{
			name: "rust_impl",
			files: map[string]string{
				"Cargo.toml": "[package]\nname='x'\nversion='0.0.0'\n",
				"src/lib.rs": "impl Widget {\n    fn new() -> Self { Widget }\n}\n",
			},
			pattern: "impl Widget",
			want:    "rust",
		},
		{
			name: "java_record",
			files: map[string]string{
				"pom.xml":              "<project></project>\n",
				"src/main/Widget.java": "public record Widget() {}\n",
			},
			pattern: "public record Widget",
			want:    "java",
		},
		{
			name: "java_class",
			files: map[string]string{
				"pom.xml":              "<project></project>\n",
				"src/main/Widget.java": "public class Widget {}\n",
			},
			pattern: "public class Widget",
			want:    "java",
		},
		{
			name: "java_interface",
			files: map[string]string{
				"pom.xml":               "<project></project>\n",
				"src/main/Service.java": "public interface Service {}\n",
			},
			pattern: "public interface Service",
			want:    "java",
		},
		{
			name: "java_enum",
			files: map[string]string{
				"pom.xml":                  "<project></project>\n",
				"src/main/WidgetKind.java": "public enum WidgetKind { A, B }\n",
			},
			pattern: "public enum WidgetKind",
			want:    "java",
		},
		{
			name: "java_annotation",
			files: map[string]string{
				"pom.xml":                   "<project></project>\n",
				"src/main/FeatureFlag.java": "public @interface FeatureFlag {}\n",
			},
			pattern: "public @interface FeatureFlag",
			want:    "java",
		},
		{
			name: "java_sealed_interface",
			files: map[string]string{
				"pom.xml":                     "<project></project>\n",
				"src/main/SealedService.java": "public sealed interface SealedService permits ServiceImpl {}\n",
			},
			pattern: "public sealed interface SealedService",
			want:    "java",
		},
		{
			name: "java_method",
			files: map[string]string{
				"pom.xml":               "<project></project>\n",
				"src/main/Service.java": "public class Service { public void execute() {} }\n",
			},
			pattern: "public void execute()",
			want:    "java",
		},
		{
			name: "java_private_method",
			files: map[string]string{
				"pom.xml":               "<project></project>\n",
				"src/main/Service.java": "public class Service { private int compute() { return 1; } }\n",
			},
			pattern: "private int compute()",
			want:    "java",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := newTestWorkspaceWithFiles(t, tc.files)
			d := NewDiscoveryRedirect()
			decision := d.Consider(workspace.ToolRequest{Tool: "search_content", Pattern: tc.pattern}, ws)
			if !decision.ShouldRedirect {
				t.Fatalf("pattern %q should redirect", tc.pattern)
			}
			if decision.Language != tc.want {
				t.Fatalf("language: want %s, got %s", tc.want, decision.Language)
			}
		})
	}
}

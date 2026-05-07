package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/config"
)

func TestJavaWorkspaceTools(t *testing.T) {
	root := buildJavaTempWorkspace(t)
	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		EnableShell:           true,
		EnableWrites:          true,
		CommandTimeoutSeconds: 30,
		MaxFileBytes:          65536,
	})

	if !mgr.isJavaWorkspace() {
		t.Fatal("expected temp fixture to count as Java workspace")
	}
	overview := mgr.javaOverview()
	if overview.Error != "" || !strings.Contains(overview.Output, "Indexed") {
		t.Fatalf("unexpected javaOverview result: %#v", overview)
	}

	find := mgr.javaFindSymbol(ToolRequest{Tool: "java_find_symbol", Pattern: "Helper"})
	if find.Error != "" || !strings.Contains(find.Output, "Helper.java") {
		t.Fatalf("unexpected javaFindSymbol result: %#v", find)
	}
	findDecl := mgr.javaFindSymbol(ToolRequest{Tool: "java_find_symbol", Pattern: "public class Helper"})
	if findDecl.Error != "" || !strings.Contains(findDecl.Output, "Helper") {
		t.Fatalf("expected declaration-shaped javaFindSymbol query to find Helper, got %#v", findDecl)
	}

	outline := mgr.javaFileOutline(ToolRequest{Tool: "java_file_outline", Path: filepath.Join("src", "main", "java", "com", "example", "App.java")})
	if outline.Error != "" {
		t.Fatalf("unexpected javaFileOutline error: %#v", outline)
	}
	if !strings.Contains(outline.Output, "package: com.example") {
		t.Fatalf("javaFileOutline missing package line: %#v", outline)
	}
	if !strings.Contains(outline.Output, "com.example.util.Helper") {
		t.Fatalf("javaFileOutline missing import: %#v", outline)
	}
	if !strings.Contains(outline.Output, "main") || !strings.Contains(outline.Output, "App") {
		t.Fatalf("javaFileOutline missing symbols: %#v", outline)
	}

	entry := mgr.javaEntrypoints(ToolRequest{Tool: "java_entrypoints"})
	if entry.Error != "" || !strings.Contains(entry.Output, "App.java") {
		t.Fatalf("unexpected javaEntrypoints result: %#v", entry)
	}

	related := mgr.javaRelatedFiles(ToolRequest{Tool: "java_related_files", Pattern: "helper"})
	if related.Error != "" {
		t.Fatalf("unexpected javaRelatedFiles error: %#v", related)
	}

	impls := mgr.javaImplementations(context.Background(), ToolRequest{Tool: "java_implementations", Pattern: "Service"})
	if impls.Error != "" || !strings.Contains(impls.Output, "ServiceImpl.java") {
		t.Fatalf("unexpected javaImplementations result: %#v", impls)
	}

	refs := mgr.javaReferences(context.Background(), ToolRequest{Tool: "java_references", Pattern: "Helper", Path: "."})
	if refs.Error != "" || !strings.Contains(refs.Output, "Helper") {
		t.Fatalf("unexpected javaReferences result: %#v", refs)
	}

	read := mgr.javaReadSymbol(ToolRequest{Tool: "java_read_symbol", Pattern: "Helper#run"})
	if read.Error != "" {
		t.Fatalf("unexpected javaReadSymbol error: %#v", read)
	}
	// Fall back: allow either Helper#run or plain Helper match.
	if !strings.Contains(read.Output, "Helper") && !strings.Contains(read.Output, "run") {
		t.Fatalf("unexpected javaReadSymbol result: %#v", read)
	}
	readDecl := mgr.javaReadSymbol(ToolRequest{Tool: "java_read_symbol", Pattern: "public class Helper"})
	if readDecl.Error != "" || !strings.Contains(readDecl.Output, "public class Helper") {
		t.Fatalf("expected declaration-shaped javaReadSymbol to return Helper class, got %#v", readDecl)
	}

	callsFrom := mgr.javaCallsFrom(context.Background(), ToolRequest{Tool: "java_calls_from", Pattern: "main"})
	if callsFrom.Error != "" {
		t.Fatalf("unexpected javaCallsFrom result: %#v", callsFrom)
	}

	callsInto := mgr.javaCallsInto(context.Background(), ToolRequest{Tool: "java_calls_into", Pattern: "compute"})
	if callsInto.Error != "" {
		t.Fatalf("unexpected javaCallsInto result: %#v", callsInto)
	}

	ws := mgr.javaWorkspaceSymbol(context.Background(), ToolRequest{Tool: "java_workspace_symbol", Pattern: "Helper"})
	if ws.Error != "" || !strings.Contains(ws.Output, "Helper") {
		t.Fatalf("unexpected javaWorkspaceSymbol result: %#v", ws)
	}
}

func TestJavaWorkspaceToolsWithJdtls(t *testing.T) {
	if _, err := exec.LookPath("jdtls"); err != nil {
		t.Skip("jdtls not installed")
	}
	root := buildJavaTempWorkspace(t)
	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		EnableShell:           true,
		EnableWrites:          true,
		CommandTimeoutSeconds: 60,
		MaxFileBytes:          65536,
	})

	ws := mgr.javaWorkspaceSymbol(context.Background(), ToolRequest{Tool: "java_workspace_symbol", Pattern: "Helper"})
	if ws.Error != "" || !strings.Contains(ws.Summary, "jdtls") {
		t.Fatalf("expected jdtls summary, got: %#v", ws)
	}

	def := mgr.javaDefinition(context.Background(), ToolRequest{Tool: "java_definition", Pattern: "Helper"})
	if def.Error != "" {
		t.Fatalf("unexpected javaDefinition error: %#v", def)
	}

	refs := mgr.javaReferences(context.Background(), ToolRequest{Tool: "java_references", Pattern: "Helper", Path: "."})
	if refs.Error != "" {
		t.Fatalf("unexpected javaReferences result: %#v", refs)
	}

	impls := mgr.javaImplementations(context.Background(), ToolRequest{Tool: "java_implementations", Pattern: "Service"})
	if impls.Error != "" {
		t.Fatalf("unexpected javaImplementations result: %#v", impls)
	}
}

func TestJavaDiagnosticsRequiresBuildSystem(t *testing.T) {
	root := buildJavaTempWorkspace(t)
	mgr := NewManager(config.Workspace{
		Root:                  root,
		CWD:                   root,
		EnableShell:           true,
		EnableWrites:          true,
		CommandTimeoutSeconds: 30,
		MaxFileBytes:          65536,
	})
	// Diagnostics may fail (no mvn/gradle/javac on CI PATH) but must
	// never panic and must always produce a ToolResult tagged as
	// java_diagnostics.
	result := mgr.javaDiagnostics(context.Background(), ToolRequest{Tool: "java_diagnostics"})
	if result.Tool != "java_diagnostics" {
		t.Fatalf("expected tool=java_diagnostics, got %q", result.Tool)
	}
}

// buildJavaTempWorkspace constructs a throwaway Maven-ish workspace
// used by multiple tests. Returns the root path.
func buildJavaTempWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeJavaFile(t, filepath.Join(root, "pom.xml"), `<?xml version="1.0"?>
<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>example</artifactId>
  <version>0.1.0</version>
</project>
`)
	writeJavaFile(t, filepath.Join(root, "src", "main", "java", "com", "example", "App.java"), `
package com.example;

import com.example.util.Helper;

public class App {
    public static void main(String[] args) {
        Helper helper = new Helper();
        helper.run();
    }
}
`)
	writeJavaFile(t, filepath.Join(root, "src", "main", "java", "com", "example", "util", "Helper.java"), `
package com.example.util;

public class Helper {
    public void run() {
        compute();
    }

    private int compute() {
        return 42;
    }
}
`)
	writeJavaFile(t, filepath.Join(root, "src", "main", "java", "com", "example", "Service.java"), `
package com.example;

public interface Service {
    void execute();
}
`)
	writeJavaFile(t, filepath.Join(root, "src", "main", "java", "com", "example", "ServiceImpl.java"), `
package com.example;

public class ServiceImpl implements Service {
    @Override
    public void execute() {
    }
}
`)
	writeJavaFile(t, filepath.Join(root, "src", "test", "java", "com", "example", "AppTest.java"), `
package com.example;

import org.junit.Test;

public class AppTest {
    @Test
    public void smokeTest() {
    }
}
`)
	return root
}

func writeJavaFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

package java

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildJavaIndex(t *testing.T) {
	root := t.TempDir()
	writeJavaFixture(t, filepath.Join(root, "src", "main", "java", "com", "example", "App.java"), `
package com.example;

import com.example.util.Helper;

public class App {
    public static void main(String[] args) {
        Helper helper = new Helper();
        helper.run();
    }
}
`)
	writeJavaFixture(t, filepath.Join(root, "src", "main", "java", "com", "example", "util", "Helper.java"), `
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
	writeJavaFixture(t, filepath.Join(root, "src", "main", "java", "com", "example", "Service.java"), `
package com.example;

public interface Service {
    void execute();
}
`)
	writeJavaFixture(t, filepath.Join(root, "src", "main", "java", "com", "example", "FeatureFlag.java"), `
package com.example;

public @interface FeatureFlag {
    String value() default "";
}
`)
	writeJavaFixture(t, filepath.Join(root, "src", "main", "java", "com", "example", "SealedService.java"), `
package com.example;

public sealed interface SealedService permits SealedServiceImpl {
    void close();
}

final class SealedServiceImpl implements SealedService {
    public void close() {
    }
}
`)
	writeJavaFixture(t, filepath.Join(root, "src", "main", "java", "com", "example", "ServiceImpl.java"), `
package com.example;

public class ServiceImpl implements Service {
    @Override
    public void execute() {
    }
}
`)
	writeJavaFixture(t, filepath.Join(root, "src", "test", "java", "com", "example", "AppTest.java"), `
package com.example;

import org.junit.Test;

public class AppTest {
    @Test
    public void smokeTest() {
    }
}
`)

	index, err := BuildJavaIndex(root)
	if err != nil {
		t.Fatalf("BuildJavaIndex: %v", err)
	}
	if index == nil {
		t.Fatal("expected non-nil index")
	}
	if len(index.Files) != 7 {
		t.Fatalf("expected 7 files, got %d", len(index.Files))
	}

	app := index.FileByPath("src/main/java/com/example/App.java")
	if app == nil {
		t.Fatal("App.java not indexed")
	}
	if !app.HasMain {
		t.Fatal("App.java should have main detected")
	}
	if app.Package != "com.example" {
		t.Fatalf("App.java package: %q", app.Package)
	}
	if !slices.Contains(app.Imports, "com.example.util.Helper") {
		t.Fatalf("App.java imports: %v", app.Imports)
	}

	matches := index.FindSymbols("Helper", 4)
	if len(matches) == 0 {
		t.Fatal("expected Helper matches")
	}
	foundHelper := false
	for _, m := range matches {
		if m.Name == "Helper" && m.Kind == "class" {
			foundHelper = true
			break
		}
	}
	if !foundHelper {
		t.Fatalf("Helper class not among matches: %+v", matches)
	}
	if matches := index.FindSymbols("public class Helper", 4); len(matches) == 0 || matches[0].Name != "Helper" {
		t.Fatalf("expected declaration-shaped class search to find Helper, got %#v", matches)
	}
	if matches := index.FindSymbols("public interface Service", 4); len(matches) == 0 || matches[0].Name != "Service" {
		t.Fatalf("expected declaration-shaped interface search to find Service, got %#v", matches)
	}
	if matches := index.FindSymbols("public @interface FeatureFlag", 4); len(matches) == 0 || matches[0].Name != "FeatureFlag" {
		t.Fatalf("expected declaration-shaped annotation search to find FeatureFlag, got %#v", matches)
	}
	if matches := index.FindSymbols("public sealed interface SealedService", 4); len(matches) == 0 || matches[0].Name != "SealedService" {
		t.Fatalf("expected declaration-shaped sealed interface search to find SealedService, got %#v", matches)
	}
	if matches := index.FindSymbols("public void run()", 4); len(matches) == 0 || matches[0].Name != "run" || !strings.HasSuffix(matches[0].Path, "Helper.java") {
		t.Fatalf("expected declaration-shaped method search to find run, got %#v", matches)
	}
	if matches := index.FindSymbols("private int compute()", 4); len(matches) == 0 || matches[0].Name != "compute" || !strings.HasSuffix(matches[0].Path, "Helper.java") {
		t.Fatalf("expected declaration-shaped private method search to find compute, got %#v", matches)
	}

	impls := index.Implementations("Service", 8)
	if len(impls) == 0 {
		t.Fatal("expected at least one Service implementation")
	}
	foundImpl := false
	for _, impl := range impls {
		if impl.Name == "ServiceImpl" {
			foundImpl = true
			break
		}
	}
	if !foundImpl {
		t.Fatalf("ServiceImpl not flagged as Service implementation: %+v", impls)
	}

	entries := index.Entrypoints(4)
	foundApp, foundTest := false, false
	for _, e := range entries {
		if strings.HasSuffix(e, "App.java") {
			foundApp = true
		}
		if strings.HasSuffix(e, "AppTest.java") {
			foundTest = true
		}
	}
	if !foundApp || !foundTest {
		t.Fatalf("entrypoints missing expected files: %v", entries)
	}

	callsFrom := index.FindCallsFrom("App#main", 16)
	if len(callsFrom) == 0 {
		t.Fatalf("expected outgoing calls from App.main")
	}

	related := index.RelevantFiles([]string{"helper"}, 4)
	if len(related) == 0 {
		t.Fatalf("expected related files for 'helper'")
	}

	// Reverse edges: App.java imports Helper.java.
	importers := index.Reverse["src/main/java/com/example/util/Helper.java"]
	foundApp = false
	for _, imp := range importers {
		if strings.HasSuffix(imp, "App.java") {
			foundApp = true
			break
		}
	}
	if !foundApp {
		t.Fatalf("App.java should directly import Helper.java: %v", importers)
	}
}

func TestBuildJavaIndexInnerClasses(t *testing.T) {
	root := t.TempDir()
	writeJavaFixture(t, filepath.Join(root, "src", "Outer.java"), `
package example;

public class Outer {
    public static class Inner {
        public void innerMethod() {}
    }
    public void outerMethod() {}
}
`)
	index, err := BuildJavaIndex(root)
	if err != nil || index == nil {
		t.Fatalf("BuildJavaIndex: %v", err)
	}
	file := index.FileByPath("src/Outer.java")
	if file == nil {
		t.Fatal("Outer.java not indexed")
	}
	var outer, inner, innerMethod, outerMethod *JavaSymbol
	for i := range file.Symbols {
		s := &file.Symbols[i]
		switch s.Name {
		case "Outer":
			outer = s
		case "Inner":
			inner = s
		case "innerMethod":
			innerMethod = s
		case "outerMethod":
			outerMethod = s
		}
	}
	if outer == nil || inner == nil || innerMethod == nil || outerMethod == nil {
		t.Fatalf("missing one of Outer/Inner/innerMethod/outerMethod: %+v", file.Symbols)
	}
	// Inner should be scoped under Outer.
	if inner.Scope != "Outer" {
		t.Fatalf("Inner.Scope = %q, want Outer", inner.Scope)
	}
	// innerMethod should be scoped under Outer.Inner.
	if innerMethod.Scope != "Outer.Inner" {
		t.Fatalf("innerMethod.Scope = %q, want Outer.Inner", innerMethod.Scope)
	}
	// outerMethod should be scoped under Outer (not Inner).
	if outerMethod.Scope != "Outer" {
		t.Fatalf("outerMethod.Scope = %q, want Outer", outerMethod.Scope)
	}
}

func writeJavaFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

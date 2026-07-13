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

var _ = Describe("Java workspace tools", func() {
	It("provides syntax-index discovery and navigation", func() {
		root := buildJavaTempWorkspace()
		manager := newLanguageWorkspaceManager(root, 30)
		Expect(languageDetects(root, "java")).To(BeTrue())
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_overview"}), "Indexed")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_find_symbol", Pattern: "Helper"}), "Helper.java")

		for query, output := range map[string]string{
			"public class Helper":                   "Helper",
			"public @interface FeatureFlag":         "FeatureFlag",
			"public sealed interface SealedService": "SealedService",
			"public void run()":                     "run",
			"private int compute()":                 "compute",
		} {
			expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{
				Tool: "java_find_symbol", Pattern: query,
			}), output)
		}

		outline := manager.Execute(context.Background(), workspace.ToolRequest{
			Tool: "java_file_outline", Path: filepath.Join("src", "main", "java", "com", "example", "App.java"),
		})
		expectToolSuccess(outline, "package: com.example", "com.example.util.Helper", "main", "App")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_entrypoints"}), "App.java")
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_related_files", Pattern: "helper"}).Error).To(BeEmpty())
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_implementations", Pattern: "Service"}), "ServiceImpl.java")
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_references", Pattern: "Helper", Path: "."}), "Helper")

		read := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_read_symbol", Pattern: "Helper#run"})
		Expect(read.Error).To(BeEmpty())
		Expect(read.Output).To(Or(ContainSubstring("Helper"), ContainSubstring("run")))
		for query, output := range map[string]string{
			"public class Helper":           "public class Helper",
			"public @interface FeatureFlag": "public @interface FeatureFlag",
			"public void run()":             "public void run()",
			"private int compute()":         "private int compute()",
		} {
			expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{
				Tool: "java_read_symbol", Pattern: query,
			}), output)
		}
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_calls_from", Pattern: "main"}).Error).To(BeEmpty())
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_calls_into", Pattern: "compute"}).Error).To(BeEmpty())
		expectToolSuccess(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_workspace_symbol", Pattern: "Helper"}), "Helper")
	})

	It("uses jdtls for precise operations when available", func() {
		if _, err := exec.LookPath("jdtls"); err != nil {
			Skip("jdtls not installed")
		}
		manager := newLanguageWorkspaceManager(buildJavaTempWorkspace(), 60)
		workspaceSymbols := manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_workspace_symbol", Pattern: "Helper"})
		Expect(workspaceSymbols.Error).To(BeEmpty())
		Expect(workspaceSymbols.Summary).To(ContainSubstring("jdtls"))
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_definition", Pattern: "Helper"}).Error).To(BeEmpty())
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_references", Pattern: "Helper", Path: "."}).Error).To(BeEmpty())
		Expect(manager.Execute(context.Background(), workspace.ToolRequest{Tool: "java_implementations", Pattern: "Service"}).Error).To(BeEmpty())
	})

	It("always returns a tagged diagnostics result", func() {
		result := newLanguageWorkspaceManager(buildJavaTempWorkspace(), 30).Execute(context.Background(), workspace.ToolRequest{Tool: "java_diagnostics"})
		Expect(result.Tool).To(Equal("java_diagnostics"))
	})
})

func buildJavaTempWorkspace() string {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	writeJavaFile(filepath.Join(root, "pom.xml"), `<?xml version="1.0"?>
<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>example</artifactId>
  <version>0.1.0</version>
</project>`)
	writeJavaFile(filepath.Join(root, "src", "main", "java", "com", "example", "App.java"), `
package com.example;

import com.example.util.Helper;

public class App {
    public static void main(String[] args) {
        Helper helper = new Helper();
        helper.run();
    }
}
`)
	writeJavaFile(filepath.Join(root, "src", "main", "java", "com", "example", "util", "Helper.java"), `
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
	writeJavaFile(filepath.Join(root, "src", "main", "java", "com", "example", "Service.java"), `
package com.example;

public interface Service {
    void execute();
}`)
	writeJavaFile(filepath.Join(root, "src", "main", "java", "com", "example", "FeatureFlag.java"), `
package com.example;

public @interface FeatureFlag {
    String value() default "";
}`)
	writeJavaFile(filepath.Join(root, "src", "main", "java", "com", "example", "SealedService.java"), `
package com.example;

public sealed interface SealedService permits SealedServiceImpl {
    void close();
}

final class SealedServiceImpl implements SealedService {
    public void close() {}
}
`)
	writeJavaFile(filepath.Join(root, "src", "main", "java", "com", "example", "ServiceImpl.java"), `
package com.example;

public class ServiceImpl implements Service {
    public void execute() {}
}`)
	writeJavaFile(filepath.Join(root, "src", "test", "java", "com", "example", "AppTest.java"), `
package com.example;

public class AppTest {
    public void smokeTest() {}
}`)
	return root
}

func writeJavaFile(path string, content string) {
	GinkgoHelper()
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644)).To(Succeed())
}

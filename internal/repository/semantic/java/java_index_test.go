package java

import (
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Java semantic index", func() {
	It("indexes types, methods, calls, imports, implementations, and entrypoints", func() {
		root := GinkgoT().TempDir()
		writeJavaFixture(filepath.Join(root, "src", "main", "java", "com", "example", "App.java"), `
package com.example;

import com.example.util.Helper;

public class App {
    public static void main(String[] args) {
        Helper helper = new Helper();
        helper.run();
    }
}
`)
		writeJavaFixture(filepath.Join(root, "src", "main", "java", "com", "example", "util", "Helper.java"), `
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
		writeJavaFixture(filepath.Join(root, "src", "main", "java", "com", "example", "Service.java"), `
package com.example;

public interface Service {
    void execute();
}
`)
		writeJavaFixture(filepath.Join(root, "src", "main", "java", "com", "example", "FeatureFlag.java"), `
package com.example;

public @interface FeatureFlag {
    String value() default "";
}
`)
		writeJavaFixture(filepath.Join(root, "src", "main", "java", "com", "example", "SealedService.java"), `
package com.example;

public sealed interface SealedService permits SealedServiceImpl {
    void close();
}

final class SealedServiceImpl implements SealedService {
    public void close() {
    }
}
`)
		writeJavaFixture(filepath.Join(root, "src", "main", "java", "com", "example", "ServiceImpl.java"), `
package com.example;

public class ServiceImpl implements Service {
    @Override
    public void execute() {
    }
}
`)
		writeJavaFixture(filepath.Join(root, "src", "test", "java", "com", "example", "AppTest.java"), `
package com.example;

import org.junit.Test;

public class AppTest {
    @Test
    public void smokeTest() {
    }
}
`)

		index, err := BuildJavaIndex(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(index).NotTo(BeNil())
		Expect(index.Files).To(HaveLen(7))

		app := index.FileByPath("src/main/java/com/example/App.java")
		Expect(app).NotTo(BeNil())
		Expect(app.HasMain).To(BeTrue())
		Expect(app.Package).To(Equal("com.example"))
		Expect(app.Imports).To(ContainElement("com.example.util.Helper"))

		matches := index.FindSymbols("Helper", 4)
		Expect(matches).NotTo(BeEmpty())

		foundHelper := false
		for _, match := range matches {
			if match.Name == "Helper" && match.Kind == "class" {
				foundHelper = true
				break
			}
		}
		Expect(foundHelper).To(BeTrue())

		for query, expected := range map[string]string{
			"public class Helper":                   "Helper",
			"public interface Service":              "Service",
			"public @interface FeatureFlag":         "FeatureFlag",
			"public sealed interface SealedService": "SealedService",
		} {
			matches = index.FindSymbols(query, 4)
			Expect(matches).NotTo(BeEmpty(), "query %q", query)
			Expect(matches[0].Name).To(Equal(expected))
		}
		matches = index.FindSymbols("public void run()", 4)
		Expect(matches).NotTo(BeEmpty())
		Expect(matches[0].Name).To(Equal("run"))
		Expect(matches[0].Path).To(HaveSuffix("Helper.java"))
		matches = index.FindSymbols("private int compute()", 4)
		Expect(matches).NotTo(BeEmpty())
		Expect(matches[0].Name).To(Equal("compute"))
		Expect(matches[0].Path).To(HaveSuffix("Helper.java"))

		implementations := index.Implementations("Service", 8)
		Expect(implementations).NotTo(BeEmpty())
		foundImplementation := false
		for _, implementation := range implementations {
			if implementation.Name == "ServiceImpl" {
				foundImplementation = true
				break
			}
		}
		Expect(foundImplementation).To(BeTrue())

		entries := index.Entrypoints(4)
		foundApp, foundTest := false, false
		for _, entry := range entries {
			if strings.HasSuffix(entry, "App.java") {
				foundApp = true
			}
			if strings.HasSuffix(entry, "AppTest.java") {
				foundTest = true
			}
		}
		Expect(foundApp).To(BeTrue())
		Expect(foundTest).To(BeTrue())
		Expect(index.FindCallsFrom("App#main", 16)).NotTo(BeEmpty())
		Expect(index.RelevantFiles([]string{"helper"}, 4)).NotTo(BeEmpty())

		importers := index.Reverse["src/main/java/com/example/util/Helper.java"]
		foundApp = false
		for _, importer := range importers {
			if strings.HasSuffix(importer, "App.java") {
				foundApp = true
				break
			}
		}
		Expect(foundApp).To(BeTrue())
	})

	It("tracks scopes for inner classes and their methods", func() {
		root := GinkgoT().TempDir()
		writeJavaFixture(filepath.Join(root, "src", "Outer.java"), `
package example;

public class Outer {
    public static class Inner {
        public void innerMethod() {}
    }
    public void outerMethod() {}
}
`)

		index, err := BuildJavaIndex(root)
		Expect(err).NotTo(HaveOccurred())
		Expect(index).NotTo(BeNil())

		file := index.FileByPath("src/Outer.java")
		Expect(file).NotTo(BeNil())

		var outer, inner, innerMethod, outerMethod *JavaSymbol

		for i := range file.Symbols {
			symbol := &file.Symbols[i]
			switch symbol.Name {
			case "Outer":
				outer = symbol
			case "Inner":
				inner = symbol
			case "innerMethod":
				innerMethod = symbol
			case "outerMethod":
				outerMethod = symbol
			}
		}
		Expect(outer).NotTo(BeNil())
		Expect(inner).NotTo(BeNil())
		Expect(innerMethod).NotTo(BeNil())
		Expect(outerMethod).NotTo(BeNil())
		Expect(inner.Scope).To(Equal("Outer"))
		Expect(innerMethod.Scope).To(Equal("Outer.Inner"))
		Expect(outerMethod.Scope).To(Equal("Outer"))
	})
})

func writeJavaFixture(path string, content string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644)).To(Succeed())
}

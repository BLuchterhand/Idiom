package semantic_test

import (
	semanticcpp "idiom/internal/repository/semantic/cpp"
	semanticgo "idiom/internal/repository/semantic/golang"
	semanticjava "idiom/internal/repository/semantic/java"
	semanticpython "idiom/internal/repository/semantic/python"
	semanticrust "idiom/internal/repository/semantic/rust"
	semanticts "idiom/internal/repository/semantic/typescript"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("FileByPath", func() {
	It("normalizes caller path separators across language indexes", func() {
		goIndex := &semanticgo.GoIndex{Files: []semanticgo.GoFile{{Path: "internal/surfaces/tui/app/app.go"}}}
		Expect(goIndex.FileByPath(` internal\surfaces\tui\app\app.go `)).To(HaveField("Path", "internal/surfaces/tui/app/app.go"))
		Expect(goIndex.FileByPath(`.\internal\surfaces\tui\app\app.go`)).To(HaveField("Path", "internal/surfaces/tui/app/app.go"))

		rustIndex := &semanticrust.RustIndex{Files: []semanticrust.RustFile{{Path: "src/lib.rs"}}}
		Expect(rustIndex.FileByPath(`src\lib.rs`)).To(HaveField("Path", "src/lib.rs"))

		javaIndex := &semanticjava.JavaIndex{Files: []semanticjava.JavaFile{{Path: "src/main/java/App.java"}}}
		Expect(javaIndex.FileByPath(`src\main\java\App.java`)).To(HaveField("Path", "src/main/java/App.java"))

		pythonIndex := &semanticpython.PythonIndex{Files: []semanticpython.PythonFile{{Path: "src/demo/core.py"}}}
		Expect(pythonIndex.FileByPath(`src\demo\core.py`)).To(HaveField("Path", "src/demo/core.py"))

		tsIndex := &semanticts.TsIndex{Files: []semanticts.TsFile{{Path: "src/app.ts"}}}
		Expect(tsIndex.FileByPath(`src\app.ts`)).To(HaveField("Path", "src/app.ts"))

		cppIndex := &semanticcpp.CppIndex{Files: []semanticcpp.CppFile{{Path: "src/native/app.cpp"}}}
		Expect(cppIndex.FileByPath(`src\native\app.cpp`)).To(HaveField("Path", "src/native/app.cpp"))
	})
})

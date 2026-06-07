package semantic

import (
	semanticcpp "idiom/internal/repository/semantic/cpp"
	semanticgo "idiom/internal/repository/semantic/golang"
	semanticjava "idiom/internal/repository/semantic/java"
	semanticpython "idiom/internal/repository/semantic/python"
	semanticrust "idiom/internal/repository/semantic/rust"
	semanticts "idiom/internal/repository/semantic/typescript"
)

type GoIndex = semanticgo.GoIndex
type GoFile = semanticgo.GoFile
type GoSymbol = semanticgo.GoSymbol
type GoCall = semanticgo.GoCall
type GoPackage = semanticgo.GoPackage
type SymbolMatch = semanticgo.SymbolMatch
type PackageMatch = semanticgo.PackageMatch
type CallMatch = semanticgo.CallMatch

type CppIndex = semanticcpp.CppIndex
type CppFile = semanticcpp.CppFile
type CppSymbol = semanticcpp.CppSymbol
type CppCall = semanticcpp.CppCall
type CppSymbolMatch = semanticcpp.CppSymbolMatch
type CppCallMatch = semanticcpp.CppCallMatch

type JavaIndex = semanticjava.JavaIndex
type JavaFile = semanticjava.JavaFile
type JavaSymbol = semanticjava.JavaSymbol
type JavaCall = semanticjava.JavaCall
type JavaSymbolMatch = semanticjava.JavaSymbolMatch
type JavaCallMatch = semanticjava.JavaCallMatch

type PythonIndex = semanticpython.PythonIndex
type PythonFile = semanticpython.PythonFile
type PythonSymbol = semanticpython.PythonSymbol
type PythonCall = semanticpython.PythonCall
type PythonSymbolMatch = semanticpython.PythonSymbolMatch
type PythonCallMatch = semanticpython.PythonCallMatch

type RustIndex = semanticrust.RustIndex
type RustFile = semanticrust.RustFile
type RustSymbol = semanticrust.RustSymbol
type RustCall = semanticrust.RustCall
type RustSymbolMatch = semanticrust.RustSymbolMatch
type RustCallMatch = semanticrust.RustCallMatch

type TsIndex = semanticts.TsIndex
type TsFile = semanticts.TsFile
type TsSymbol = semanticts.TsSymbol
type TsCall = semanticts.TsCall
type TsSymbolMatch = semanticts.TsSymbolMatch
type TsCallMatch = semanticts.TsCallMatch

func BuildGoIndex(root string) (*GoIndex, error) {
	return semanticgo.BuildGoIndex(root)
}

func BuildGoIndexWithOptions(root string, opts IndexOptions) (*GoIndex, error) {
	return semanticgo.BuildGoIndexWithOptions(root, opts)
}

func BuildCppIndex(root string) (*CppIndex, error) {
	return semanticcpp.BuildCppIndex(root)
}

func BuildCppIndexWithOptions(root string, opts IndexOptions) (*CppIndex, error) {
	return semanticcpp.BuildCppIndexWithOptions(root, opts)
}

func BuildJavaIndex(root string) (*JavaIndex, error) {
	return semanticjava.BuildJavaIndex(root)
}

func BuildJavaIndexWithOptions(root string, opts IndexOptions) (*JavaIndex, error) {
	return semanticjava.BuildJavaIndexWithOptions(root, opts)
}

func BuildPythonIndex(root string) (*PythonIndex, error) {
	return semanticpython.BuildPythonIndex(root)
}

func BuildPythonIndexWithOptions(root string, opts IndexOptions) (*PythonIndex, error) {
	return semanticpython.BuildPythonIndexWithOptions(root, opts)
}

func BuildRustIndex(root string) (*RustIndex, error) {
	return semanticrust.BuildRustIndex(root)
}

func BuildRustIndexWithOptions(root string, opts IndexOptions) (*RustIndex, error) {
	return semanticrust.BuildRustIndexWithOptions(root, opts)
}

func BuildTsIndex(root string) (*TsIndex, error) {
	return semanticts.BuildTsIndex(root)
}

func BuildTsIndexWithOptions(root string, opts IndexOptions) (*TsIndex, error) {
	return semanticts.BuildTsIndexWithOptions(root, opts)
}

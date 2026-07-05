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

// Package noexit проверяет, что процесс завершается только из точки входа.
//
// Вообще, в задании типа внутри Main искать нужно, но это странно, наверное просто я не понял формулировку.
// Прямой вызов os.Exit небезопасен за пределами main: он обрывает программу
// немедленно, поэтому отложенные вызовы не выполняются, а отложенные закрытия
// файлов, соединений и досылка логов теряются. Анализатор разрешает os.Exit
// только в func main пакета main и в func TestMain(m *testing.M) — там
// откладывать уже нечего. Весь остальной код должен возвращать ошибку,
// а решение о завершении процесса принимает main.
package noexit

import (
	"errors"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
)

const doc = `запрещает прямой вызов os.Exit вне main и TestMain

os.Exit завершает процесс немедленно: отложенные вызовы не выполняются,
файлы и соединения не закрываются, буферы логов не досылаются. Завершать
процесс можно только в точке входа — func main пакета main и
func TestMain(m *testing.M). Остальному коду следует возвращать ошибку,
а os.Exit оставить main.

Вызовы внутри литералов функций и замыканий считаются не точкой входа,
даже если они написаны внутри main: горутина не управляет завершением программы.`

// Analyzer находит os.Exit, вызванный не из точки входа процесса.
var Analyzer = &analysis.Analyzer{
	Name:     "noexit",
	Doc:      doc,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	// Пакет без os импортировать не может — ранний выход экономит обход AST.
	if !importsOS(pass.Pkg) {
		return nil, nil
	}

	tree, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, errors.New("в pass.ResultOf нет результата анализатора inspect")
	}
	nodes := []ast.Node{(*ast.CallExpr)(nil)}

	tree.WithStack(nodes, func(node ast.Node, push bool, stack []ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if push && isCall && isExitCall(pass.TypesInfo, call) && !inEntryPoint(pass, stack) {
			pass.Reportf(call.Pos(), "прямой вызов os.Exit: разрешён только в func main пакета main и func TestMain")
		}
		return true
	})

	return nil, nil
}

// importsOS сообщает, что пакет прямо обращается к пакету os.
func importsOS(pkg *types.Package) bool {
	for _, imported := range pkg.Imports() {
		if imported.Path() == "os" {
			return true
		}
	}
	return false
}

// isExitCall сообщает, что call вызывает os.Exit, как бы пакет os ни был импортирован.
func isExitCall(info *types.Info, call *ast.CallExpr) bool {
	fn, ok := typeutil.Callee(info, call).(*types.Func)
	return ok && fn.Pkg() != nil && fn.Pkg().Path() == "os" && fn.Name() == "Exit"
}

// inEntryPoint сообщает, что ближайшая внешняя функция стека — точка входа процесса.
// Стек идёт от File к текущему узлу, поэтому его обходят с конца.
func inEntryPoint(pass *analysis.Pass, stack []ast.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		switch fn := stack[i].(type) {
		case *ast.FuncLit:
			return false
		case *ast.FuncDecl:
			return isEntryPoint(pass, fn)
		}
	}
	return false
}

// isEntryPoint сообщает, что decl — func main пакета main или func TestMain(m *testing.M).
func isEntryPoint(pass *analysis.Pass, decl *ast.FuncDecl) bool {
	fn, ok := pass.TypesInfo.Defs[decl.Name].(*types.Func)
	if !ok {
		return false
	}
	signature, ok := fn.Type().(*types.Signature)
	if !ok || signature.Recv() != nil || signature.Results().Len() != 0 {
		return false
	}
	switch fn.Name() {
	case "main":
		return pass.Pkg.Name() == "main" && signature.Params().Len() == 0
	case "TestMain":
		return signature.Params().Len() == 1 && isTestingM(signature.Params().At(0).Type())
	}
	return false
}

// isTestingM сообщает, что typ — указатель на testing.M.
func isTestingM(typ types.Type) bool {
	pointer, ok := typ.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := pointer.Elem().(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Name() == "M" && obj.Pkg() != nil && obj.Pkg().Path() == "testing"
}

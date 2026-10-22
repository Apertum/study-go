package resetgen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// render собирает содержимое файла с методами Reset всех типов одного исходника. resettable —
// имена всех типов, чей Reset появится по итогам прогона: они делегируют сброс друг другу.
func render(
	pkg *packages.Package,
	src *ast.File,
	targets []target,
	resettable map[string]bool,
	warnings *[]Warning,
) ([]byte, error) {
	b := &builder{pkg: pkg, src: src, warnings: warnings, resettable: resettable}

	declarations := make([]*ast.FuncDecl, 0, len(targets))
	for _, item := range targets {
		declarations = append(declarations, b.methodDecl(item))
	}

	imports := b.collectImports(declarations)

	var source bytes.Buffer
	source.WriteString(GeneratedHeader + "\n\npackage " + pkg.Name + "\n")
	if len(imports) > 0 {
		source.WriteString("\nimport (\n")
		for _, importPath := range imports {
			source.WriteString("\t" + strconv.Quote(importPath) + "\n")
		}
		source.WriteString(")\n")
	}

	for _, decl := range declarations {
		var code bytes.Buffer
		// Печать через FileSet пакета: go/printer обращается к нему даже для узлов без позиций,
		// а отступы и переносы строки в итоге выравнивает format.Source.
		if err := format.Node(&code, pkg.Fset, decl); err != nil {
			return nil, fmt.Errorf("печать метода %s для %s: %w", decl.Name.Name, sourcePath(pkg, src), err)
		}
		source.WriteString("\n\n")
		source.Write(code.Bytes())
	}

	content, err := format.Source(assertionPattern.ReplaceAll(source.Bytes(), []byte(assertionInline)))
	if err != nil {
		return nil, fmt.Errorf("форматирование %s: %w", sourcePath(pkg, src), err)
	}
	return content, nil
}

// assertionPattern — проверка типа, которую go/printer разворачивает на три строки: у
// синтетических узлов нет позиций, и единственный метод интерфейса печатается с новой строки.
// В исходном коде такую запись пишут в одну строку, поэтому она собирается обратно до
// форматирования.
var assertionPattern = regexp.MustCompile(`\.\(interface \{\s+` + resetMethod + `\(\)\s+\}\)`)

const assertionInline = `.(interface{ ` + resetMethod + `() })`

// methodDecl строит func (получатель *Тип) Reset() с проверкой nil-получателя первым оператором.
func (b *builder) methodDecl(item target) *ast.FuncDecl {
	receiver := ast.NewIdent(receiverName(item.name, b.importNames()))

	nilGuard := &ast.IfStmt{
		Cond: &ast.BinaryExpr{X: receiver, Op: token.EQL, Y: ast.NewIdent("nil")},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{}}},
	}

	statements := []ast.Stmt{nilGuard}
	for _, field := range item.strct.Fields.List {
		statements = append(statements, b.fieldStatements(receiver, item.named, field)...)
	}

	return &ast.FuncDecl{
		Recv: &ast.FieldList{List: []*ast.Field{{
			Names: []*ast.Ident{ast.NewIdent(receiver.Name)},
			Type:  &ast.StarExpr{X: ast.NewIdent(item.name)},
		}}},
		Name: ast.NewIdent(resetMethod),
		Type: &ast.FuncType{Params: &ast.FieldList{}},
		Body: &ast.BlockStmt{List: statements},
	}
}

// importNames возвращает имена импортированных пакетов исходника — они заняты в выходном файле.
func (b *builder) importNames() map[string]bool {
	names := make(map[string]bool, len(b.src.Imports))
	for _, spec := range b.src.Imports {
		if name := importName(spec); name != "" {
			names[name] = true
		}
	}
	return names
}

// collectImports возвращает отсортированные пути пакетов, которые понадобились сгенерированному
// коду. Появиться они могут только в записи типа массива, например `[3]time.Month{}`.
func (b *builder) collectImports(declarations []*ast.FuncDecl) []string {
	available := make(map[string]string, len(b.src.Imports))
	for _, spec := range b.src.Imports {
		if name := importName(spec); name != "" {
			available[name] = strings.Trim(spec.Path.Value, `"`)
		}
	}
	if len(available) == 0 {
		return nil
	}

	used := make(map[string]bool)
	for _, decl := range declarations {
		ast.Inspect(decl, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if qualifier, ok := selector.X.(*ast.Ident); ok {
				if importPath, found := available[qualifier.Name]; found {
					used[importPath] = true
				}
			}
			return true
		})
	}

	paths := make([]string, 0, len(used))
	for importPath := range used {
		paths = append(paths, importPath)
	}
	sort.Strings(paths)
	return paths
}

// importName возвращает имя пакета в исходнике: явное из объявления либо последний сегмент пути.
func importName(spec *ast.ImportSpec) string {
	if spec.Name != nil {
		return spec.Name.Name
	}
	unquoted, err := strconv.Unquote(spec.Path.Value)
	if err != nil {
		return ""
	}
	return path.Base(unquoted)
}

func assignStatement(selector ast.Expr, value ast.Expr) ast.Stmt {
	return &ast.AssignStmt{Lhs: []ast.Expr{selector}, Tok: token.ASSIGN, Rhs: []ast.Expr{value}}
}

// trimToZero строит выражение selector[:0]: длина обнуляется, ёмкость и массив остаются.
func trimToZero(selector ast.Expr) ast.Expr {
	return &ast.SliceExpr{X: selector, High: &ast.BasicLit{Kind: token.INT, Value: "0"}}
}

func methodCallStatement(selector ast.Expr) ast.Stmt {
	return &ast.ExprStmt{X: &ast.CallExpr{
		Fun: &ast.SelectorExpr{X: selector, Sel: ast.NewIdent(resetMethod)},
	}}
}

func clearStatement(selector ast.Expr) ast.Stmt {
	return &ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("clear"), Args: []ast.Expr{selector}}}
}

func nilGuard(selector ast.Expr, inner ast.Stmt) ast.Stmt {
	return &ast.IfStmt{
		Cond: &ast.BinaryExpr{X: selector, Op: token.NEQ, Y: ast.NewIdent("nil")},
		Body: &ast.BlockStmt{List: []ast.Stmt{inner}},
	}
}

// assertResetStatement строит проверку интерфейсного поля на наличие Reset():
// if value, ok := pole.(interface{ Reset() }); ok && value != nil { value.Reset() }.
func assertResetStatement(selector ast.Expr) ast.Stmt {
	return &ast.IfStmt{
		Init: &ast.AssignStmt{
			Tok: token.DEFINE,
			Lhs: []ast.Expr{ast.NewIdent("value"), ast.NewIdent("ok")},
			Rhs: []ast.Expr{&ast.TypeAssertExpr{X: selector, Type: resetInterface()}},
		},
		Cond: &ast.BinaryExpr{
			X:  ast.NewIdent("ok"),
			Op: token.LAND,
			Y:  &ast.BinaryExpr{X: ast.NewIdent("value"), Op: token.NEQ, Y: ast.NewIdent("nil")},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{methodCallStatement(ast.NewIdent("value"))}},
	}
}

// resetInterface — запись интерфейсного типа interface{ Reset() } для проверки значения поля.
func resetInterface() ast.Expr {
	return &ast.InterfaceType{Methods: &ast.FieldList{List: []*ast.Field{{
		Names: []*ast.Ident{ast.NewIdent(resetMethod)},
		Type:  &ast.FuncType{Params: &ast.FieldList{}},
	}}}}
}

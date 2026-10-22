package resetgen

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

// builder строит дерево разбора метода Reset для одного исходного файла и собирает
// предупреждения о пропущенных полях.
type builder struct {
	pkg      *packages.Package
	src      *ast.File
	warnings *[]Warning
	// resettable — ключи типов, чей метод Reset будет создан этим прогоном. Без него делегировать
	// сброс дочернему типу нельзя: в исходнике этого метода ещё нет.
	resettable map[string]bool
}

func (b *builder) warn(pos token.Pos, format string, args ...any) {
	warn(b.warnings, b.pkg.Fset, pos, format, args...)
}

// fieldStatements возвращает операторы сброса объявления поля. Имена в одном объявлении
// (`a, b int`) получают собственный код, поэтому выражения строятся заново для каждого имени.
func (b *builder) fieldStatements(receiver *ast.Ident, owner *types.Named, field *ast.Field) []ast.Stmt {
	fieldType := b.pkg.TypesInfo.TypeOf(field.Type)
	if fieldType == nil {
		b.warn(field.Pos(), "не удалось определить тип поля: поле не сброшено")
		return nil
	}

	var statements []ast.Stmt
	for _, name := range fieldNames(field) {
		if name == "_" {
			continue
		}
		selector := &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent(name)}
		if statement, ok := b.resetStatement(selector, fieldType, field.Type, field.Pos(), owner, name); ok {
			statements = append(statements, statement)
		}
	}
	return statements
}

// resetStatement выбирает код сброса по статическому типу поля: сначала делегирование чужому
// Reset, затем снятие указателей, затем правило по базовому виду. Второй вариант возвращает false,
// если поле пропущено с предупреждением.
func (b *builder) resetStatement(
	selector ast.Expr,
	fieldType types.Type,
	source ast.Expr,
	pos token.Pos,
	owner *types.Named,
	name string,
) (ast.Stmt, bool) {
	typeName := types.TypeString(fieldType, types.RelativeTo(b.pkg.Types))

	named := namedOf(fieldType)

	if named != nil && named.TypeParams() != nil {
		b.warn(pos, "поле %q имеет параметризованный тип %s: сброс для него не определён", name, typeName)
		return nil, false
	}

	if named != nil && (b.resettable[typeKey(named)] || b.hasResetMethod(fieldType)) {
		if typeKey(named) == typeKey(owner) {
			b.warn(pos, "поле %q ссылается на тип %s: сброс корректен для дерева и списка, "+
				"но уйдёт в бесконечную рекурсию на циклическом графе", name, owner.Obj().Name())
		}
		call := methodCallStatement(selector)
		if _, isPointer := pointerElem(fieldType); isPointer {
			return nilGuard(selector, call), true
		}
		return call, true
	}

	if elem, isPointer := pointerElem(fieldType); isPointer {
		inner, ok := b.resetStatement(&ast.StarExpr{X: selector}, elem, starInner(source), pos, owner, name)
		if !ok {
			return nil, false
		}
		return nilGuard(selector, inner), true
	}

	switch underlying := types.Unalias(fieldType).Underlying().(type) {
	case *types.Basic:
		zero, ok := zeroLiteral(underlying)
		if !ok {
			b.warn(pos, "поле %q имеет тип %s: сброс для него не определён", name, typeName)
			return nil, false
		}
		return assignStatement(selector, zero), true

	case *types.Slice:
		return assignStatement(selector, trimToZero(selector)), true

	case *types.Map:
		return clearStatement(selector), true

	case *types.Chan, *types.Signature:
		return assignStatement(selector, ast.NewIdent("nil")), true

	case *types.Array:
		typeExpr := b.cloneTypeExpr(source)
		if typeExpr == nil {
			b.warn(pos, "поле %q имеет тип %s: не удалось восстановить запись типа для нулевого литерала", name, typeName)
			return nil, false
		}
		return assignStatement(selector, &ast.CompositeLit{Type: typeExpr}), true

	case *types.Interface:
		return assertResetStatement(selector), true

	case *types.Struct:
		b.warn(pos, "поле %q имеет тип %s без метода %s: поле не сброшено, развёртка его полей разрушила бы инкапсуляцию",
			name, typeName, resetMethod)
		return nil, false

	default:
		b.warn(pos, "поле %q имеет неподдерживаемый тип %s: поле не сброшено", name, typeName)
		return nil, false
	}
}

// hasResetMethod сообщает, что у значения поля можно вызвать Reset() без аргументов и результата.
// Поле доступно по адресу (получатель метода — указатель), поэтому проверяется набор методов
// адресного значения: в него попадают и методы с получателем T, и методы с получателем *T.
func (b *builder) hasResetMethod(fieldType types.Type) bool {
	found, _, _ := types.LookupFieldOrMethod(fieldType, true, b.pkg.Types, resetMethod)

	method, ok := found.(*types.Func)
	if !ok {
		return false
	}
	signature, ok := method.Type().(*types.Signature)
	return ok && signature.Params().Len() == 0 && signature.Results().Len() == 0
}

// pointerElem возвращает тип, на который указывает значение, включая именованные типы-указатели.
func pointerElem(fieldType types.Type) (types.Type, bool) {
	switch typ := types.Unalias(fieldType).(type) {
	case *types.Pointer:
		return typ.Elem(), true
	case *types.Named:
		if pointer, ok := typ.Underlying().(*types.Pointer); ok {
			return pointer.Elem(), true
		}
		return nil, false
	default:
		return nil, false
	}
}

// starInner снимает один уровень указателя с синтаксической записи типа. nil возвращается, когда
// снять нечего: например для псевдонима вида `type Ref *int`, где синтаксис указателя в другом месте.
func starInner(source ast.Expr) ast.Expr {
	star, ok := source.(*ast.StarExpr)
	if !ok {
		return nil
	}
	return star.X
}

// fieldNames возвращает имена полей объявления; для встроенного поля именем служит имя типа.
func fieldNames(field *ast.Field) []string {
	if len(field.Names) > 0 {
		names := make([]string, 0, len(field.Names))
		for _, ident := range field.Names {
			names = append(names, ident.Name)
		}
		return names
	}
	if name := embeddedName(field.Type); name != "" {
		return []string{name}
	}
	return nil
}

// embeddedName извлекает имя встроенного типа из записи `Base`, `pkg.Base`, `*Base`, `Base[T]`.
func embeddedName(expr ast.Expr) string {
	switch typ := expr.(type) {
	case *ast.Ident:
		return typ.Name
	case *ast.SelectorExpr:
		return typ.Sel.Name
	case *ast.StarExpr:
		return embeddedName(typ.X)
	case *ast.IndexExpr:
		return embeddedName(typ.X)
	case *ast.IndexListExpr:
		return embeddedName(typ.X)
	default:
		return ""
	}
}

// cloneTypeExpr пересобирает запись типа из исходника в отдельное дерево без позиций исходного
// файла, чтобы одно и то же дерево не попадало в два места результата.
func (b *builder) cloneTypeExpr(source ast.Expr) ast.Expr {
	if source == nil {
		return nil
	}

	var printed bytes.Buffer
	if err := printer.Fprint(&printed, b.pkg.Fset, source); err != nil {
		return nil
	}
	clone, err := parser.ParseExpr(printed.String())
	if err != nil {
		return nil
	}
	return clone
}

// zeroLiteral возвращает нулевое значение скалярного вида. Для unsafe.Pointer и ошибочных типов
// нулевого значения нет — второй результат false.
func zeroLiteral(basic *types.Basic) (ast.Expr, bool) {
	switch basic.Kind() {
	case types.Bool:
		return ast.NewIdent("false"), true
	case types.String:
		return &ast.BasicLit{Kind: token.STRING, Value: `""`}, true
	case types.Invalid, types.UnsafePointer:
		return nil, false
	}

	if basic.Info()&types.IsComplex != 0 {
		return &ast.BasicLit{Kind: token.IMAG, Value: "0i"}, true
	}
	return &ast.BasicLit{Kind: token.INT, Value: "0"}, true
}

// receiverName собирает имя получателя из первых букв составных слов имени типа:
// ResetableStruct -> rs, Session -> s. Пустое имя и имя, совпадающее с импортированным пакетом,
// заменяется на v.
func receiverName(typeName string, reserved map[string]bool) string {
	var name strings.Builder
	for index, letter := range typeName {
		if index > 0 && !unicode.IsUpper(letter) {
			continue
		}
		name.WriteRune(unicode.ToLower(letter))
	}

	candidate := name.String()
	if candidate == "" || reserved[candidate] {
		return "v"
	}
	return candidate
}

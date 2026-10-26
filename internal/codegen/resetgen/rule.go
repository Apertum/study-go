package resetgen

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/tools/go/packages"
)

// builder описывает операторы сброса для одного исходного файла и собирает предупреждения о
// пропущенных полях.
type builder struct {
	pkg      *packages.Package
	warnings *[]Warning
	// resettable — ключи типов, чей метод Reset будет создан этим прогоном. Без него делегировать
	// сброс дочернему типу нельзя: в исходнике этого метода ещё нет.
	resettable map[string]bool
	// imports — имена импортированных пакетов исходника и их пути. Имена заняты в выходном файле,
	// а пути нужны, чтобы вернуть вместе с кодом те из них, что попали в нулевой литерал массива.
	imports map[string]string
	// reserved — те же имена, что заняты в выходном файле: они не подходят на роль получателя.
	reserved map[string]bool
	// used — пути импортов, которые понадобились сгенерированному коду.
	used map[string]bool
}

// newBuilder готовит описание операторов одного исходника: имена импортов исходника учитываются
// как занятые и как возможные обращения выходного кода.
func newBuilder(
	pkg *packages.Package,
	src *ast.File,
	warnings *[]Warning,
	resettable map[string]bool,
) *builder {
	imports := make(map[string]string, len(src.Imports))
	for _, spec := range src.Imports {
		if name := importName(spec); name != "" {
			imports[name] = strings.Trim(spec.Path.Value, `"`)
		}
	}

	reserved := make(map[string]bool, len(imports))
	for name := range imports {
		reserved[name] = true
	}

	return &builder{
		pkg:        pkg,
		warnings:   warnings,
		resettable: resettable,
		imports:    imports,
		reserved:   reserved,
		used:       make(map[string]bool),
	}
}

func (b *builder) warn(pos token.Pos, format string, args ...any) {
	warn(b.warnings, b.pkg.Fset, pos, format, args...)
}

// fieldStatements возвращает операторы сброса объявления поля. Имена в одном объявлении
// (`a, b int`) получают собственный код, поэтому выражения строятся заново для каждого имени.
func (b *builder) fieldStatements(receiver string, owner *types.Named, field *ast.Field) []statement {
	fieldType := b.pkg.TypesInfo.TypeOf(field.Type)
	if fieldType == nil {
		b.warn(field.Pos(), "не удалось определить тип поля: поле не сброшено")
		return nil
	}

	var statements []statement
	for _, name := range fieldNames(field) {
		if name == "_" {
			continue
		}
		if item, ok := b.resetStatement(receiver+"."+name, fieldType, field.Type, field.Pos(), owner, name); ok {
			statements = append(statements, item)
		}
	}
	return statements
}

// resetStatement выбирает код сброса по статическому типу поля: сначала делегирование чужому
// Reset, затем снятие указателей, затем правило по базовому виду. Второй результат возвращает false,
// если поле пропущено с предупреждением.
func (b *builder) resetStatement(
	target string,
	fieldType types.Type,
	source ast.Expr,
	pos token.Pos,
	owner *types.Named,
	name string,
) (statement, bool) {
	typeName := types.TypeString(fieldType, types.RelativeTo(b.pkg.Types))

	named := namedOf(fieldType)

	if named != nil && named.TypeParams() != nil {
		b.warn(pos, "поле %q имеет параметризованный тип %s: сброс для него не определён", name, typeName)
		return statement{}, false
	}

	if named != nil && (b.resettable[typeKey(named)] || b.hasResetMethod(fieldType)) {
		if typeKey(named) == typeKey(owner) {
			b.warn(pos, "поле %q ссылается на тип %s: сброс корректен для дерева и списка, "+
				"но уйдёт в бесконечную рекурсию на циклическом графе", name, owner.Obj().Name())
		}
		call := statement{Kind: kindReset, Target: target}
		if _, isPointer := pointerElem(fieldType); isPointer {
			return guard(target, call), true
		}
		return call, true
	}

	if elem, isPointer := pointerElem(fieldType); isPointer {
		inner, ok := b.resetStatement("*"+target, elem, starInner(source), pos, owner, name)
		if !ok {
			return statement{}, false
		}
		return guard(target, inner), true
	}

	switch underlying := types.Unalias(fieldType).Underlying().(type) {
	case *types.Basic:
		zero, ok := zeroLiteral(underlying)
		if !ok {
			b.warn(pos, "поле %q имеет тип %s: сброс для него не определён", name, typeName)
			return statement{}, false
		}
		return statement{Kind: kindAssign, Target: target, Value: zero}, true

	case *types.Slice:
		return statement{Kind: kindTrim, Target: target}, true

	case *types.Map:
		return statement{Kind: kindClear, Target: target}, true

	case *types.Chan, *types.Signature:
		return statement{Kind: kindAssign, Target: target, Value: "nil"}, true

	case *types.Array:
		array, ok := b.typeText(source)
		if !ok {
			b.warn(pos, "поле %q имеет тип %s: не удалось восстановить запись типа для нулевого литерала", name, typeName)
			return statement{}, false
		}
		return statement{Kind: kindAssign, Target: target, Value: array + "{}"}, true

	case *types.Interface:
		return statement{Kind: kindAssert, Target: target}, true

	case *types.Struct:
		b.warn(pos, "поле %q имеет тип %s без метода %s: поле не сброшено, развёртка его полей разрушила бы инкапсуляцию",
			name, typeName, resetMethod)
		return statement{}, false

	default:
		b.warn(pos, "поле %q имеет неподдерживаемый тип %s: поле не сброшено", name, typeName)
		return statement{}, false
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

// typeText возвращает синтаксическую запись типа из исходника — она попадает в нулевой литерал
// массива — и отмечает импорты, без которых эта запись в выходном файле не соберётся. Записи нет,
// когда тип снят с указателя, у которого нечего снимать (псевдоним вида `type Ref *int`).
func (b *builder) typeText(source ast.Expr) (string, bool) {
	if source == nil {
		return "", false
	}

	var printed bytes.Buffer
	if err := printer.Fprint(&printed, b.pkg.Fset, source); err != nil {
		return "", false
	}

	for _, match := range qualifierPattern.FindAllStringSubmatch(printed.String(), -1) {
		if importPath, found := b.imports[match[1]]; found {
			b.used[importPath] = true
		}
	}
	return printed.String(), true
}

// qualifierPattern находит в записи типа обращения к пакету: имя идентификатора перед точкой, не
// стоящей после точки (иначе это выбор поля, а не квалификатор пакета).
var qualifierPattern = regexp.MustCompile(`(?:^|[^.\w])([A-Za-z_]\w*)\.`)

// zeroLiteral возвращает нулевое значение скалярного вида. Для unsafe.Pointer и ошибочных типов
// нулевого значения нет — второй результат false.
func zeroLiteral(basic *types.Basic) (string, bool) {
	switch basic.Kind() {
	case types.Bool:
		return "false", true
	case types.String:
		return `""`, true
	case types.Invalid, types.UnsafePointer:
		return "", false
	}

	if basic.Info()&types.IsComplex != 0 {
		return "0i", true
	}
	return "0", true
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

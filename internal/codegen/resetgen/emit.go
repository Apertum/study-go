package resetgen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"path"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"golang.org/x/tools/go/packages"
)

// Формы выходного кода: файл целиком, один метод и один оператор сброса. Итоговые отступы и
// переносы выравнивает format.Source, поэтому шаблон описывает только синтаксис.
const (
	// fileFormat — форма выходного файла: преамбула, пакет, импорты и методы в порядке объявления.
	fileFormat = `{{- define "file" -}}
` + GeneratedHeader + `

package {{ .Package }}
{{ with .Imports }}
import (
{{- range . }}
	{{ printf "%q" . }}
{{- end }}
)
{{- end }}
{{ range .Methods }}
{{ template "method" . }}
{{ end }}
{{- end -}}`

	// methodFormat — форма метода: func (получатель *Тип) Reset() с проверкой nil-получателя
	// первым оператором, далее операторы полей в порядке объявления.
	methodFormat = `{{- define "method" -}}
func ({{ .Receiver }} *{{ .Type }}) Reset() {
	if {{ .Receiver }} == nil {
		return
	}
{{- range .Statements }}
	{{ template "statement" . }}
{{- end }}}
{{- end -}}`

	// statementFormat — форма оператора сброса: по ветке на вид поля из таблицы правил, включая
	// вложенные проверки указателей. Имена веток совпадают с константами kind*. Операнд перед срезом,
	// проверкой типа и вызовом метода берётся из operand: без скобок они применились бы к указателю.
	statementFormat = `{{- define "statement" -}}
{{- if eq .Kind "assign" -}}
{{ .Target }} = {{ .Value }}
{{- else if eq .Kind "trim" -}}
{{ .Target }} = {{ .Target | operand }}[:0]
{{- else if eq .Kind "clear" -}}
clear({{ .Target }})
{{- else if eq .Kind "reset" -}}
{{ .Target | operand }}.Reset()
{{- else if eq .Kind "assert" -}}
if value, ok := {{ .Target | operand }}.(interface{ Reset() }); ok && value != nil {
	value.Reset()
}
{{- else if eq .Kind "guard" -}}
if {{ .Target }} != nil {
{{- range .Inner }}
	{{ template "statement" . }}
{{- end }}}
{{- end -}}
{{- end -}}`
)

// emitter собирает выходной файл из форм выше; шаблоны статичны, поэтому собираются один раз.
var emitter = template.Must(template.New("reset").Funcs(template.FuncMap{
	"operand": derefOperand,
}).Parse(strings.Join([]string{fileFormat, methodFormat, statementFormat}, "\n")))

// derefOperand оборачивает в скобки разыменованное выражение: срез x[:0], проверка типа x.(T) и
// вызов x.M() связываются сильнее разыменования, поэтому без скобок они достали бы до указателя,
// а не до значения по нему. Присваивание и аргумент clear() скобок не требуют.
func derefOperand(expr string) string {
	if strings.HasPrefix(expr, "*") {
		return "(" + expr + ")"
	}
	return expr
}

// fileData — данные шаблона file: содержимое одного выходного файла.
type fileData struct {
	Package string
	Imports []string
	Methods []method
}

// method — данные шаблона method: метод Reset одного помеченного типа.
type method struct {
	Receiver   string
	Type       string
	Statements []statement
}

// statement — данные шаблона statement: один оператор сброса. Kind выбирает ветку шаблона, Target
// — сбрасываемое выражение, Value — нулевое значение, Inner — операторы внутри nil-проверки.
type statement struct {
	Kind   string
	Target string
	Value  string
	Inner  []statement
}

// Вид оператора — имя ветки шаблона statement.
const (
	kindAssign = "assign"
	kindTrim   = "trim"
	kindClear  = "clear"
	kindReset  = "reset"
	kindAssert = "assert"
	kindGuard  = "guard"
)

// guard оборачивает операторы в проверку указателя на nil: адрес поля сохраняется, сбрасывается
// значение по указателю.
func guard(target string, inner ...statement) statement {
	return statement{Kind: kindGuard, Target: target, Inner: inner}
}

// render собирает содержимое файла с методами Reset всех типов одного исходника. resettable —
// имена всех типов, чей Reset появится по итогам прогона: они делегируют сброс друг другу.
func render(
	pkg *packages.Package,
	src *ast.File,
	targets []target,
	resettable map[string]bool,
	warnings *[]Warning,
) ([]byte, error) {
	b := newBuilder(pkg, src, warnings, resettable)

	methods := make([]method, 0, len(targets))
	for _, item := range targets {
		methods = append(methods, b.methodDecl(item))
	}

	data := fileData{Package: pkg.Name, Imports: b.importPaths(), Methods: methods}

	var source bytes.Buffer
	if err := emitter.ExecuteTemplate(&source, "file", data); err != nil {
		return nil, fmt.Errorf("рендер %s: %w", sourcePath(pkg, src), err)
	}

	content, err := format.Source(source.Bytes())
	if err != nil {
		return nil, fmt.Errorf("форматирование %s: %w", sourcePath(pkg, src), err)
	}
	return content, nil
}

// methodDecl описывает метод Reset типа: имя получателя и операторы сброса всех его полей.
func (b *builder) methodDecl(item target) method {
	receiver := receiverName(item.name, b.reserved)

	statements := make([]statement, 0, len(item.strct.Fields.List))
	for _, field := range item.strct.Fields.List {
		statements = append(statements, b.fieldStatements(receiver, item.named, field)...)
	}

	return method{Receiver: receiver, Type: item.name, Statements: statements}
}

// importPaths возвращает отсортированные пути пакетов, которые понадобились сгенерированному
// коду. Появиться они могут только в записи типа массива, например `[3]time.Month{}`.
func (b *builder) importPaths() []string {
	paths := make([]string, 0, len(b.used))
	for importPath := range b.used {
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

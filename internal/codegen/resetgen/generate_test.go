package resetgen

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

var update = flag.Bool("update", false, "перезаписать golden-файлы текущим выводом генератора")

// loadFixture грузит фикстурный пакет из testdata/src/<name>.
func loadFixture(t *testing.T, name string) *packages.Package {
	t.Helper()

	pkgs, err := Load(filepath.Join("testdata", "src"), "./"+name)
	if err != nil {
		t.Fatalf("загрузка фикстуры %s: %v", name, err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("фикстура %s: ожидался один пакет, получено %d", name, len(pkgs))
	}
	return pkgs[0]
}

// generateFixture возвращает результат генерации фикстуры целиком: единственный выходной файл
// и предупреждения.
func generateFixture(t *testing.T, name string) (File, []Warning) {
	t.Helper()

	files, warnings, err := Generate([]*packages.Package{loadFixture(t, name)})
	if err != nil {
		t.Fatalf("генерация фикстуры %s: %v", name, err)
	}
	if len(files) != 1 {
		t.Fatalf("фикстура %s: ожидался один выходной файл, получено %d", name, len(files))
	}
	return files[0], warnings
}

func TestGenerateGolden(t *testing.T) {
	for _, fixture := range []string{"simple", "kinds", "nesting", "conflicts", "markers"} {
		t.Run(fixture, func(t *testing.T) {
			file, _ := generateFixture(t, fixture)

			if _, err := parser.ParseFile(token.NewFileSet(), filepath.Base(file.Path), file.Content, parser.AllErrors); err != nil {
				t.Fatalf("сгенерированный код не разбирается: %v\n%s", err, file.Content)
			}
			assertGolden(t, fixture, file.Content)
		})
	}
}

func TestWarningsAndSkippedFields(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		strings []string
		skip    []string
	}{
		{
			name:    "unsafe.Pointer пропущен",
			fixture: "kinds",
			strings: []string{`поле "pointer"`},
			skip:    []string{"pointer"},
		},
		{
			name:    "структура без Reset и параметризованный тип пропущены",
			fixture: "kinds",
			strings: []string{
				`поле "plain" имеет тип`,
				`ext.Plain без метода Reset`,
				`поле "pair" имеет параметризованный тип`,
			},
			skip: []string{"plain", "pair"},
		},
		{
			name:    "структуры без Reset пропущены, самоссылка помечена",
			fixture: "nesting",
			strings: []string{
				`поле "plain"`,
				`поле "mutex"`,
				`поле "next" ссылается на тип Outer`,
			},
			skip: []string{"plain", "mutex"},
		},
		{
			name:    "готовый метод и не-структура не порождают код",
			fixture: "conflicts",
			strings: []string{
				"тип ManualValue уже объявляет метод Reset",
				"тип ManualPtr уже объявляет метод Reset",
				"директива generate:reset над типом NotStruct",
				"директива generate:reset над типом Runner",
				"директива generate:reset над типом Status",
				"директива generate:reset над блоком объявлений",
				"тип Generic параметризован",
			},
			skip: []string{"ManualValue", "ManualPtr", "GroupedStruct", "GroupedScalar", "InGroupSilent", "Generic", "Runner", "Status"},
		},
		{
			name:    "все формы директивы без предупреждений",
			fixture: "markers",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file, warnings := generateFixture(t, test.fixture)

			for _, want := range test.strings {
				if !containsWarning(warnings, want) {
					t.Errorf("нет предупреждения со словами %q; есть: %v", want, warnings)
				}
			}
			if len(test.strings) == 0 && len(warnings) != 0 {
				t.Errorf("неожиданные предупреждения: %v", warnings)
			}
			for _, skipped := range test.skip {
				if strings.Contains(string(file.Content), skipped) {
					t.Errorf("пропущенное поле %q попало в выходной код:\n%s", skipped, file.Content)
				}
			}
		})
	}
}

// TestGeneratedTypesOrder проверяет, что метод получают только помеченные типы и в порядке объявления.
func TestGeneratedTypesOrder(t *testing.T) {
	tests := []struct {
		fixture string
		types   []string
	}{
		{fixture: "simple", types: []string{"ResetableStruct"}},
		{fixture: "kinds", types: []string{"All"}},
		{fixture: "nesting", types: []string{"Outer"}},
		{fixture: "markers", types: []string{"Exact", "Spaced", "UpperCase"}},
		{fixture: "conflicts", types: []string{"InGroupMarked", "Neighbor"}},
	}

	for _, test := range tests {
		t.Run(test.fixture, func(t *testing.T) {
			file, _ := generateFixture(t, test.fixture)

			if got := receiversOf(string(file.Content)); !reflect.DeepEqual(got, test.types) {
				t.Errorf("набор и порядок методов:\nполучено %v\nожидалось %v", got, test.types)
			}
		})
	}
}

// receiversOf возвращает имена типов получателей сгенерированных методов — в порядке вывода.
func receiversOf(content string) []string {
	const prefix = "func ("
	const suffix = ") Reset() {"

	var names []string
	for line := range strings.SplitSeq(content, "\n") {
		if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
			continue
		}
		receiver := strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix)
		names = append(names, strings.TrimSpace(receiver[strings.LastIndex(receiver, "*")+1:]))
	}
	return names
}

func TestGenerateIsByteStable(t *testing.T) {
	const fixture = "kinds"

	first, _ := generateFixture(t, fixture)
	second, _ := generateFixture(t, fixture)

	if !bytes.Equal(first.Content, second.Content) {
		t.Errorf("две генерации одного исходника дали разный вывод:\n%s\n----\n%s", first.Content, second.Content)
	}
}

// TestSelfReferenceWarnedOnce проверяет, что о риске циклического графа сообщается ровно один раз
// на поле, а сам метод генерируется.
func TestSelfReferenceWarnedOnce(t *testing.T) {
	file, warnings := generateFixture(t, "nesting")

	const part = `поле "next" ссылается на тип Outer`
	if got := countWarning(warnings, part); got != 1 {
		t.Errorf("предупреждений о самоссылке = %d, хотим 1; есть: %v", got, warnings)
	}
	if !strings.Contains(string(file.Content), "next.Reset()") {
		t.Errorf("рекурсивный вызов пропал из метода:\n%s", file.Content)
	}
}

// TestNoTypeAssertionForConcreteFields: type assertion к interface{ Reset() } компилируется только
// над интерфейсным значением, поэтому полям конкретных типов он не генерируется.
func TestNoTypeAssertionForConcreteFields(t *testing.T) {
	file, _ := generateFixture(t, "kinds")

	if got := strings.Count(string(file.Content), ".(interface"); got != 1 {
		t.Errorf("проверок типа = %d, хотим 1 (только интерфейснее поле payload):\n%s", got, file.Content)
	}
}

func TestLoadRejectsBrokenPackage(t *testing.T) {
	if _, err := Load(filepath.Join("testdata", "src"), "./broken"); err == nil {
		t.Fatal("ожидалась ошибка разбора фикстуры broken")
	}
}

// TestGeneratedCodeTypeChecks проверяет, что метод Reset не только разбирается, но и проходит
// проверку типов вместе с фикстурой: сгенерированное дерево добавляется к исходным файлам пакета.
func TestGeneratedCodeTypeChecks(t *testing.T) {
	for _, fixture := range []string{"simple", "kinds", "nesting", "conflicts", "markers"} {
		t.Run(fixture, func(t *testing.T) {
			pkg := loadFixture(t, fixture)

			files, _, err := Generate([]*packages.Package{pkg})
			if err != nil {
				t.Fatalf("генерация фикстуры %s: %v", fixture, err)
			}

			// Фикстуру разбирает packages.Load в своём FileSet, а проверка типов не переносит
			// позиции между FileSet — поэтому исходники читаются заново.
			fset := token.NewFileSet()
			units := make([]*ast.File, 0, len(files)+1)
			for _, source := range sortFiles(pkg) {
				path := sourcePath(pkg, source)
				unit, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
				if err != nil {
					t.Fatalf("разбор %s: %v", path, err)
				}
				units = append(units, unit)
			}
			for _, generated := range files {
				unit, err := parser.ParseFile(fset, filepath.Base(generated.Path), generated.Content, parser.SkipObjectResolution)
				if err != nil {
					t.Fatalf("разбор %s: %v\n%s", generated.Path, err, generated.Content)
				}
				units = append(units, unit)
			}

			config := types.Config{Importer: fixedImporter(pkg)}
			if _, err := config.Check(pkg.Name, fset, units, nil); err != nil {
				t.Fatalf("проверка типов сгенерированного кода: %v\n%s", err, dumpFiles(files))
			}
		})
	}
}

// fixedImporter отдаёт типы зависимостей из уже загруженного пакета: заново разбирать sync ради
// проверки сгенерированного кода не нужно.
func fixedImporter(pkg *packages.Package) types.Importer {
	return importFunc(func(path string) (*types.Package, error) {
		if path == "unsafe" {
			return types.Unsafe, nil
		}
		loaded, found := pkg.Imports[path]
		if !found || loaded.Types == nil {
			return nil, fmt.Errorf("зависимость %s не загружена", path)
		}
		return loaded.Types, nil
	})
}

type importFunc func(path string) (*types.Package, error)

func (f importFunc) Import(path string) (*types.Package, error) { return f(path) }

func dumpFiles(files []File) string {
	var content strings.Builder
	for _, file := range files {
		content.WriteString("---- " + file.Path + " ----\n")
		content.Write(file.Content)
	}
	return content.String()
}

// TestWarningString фиксирует формат диагностики: cmd/reset печатает предупреждения как есть.
func TestWarningString(t *testing.T) {
	warning := Warning{
		Message:  "поле \"s\" не сброшено",
		Position: token.Position{Filename: "box.go", Line: 7, Column: 2},
	}

	const want = "box.go:7:2: поле \"s\" не сброшено"
	if got := warning.String(); got != want {
		t.Errorf("строка предупреждения = %q, хотим %q", got, want)
	}
}

func TestGeneratedPath(t *testing.T) {
	if got, want := GeneratedPath("/tmp/pkg/accounts.go"), "/tmp/pkg/accounts_reset_gen.go"; got != want {
		t.Errorf("GeneratedPath = %q, хотим %q", got, want)
	}
}

func TestReceiverName(t *testing.T) {
	tests := []struct {
		typeName string
		reserved map[string]bool
		want     string
	}{
		{typeName: "ResetableStruct", want: "rs"},
		{typeName: "Session", want: "s"},
		{typeName: "ID", want: "id"},
		{typeName: "Account", reserved: map[string]bool{"a": true}, want: "v"},
	}

	for _, test := range tests {
		if got := receiverName(test.typeName, test.reserved); got != test.want {
			t.Errorf("receiverName(%q) = %q, хотим %q", test.typeName, got, test.want)
		}
	}
}

func containsWarning(warnings []Warning, part string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning.Message, part) {
			return true
		}
	}
	return false
}

func countWarning(warnings []Warning, part string) int {
	var count int
	for _, warning := range warnings {
		if strings.Contains(warning.Message, part) {
			count++
		}
	}
	return count
}

// assertGolden сверяет вывод с эталоном; с флагом -update сохраняет текущий вывод.
func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", "golden", name+".golden")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("сохранение %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s: %v (эталон создают прогоном go test -run TestGenerateGolden -update)", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("вывод не совпал с %s\nхотим:\n%s\nполучено:\n%s", path, want, got)
	}
}

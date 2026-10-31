package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"golang.org/x/tools/go/packages"

	"study-go.ru/cho/eto/internal/codegen/resetgen"
)

const generatedContent = resetgen.GeneratedHeader + "\n\npackage demo\n"

func TestWriteFiles(t *testing.T) {
	tests := []struct {
		initial string // содержимое файла до записи; пусто — файла нет
		name    string
	}{
		{name: "файла не было — создан"},
		{name: "файл был — перезаписан", initial: "// написано рукой\n\npackage demo\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "demo_reset_gen.go")
			if test.initial != "" {
				writeFile(t, path, test.initial)
			}

			written, err := writeFiles([]resetgen.File{{Path: path, Content: []byte(generatedContent)}})
			if err != nil {
				t.Fatalf("запись вернула ошибку: %v", err)
			}
			if !reflect.DeepEqual(written, []string{path}) {
				t.Errorf("записано %v, хотим [%s]", written, path)
			}
			if body := readFile(t, path); body != generatedContent {
				t.Errorf("содержимое = %q, хотим %q", body, generatedContent)
			}
		})
	}
}

// TestWriteFilesSortedPaths проверяет, что вывод команды не зависит от порядка файлов,
// который вернул генератор.
func TestWriteFilesSortedPaths(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a_reset_gen.go")
	second := filepath.Join(dir, "b_reset_gen.go")

	written, err := writeFiles([]resetgen.File{
		{Path: second, Content: []byte(generatedContent)},
		{Path: first, Content: []byte(generatedContent)},
	})
	if err != nil {
		t.Fatalf("запись вернула ошибку: %v", err)
	}
	if want := []string{first, second}; !reflect.DeepEqual(written, want) {
		t.Errorf("записано %v, хотим %v", written, want)
	}
}

func TestWriteFilesReportsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "нет-такого-каталога", "demo_reset_gen.go")

	written, err := writeFiles([]resetgen.File{{Path: path, Content: []byte(generatedContent)}})
	if err == nil {
		t.Fatal("ожидалась ошибка записи в несуществующий каталог")
	}
	if len(written) != 0 {
		t.Errorf("записано %v, хотим пустой список", written)
	}
}

func TestRemoveOrphaned(t *testing.T) {
	tests := []struct {
		initial     string // содержимое файла на диске; пусто — файла нет
		name        string
		produced    bool // генератор вернул файл по этому пути
		wantRemoved bool // путь должен вернуться в списке удалённых
		wantExists  bool // файл должен остаться на диске
	}{
		{name: "осиротевший свой файл удалён", initial: generatedContent, wantRemoved: true},
		{name: "осиротевший файл без преамбулы тоже удалён", initial: "package demo\n", wantRemoved: true},
		{name: "нужный файл не трогается", initial: generatedContent, produced: true, wantExists: true},
		{name: "уже удалённого вручную файла нет в ответе"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "demo_reset_gen.go")
			if test.initial != "" {
				writeFile(t, path, test.initial)
			}

			var want []resetgen.File
			if test.produced {
				want = []resetgen.File{{Path: path, Content: []byte(generatedContent)}}
			}

			removed, err := removeOrphaned(want, []string{path})
			if err != nil {
				t.Fatalf("удаление вернуло ошибку: %v", err)
			}
			if wantRemoved := []string{path}; test.wantRemoved && !reflect.DeepEqual(removed, wantRemoved) {
				t.Errorf("удалено %v, хотим %v", removed, wantRemoved)
			}
			if !test.wantRemoved && len(removed) != 0 {
				t.Errorf("удалено %v, хотим пустой список", removed)
			}

			_, statErr := os.Stat(path)
			if exists := statErr == nil; exists != test.wantExists {
				t.Errorf("файл на диске = %v (stat: %v), хотим %v", exists, statErr, test.wantExists)
			}
		})
	}
}

func TestFindGenerated(t *testing.T) {
	tests := []struct {
		name string
		pkg  *packages.Package
		want []string
	}{
		{
			name: "имена относительные",
			pkg:  &packages.Package{Dir: "/p", GoFiles: []string{"box.go", "box_reset_gen.go"}},
			want: []string{filepath.Join("/p", "box_reset_gen.go")},
		},
		{
			name: "пути абсолютные",
			pkg: &packages.Package{
				Dir:     "/p",
				GoFiles: []string{absPathForTest()},
			},
			want: []string{absPathForTest()},
		},
		{
			name: "файлы других генераторов пропускаются",
			pkg:  &packages.Package{Dir: "/p", GoFiles: []string{"box.go", "box_stringer.go"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := findGenerated([]*packages.Package{test.pkg}); !reflect.DeepEqual(got, test.want) {
				t.Errorf("найдено %v, хотим %v", got, test.want)
			}
		})
	}
}

// absPathForTest возвращает абсолютный путь, понятный filepath.IsAbs на текущей ОС:
// на Windows — путь с диском, на Unix — Unix-стиль.
func absPathForTest() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "p", "box_reset_gen.go")
	}
	return "/p/box_reset_gen.go"
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s: %v", path, err)
	}

	return string(content)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("запись %s: %v", path, err)
	}
}

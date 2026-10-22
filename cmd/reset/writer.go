package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"study-go.ru/cho/eto/internal/codegen/resetgen"
)

// Статусы выходных файлов — единственное, что команда печатает на stdout.
const (
	statusWritten = "записан"
	statusRemoved = "удалён"
)

// writeFiles создаёт или перезаписывает каждый файл, который вернул генератор. Пути сортируются,
// чтобы вывод не зависел от порядка обхода пакетов.
func writeFiles(files []resetgen.File) ([]string, error) {
	sorted := append([]resetgen.File{}, files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	written := make([]string, 0, len(sorted))
	var failed []error

	for _, file := range sorted {
		if err := os.WriteFile(file.Path, file.Content, 0o644); err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", file.Path, err))
			continue
		}
		written = append(written, file.Path)
	}

	return written, errors.Join(failed...)
}

// removeOrphaned удаляет файлы генератора, которых нет в его выводе: если из исходника убрали
// последнюю директиву, в пакете остался бы метод на несуществующий тип и сборка бы падала.
// Кандидаты — файлы с суффиксом resetgen.GeneratedSuffix из разобранных пакетов; их содержимое
// не читается и ни с чем не сравнивается.
func removeOrphaned(want []resetgen.File, existing []string) ([]string, error) {
	produced := make(map[string]bool, len(want))
	for _, file := range want {
		produced[file.Path] = true
	}

	paths := append([]string{}, existing...)
	sort.Strings(paths)

	removed := make([]string, 0, len(paths))
	var failed []error

	for _, path := range paths {
		if produced[path] {
			continue
		}
		if err := os.Remove(path); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				failed = append(failed, fmt.Errorf("%s: %w", path, err))
			}
			continue
		}
		removed = append(removed, path)
	}

	return removed, errors.Join(failed...)
}

// findGenerated возвращает файлы resetgen.GeneratedSuffix разобранных пакетов: они лежат рядом
// с исходниками, поэтому достаточно имён из pkg.GoFiles. Загрузчик отдаёт их то относительными
// именами, то абсолютными путями — во втором случае присоединять pkg.Dir не нужно.
func findGenerated(pkgs []*packages.Package) []string {
	var found []string
	for _, pkg := range pkgs {
		for _, name := range pkg.GoFiles {
			if !strings.HasSuffix(name, resetgen.GeneratedSuffix) {
				continue
			}
			if filepath.IsAbs(name) {
				found = append(found, name)
				continue
			}
			found = append(found, filepath.Join(pkg.Dir, name))
		}
	}
	return found
}

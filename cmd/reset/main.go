package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/sirupsen/logrus"

	"study-go.ru/cho/eto/internal/codegen/resetgen"
	config "study-go.ru/cho/eto/internal/config"
)

// Коды возврата команды: 0 — файлы приведены к выводу генератора, 1 — ошибка загрузки или записи.
const (
	exitOK      = 0
	exitFailure = 1
)

// packagesPattern — что разбирает команда: весь проект целиком, без выбора со стороны запускающего.
const packagesPattern = "./..."

func init() {
	config.LogsInit()
}

func main() {
	os.Exit(run(os.Stdout))
}

// run разбирает все пакеты проекта, генерирует методы Reset и записывает выходные файлы заново —
// без сравнения с тем, что уже лежит на диске. Осиротевшие файлы удаляются. Диагностика уходит в
// лог (stderr), на stdout — только записанные и удалённые файлы.
func run(stdout io.Writer) int {
	logrus.SetLevel(logrus.WarnLevel)

	pkgs, err := resetgen.Load(".", packagesPattern)
	if err != nil {
		logrus.Errorf("загрузка пакетов: %v", err)
		return exitFailure
	}

	files, warnings, err := resetgen.Generate(pkgs)
	for _, warning := range warnings {
		logrus.Warn(warning.String())
	}
	if err != nil {
		logrus.Errorf("генерация: %v", err)
		return exitFailure
	}

	written, writeErr := writeFiles(files)
	for _, path := range written {
		fmt.Fprintln(stdout, statusWritten, path)
	}

	removed, removeErr := removeOrphaned(files, findGenerated(pkgs))
	for _, path := range removed {
		fmt.Fprintln(stdout, statusRemoved, path)
	}

	if err := errors.Join(writeErr, removeErr); err != nil {
		logrus.Errorf("запись файлов: %v", err)
		return exitFailure
	}

	return exitOK
}

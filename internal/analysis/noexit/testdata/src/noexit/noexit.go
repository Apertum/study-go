package noexit

import (
	"os"
	stdos "os"
	"testing"
)

// Точка входа теста: os.Exit здесь обязателен, замечания быть не должно.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func helper() {
	os.Exit(1) // want "прямой вызов os.Exit"
}

// Импорт под другим именем не меняет сути вызова.
func aliased() {
	stdos.Exit(1) // want "прямой вызов os.Exit"
}

func inFuncLit() {
	go func() {
		os.Exit(1) // want "прямой вызов os.Exit"
	}()
}

type runner struct{}

// Метод с именем main точкой входа не является.
func (runner) main() {
	os.Exit(1) // want "прямой вызов os.Exit"
}

// Функция с именем main в пакете не main точкой входа не является.
func main(size int) {
	os.Exit(size) // want "прямой вызов os.Exit"
}

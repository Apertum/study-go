// Package broken — фикстура с ошибкой компиляции: пакет собран не будет, и генератор обязан
// сообщить об ошибке, не создав ни одного частичного файла. Помеченная структура здесь есть,
// чтобы тест проверял отсутствие частичного вывода, а не отсутствие целей.
package broken

type Needles struct {
	Missing string
}

//generate:reset
type Marked struct {
	Value int
}

var _ undefinedType

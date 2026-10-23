// Package resetdemo показывает, что делает метод Reset, сгенерированный командой cmd/reset.
//
// Тип помечен директивой generate:reset, а рядом лежит сгенерированный файл
// resetdemo_reset_gen.go — его заново создаёт go run ./cmd/reset.
package resetdemo

// Payload — поле произвольного значения: сбросится, если у значения окажется метод Reset.
type Payload any

// ResetableStruct — демонстрационный тип, метод Reset для которого создаёт генератор.
//
// Порядок полей отличается от канонического примера: проверке fieldalignment нужно, чтобы
// указатели шли перед строками и слайсами. На смысл сброса порядок не влияет — метод лишь
// обходит поля в том порядке, в каком они объявлены.
//
//generate:reset
type ResetableStruct struct {
	strP    *string
	pp      **int
	m       map[string]string
	byID    map[string]*ResetableStruct
	child   *ResetableStruct
	str     string
	payload Payload
	s       []int
	strings []string
	items   []*ResetableStruct
	i       int
}

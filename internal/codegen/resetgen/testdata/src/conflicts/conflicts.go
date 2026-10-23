// Package conflicts — конфликты директивы: готовый метод Reset у помеченного типа, директива над
// не-структурой, параметризованный тип и объявление нескольких типов под одним комментарием.
package conflicts

//generate:reset
type ManualValue struct {
	A int
}

func (m ManualValue) Reset() {}

// GENERATE:RESET
type ManualPtr struct {
	A int
}

func (m *ManualPtr) Reset() {}

//generate:reset
type Generic[T any] struct {
	Value T
}

//generate:reset
type (
	GroupedScalar int
	GroupedStruct struct{ A int }
)

type (
	//generate:reset
	InGroupMarked struct {
		B int
	}
	InGroupSilent struct {
		C int
	}
)

//generate:reset
type NotStruct chan int

//generate:reset
type Runner interface {
	Run()
}

//generate:reset
type Status uint8

//generate:reset
type Neighbor struct {
	D int
}

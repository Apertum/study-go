// Package markers — варианты записи директивы: цель меняется только от наличия директивы.
package markers

//generate:reset
type Exact struct {
	A int
}

// generate:reset
type Spaced struct {
	B int
}

// GENERATE:RESET
type UpperCase struct {
	C int
}

// обычная документация, директивы нет
type Undocumented struct {
	D int
}

//go:generate echo hello
type OtherDirective struct {
	E int
}

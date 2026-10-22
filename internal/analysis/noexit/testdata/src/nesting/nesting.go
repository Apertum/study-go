// Package nesting — вложенные объекты: свои, чужие, встроенные и самоссылка.
package nesting

import (
	"sync"

	"study-go.ru/cho/eto/internal/codegen/resetgen/testdata/src/ext"
)

type Base struct {
	Steps int
}

func (b *Base) Reset() {
	b.Steps = 0
}

type Count int

type Plain struct {
	Values []int
}

//generate:reset
type Outer struct {
	Base
	Count
	*ext.Watcher
	plain Plain
	mutex sync.Mutex
	watch *ext.Watcher
	next  *Outer
}

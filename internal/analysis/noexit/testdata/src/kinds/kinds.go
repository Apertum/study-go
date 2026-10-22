// Package kinds — все виды полей, которые разбирает генератор.
package kinds

import (
	"time"
	"unsafe"

	"study-go.ru/cho/eto/internal/codegen/resetgen/testdata/src/ext"
)

type IDs []int
type Name string

// Pair — параметризованный тип: поле с ним генератор пропускает.
type Pair[T any] struct {
	First  T
	Second T
}

// generate:reset
type All struct {
	ptrInt  *int
	ptrSlc  *[]string
	ptrMap  *map[string]int
	ptrArr  *[2]bool
	pp      **int
	watcher ext.Watcher
	plain   ext.Plain
	payload any
	name    Name
	times   [2]time.Duration
	pair    Pair[int]
	count   int
	ratio   float64
	flag    bool
	complex complex64
	ids     IDs
	fixed   [3]int
	done    chan struct{}
	handle  func(error)
	items   []*All
	byID    map[string]*All
	pointer unsafe.Pointer
}

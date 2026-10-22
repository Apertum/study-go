// Package ext — внешний пакет для проверки делегирования чужому типу.
package ext

type Watcher struct {
	Ticks int
}

func (w *Watcher) Reset() {
	w.Ticks = 0
}

// Plain — внешний тип без метода Reset: поле с ним сгенерировать нечем.
type Plain struct {
	Value int
}

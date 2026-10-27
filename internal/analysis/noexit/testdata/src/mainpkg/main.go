package main

import "os"

func main() {
	defer func() {
		// Отложенная функция — не точка входа: она не отвечает за завершение процесса.
		os.Exit(1) // want "прямой вызов os.Exit"
	}()

	shutdown()

	// Единственное место, где прямой вызов os.Exit разрешён.
	os.Exit(0)
}

// shutdown — обычная функция пакета main, завершать процесс ей нельзя.
func shutdown() {
	os.Exit(1) // want "прямой вызов os.Exit"
}

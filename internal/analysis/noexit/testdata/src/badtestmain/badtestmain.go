package badtestmain

import (
	"os"
	"testing"
)

// TestMain без параметра *testing.M не является точкой входа теста.
func TestMain() {
	os.Exit(1) // want "прямой вызов os.Exit"
}

// other похож на точку входа параметром, но назван иначе.
func other(m *testing.M) {
	os.Exit(m.Run()) // want "прямой вызов os.Exit"
}

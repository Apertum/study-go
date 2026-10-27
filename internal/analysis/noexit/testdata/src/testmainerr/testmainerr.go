package testmainerr

import (
	"os"
	"testing"
)

// TestMain с результатом не является точкой входа: testing требует func(*testing.M).
func TestMain(m *testing.M) error {
	os.Exit(1) // want "прямой вызов os.Exit"
	return nil
}

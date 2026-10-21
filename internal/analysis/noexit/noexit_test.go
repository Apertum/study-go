package noexit_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"study-go.ru/cho/eto/internal/analysis/noexit"
)

// TestAnalyzer проверяет, что анализатор находит os.Exit вне точек входа процесса
// и молчит внутри них. Ожидаемые замечания заданы комментариями want в testdata.
func TestAnalyzer(t *testing.T) {
	tests := []struct {
		name string
		pkg  string
	}{
		{name: "обычный пакет", pkg: "noexit"},
		{name: "пакет main", pkg: "mainpkg"},
		{name: "TestMain без параметра testing.M", pkg: "badtestmain"},
		{name: "TestMain с результатом", pkg: "testmainerr"},
		{name: "пакет без импорта os", pkg: "nosys"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			analysistest.Run(t, analysistest.TestData(), noexit.Analyzer, test.pkg)
		})
	}
}

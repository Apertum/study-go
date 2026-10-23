package main

import (
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/multichecker"

	"golang.org/x/tools/go/analysis/passes/appends"
	"golang.org/x/tools/go/analysis/passes/asmdecl"
	"golang.org/x/tools/go/analysis/passes/assign"
	"golang.org/x/tools/go/analysis/passes/atomic"
	"golang.org/x/tools/go/analysis/passes/atomicalign"
	"golang.org/x/tools/go/analysis/passes/bools"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/analysis/passes/buildtag"
	"golang.org/x/tools/go/analysis/passes/cgocall"
	"golang.org/x/tools/go/analysis/passes/composite"
	"golang.org/x/tools/go/analysis/passes/copylock"
	"golang.org/x/tools/go/analysis/passes/ctrlflow"
	"golang.org/x/tools/go/analysis/passes/deepequalerrors"
	"golang.org/x/tools/go/analysis/passes/defers"
	"golang.org/x/tools/go/analysis/passes/directive"
	"golang.org/x/tools/go/analysis/passes/errorsas"
	"golang.org/x/tools/go/analysis/passes/fieldalignment"
	"golang.org/x/tools/go/analysis/passes/findcall"
	"golang.org/x/tools/go/analysis/passes/framepointer"
	"golang.org/x/tools/go/analysis/passes/gofix"
	"golang.org/x/tools/go/analysis/passes/hostport"
	"golang.org/x/tools/go/analysis/passes/httpmux"
	"golang.org/x/tools/go/analysis/passes/httpresponse"
	"golang.org/x/tools/go/analysis/passes/ifaceassert"
	"golang.org/x/tools/go/analysis/passes/inline"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/analysis/passes/loopclosure"
	"golang.org/x/tools/go/analysis/passes/lostcancel"
	"golang.org/x/tools/go/analysis/passes/modernize"
	"golang.org/x/tools/go/analysis/passes/nilfunc"
	"golang.org/x/tools/go/analysis/passes/nilness"
	"golang.org/x/tools/go/analysis/passes/pkgfact"
	"golang.org/x/tools/go/analysis/passes/printf"
	"golang.org/x/tools/go/analysis/passes/reflectvaluecompare"
	"golang.org/x/tools/go/analysis/passes/scannererr"
	"golang.org/x/tools/go/analysis/passes/shadow"
	"golang.org/x/tools/go/analysis/passes/shift"
	"golang.org/x/tools/go/analysis/passes/sigchanyzer"
	"golang.org/x/tools/go/analysis/passes/slog"
	"golang.org/x/tools/go/analysis/passes/sortslice"
	"golang.org/x/tools/go/analysis/passes/sqlrowserr"
	"golang.org/x/tools/go/analysis/passes/stdmethods"
	"golang.org/x/tools/go/analysis/passes/stdversion"
	"golang.org/x/tools/go/analysis/passes/stringintconv"
	"golang.org/x/tools/go/analysis/passes/structtag"
	"golang.org/x/tools/go/analysis/passes/testinggoroutine"
	"golang.org/x/tools/go/analysis/passes/tests"
	"golang.org/x/tools/go/analysis/passes/timeformat"
	"golang.org/x/tools/go/analysis/passes/unmarshal"
	"golang.org/x/tools/go/analysis/passes/unreachable"
	"golang.org/x/tools/go/analysis/passes/unsafeptr"
	"golang.org/x/tools/go/analysis/passes/unusedresult"
	"golang.org/x/tools/go/analysis/passes/unusedwrite"
	"golang.org/x/tools/go/analysis/passes/usesgenerics"
	"golang.org/x/tools/go/analysis/passes/waitgroup"

	"honnef.co/go/tools/analysis/lint"
	"honnef.co/go/tools/quickfix"
	"honnef.co/go/tools/simple"
	"honnef.co/go/tools/staticcheck"
	"honnef.co/go/tools/stylecheck"

	"github.com/gostaticanalysis/forcetypeassert"
	"github.com/gostaticanalysis/nilerr"
	"github.com/timakin/bodyclose/passes/bodyclose"

	"github.com/sirupsen/logrus"

	"study-go.ru/cho/eto/internal/analysis/noexit"
	config "study-go.ru/cho/eto/internal/config"
)

func init() {
	config.LogsInit()
}

// toolAnalyzers — анализаторы golang.org/x/tools/go/analysis/passes. buildssa, ctrlflow,
// inline, inspect, findcall, pkgfact и usesgenerics ничего не сообщают о коде: они готовят
// факты для остальных проверок, но входят в состав пакета, поэтому зарегистрированы явно.
var toolAnalyzers = []*analysis.Analyzer{
	appends.Analyzer,
	asmdecl.Analyzer,
	assign.Analyzer,
	atomic.Analyzer,
	atomicalign.Analyzer,
	bools.Analyzer,
	buildssa.Analyzer,
	buildtag.Analyzer,
	cgocall.Analyzer,
	composite.Analyzer,
	copylock.Analyzer,
	ctrlflow.Analyzer,
	deepequalerrors.Analyzer,
	defers.Analyzer,
	directive.Analyzer,
	errorsas.Analyzer,
	fieldalignment.Analyzer,
	findcall.Analyzer,
	framepointer.Analyzer,
	gofix.Analyzer,
	hostport.Analyzer,
	httpmux.Analyzer,
	httpresponse.Analyzer,
	ifaceassert.Analyzer,
	inline.Analyzer,
	inspect.Analyzer,
	loopclosure.Analyzer,
	lostcancel.Analyzer,
	nilfunc.Analyzer,
	nilness.Analyzer,
	pkgfact.Analyzer,
	printf.Analyzer,
	reflectvaluecompare.Analyzer,
	scannererr.Analyzer,
	shadow.Analyzer,
	shift.Analyzer,
	sigchanyzer.Analyzer,
	slog.Analyzer,
	sortslice.Analyzer,
	sqlrowserr.Analyzer,
	stdmethods.Analyzer,
	stdversion.Analyzer,
	stringintconv.Analyzer,
	structtag.Analyzer,
	testinggoroutine.Analyzer,
	tests.Analyzer,
	timeformat.Analyzer,
	unmarshal.Analyzer,
	unreachable.Analyzer,
	unsafeptr.Analyzer,
	unusedresult.Analyzer,
	unusedwrite.Analyzer,
	usesgenerics.Analyzer,
	waitgroup.Analyzer,
}

// thirdPartyAnalyzers — публичные анализаторы сторонних авторов на базе go/analysis.
var thirdPartyAnalyzers = []*analysis.Analyzer{
	nilerr.Analyzer,
	forcetypeassert.Analyzer,
	bodyclose.Analyzer,
}

// projectAnalyzers — собственные анализаторы репозитория. Самый лучший.
var projectAnalyzers = []*analysis.Analyzer{
	noexit.Analyzer,
}

// staticcheckFlatten разворачивает наборы проверок staticcheck в список анализаторов go/analysis.
func staticcheckFlatten(groups ...[]*lint.Analyzer) []*analysis.Analyzer {
	analyzers := make([]*analysis.Analyzer, 0, 256)
	for _, group := range groups {
		for _, a := range group {
			analyzers = append(analyzers, a.Analyzer)
		}
	}
	return analyzers
}

func main() {
	logrus.Info(`
╔═══════════════════════════════╗
║         multichecker          ║
╚═══════════════════════════════╝
    `)

	// Овердохрена. Сложим группами. Самописный последним.
	analyzers := toolAnalyzers
	analyzers = append(analyzers, modernize.Suite...)
	analyzers = append(analyzers, staticcheckFlatten(
		staticcheck.Analyzers,
		simple.Analyzers,
		stylecheck.Analyzers,
		quickfix.Analyzers,
	)...)
	analyzers = append(analyzers, thirdPartyAnalyzers...)
	// ну и самописный добавим.
	analyzers = append(analyzers, projectAnalyzers...)

	logrus.Infof("зарегистрировано анализаторов: %d", len(analyzers))

	multichecker.Main(analyzers...)
}

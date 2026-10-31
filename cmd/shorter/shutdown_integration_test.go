package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Сквозные проверки остановки на собранном бинарнике: сервер обязан завершиться по сигналу
// так, чтобы адрес освободился, начатый запрос получил полный ответ, а событие аудита
// попало в файл. Сигналы доставляются настоящие — отмена контекста проверена в serve_test.go.

// configEnvNames — переменные окружения, которые читает ParseFlags. Наследовать их нельзя:
// настройки запускаемого вручную сервиса не должны решать исход теста.
var configEnvNames = []string{
	"SERVER_ADDRESS", "BASE_URL", "FILE_STORAGE_PATH", "DATABASE_DSN", "COOKIE_KEY",
	"AUDIT_FILE", "AUDIT_URL", "ENABLE_HTTPS", "CERT_FILE", "KEY_FILE", "HTTP_SERVER_ADDRESS", "CONFIG",
}

// errSignalsUnsupported — признак того, что платформа не умеет доставлять сигналы чужому
// процессу; его возвращает заглушка killPid на Windows.
var errSignalsUnsupported = errors.New("доставка сигналов не поддерживается платформой")

// lockedBuf накапливает вывод процесса и безопасен для чтения во время записи.
type lockedBuf struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *lockedBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// String возвращает накопленный вывод.
func (b *lockedBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// shorterProcess — запущенный процесс сервера с перехватом журнала.
type shorterProcess struct {
	proc   *os.Process
	output *lockedBuf
	done   chan error
}

// buildShorter собирает бинарник сервера во временный каталог теста. Каталог пакета —
// текущий при запуске go test, поэтому пакет собирается как ".".
func buildShorter(t *testing.T) string {
	t.Helper()

	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("нужен инструмент go для сборки сервера")
	}

	name := "shorter"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	bin := filepath.Join(t.TempDir(), name)

	build := exec.Command(goTool, "build", "-o", bin, ".")

	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("сервер не собран: %v\n%s", err, out)
	}

	return bin
}

// shorterEnv собирает окружение дочернего процесса: переменные, решающие исход теста,
// вычищаются, остальное наследуется целиком (без системных переменных процесс на Windows
// может не запуститься).
func shorterEnv(values []string) []string {
	skip := make(map[string]bool, len(configEnvNames))

	for _, name := range configEnvNames {
		skip[name] = true
	}

	env := make([]string, 0, len(os.Environ())+len(values))

	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if skip[name] {
			continue
		}

		env = append(env, item)
	}

	return append(env, values...)
}

// startShorter запускает сервер в указанном каталоге с указанными переменными.
func startShorter(t *testing.T, bin, dir string, env []string) *shorterProcess {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir = dir
	cmd.Env = shorterEnv(env)

	output := &lockedBuf{}
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Start(); err != nil {
		cancel()

		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("каталог %s запрещает исполнение собранных бинарников — укажи TMPDIR на исполняемый каталог", dir)
		}

		t.Fatalf("сервер не запущен: %v", err)
	}

	// Один Wait на процесс: ошибку получает дождавшийся, а закрытие stopped видно всем.
	done := make(chan error, 1)
	stopped := make(chan struct{})

	go func() {
		err := cmd.Wait()
		done <- err
		close(stopped)
	}()

	t.Cleanup(func() {
		cancel()

		select {
		case <-stopped:
		case <-time.After(30 * time.Second):
			t.Error("процесс сервера не завершился после отмены контекста")
		}
	})

	return &shorterProcess{proc: cmd.Process, output: output, done: done}
}

// stopBySignal доставляет процессу настоящий сигнал и отвечает, дошёл ли он: false означает,
// что процесс уже завершился сам (ESRCH) — для проверки повторного сигнала это не провал.
func (p *shorterProcess) stopBySignal(t *testing.T, sig syscall.Signal) bool {
	t.Helper()

	if err := killPid(p.proc.Pid, sig); err != nil {
		if errors.Is(err, errSignalsUnsupported) {
			t.Skip("платформа не умеет доставлять сигналы чужому процессу — проверка остаётся за Linux и macOS")
		}

		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("сигнал не разрешено посылать этому процессу: %v", err)
		}

		if procGone(err) {
			return false
		}

		t.Fatalf("сигнал %v не доставлен: %v", sig, err)
	}

	return true
}

// alive проверяет, что процесс ещё жив: сигнал ноль ничего не посылает, а ошибка означает,
// что процесса нет.
func (p *shorterProcess) alive() bool {
	return p.proc.Signal(syscall.Signal(0)) == nil
}

// exitCode дожидается процесса и возвращает код завершения.
func (p *shorterProcess) exitCode(t *testing.T) int {
	t.Helper()

	select {
	case err := <-p.done:
		if err == nil {
			return 0
		}

		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("процесс завершился нестандартно: %v\nвывод:\n%s", err, p.output.String())
		}

		return exitErr.ExitCode()
	case <-time.After(60 * time.Second):
		t.Fatalf("процесс не завершился; вывод:\n%s", p.output.String())
	}

	return -1
}

// fileModeEnv — запуск без базы: файловое хранилище и аудит в файл внутри каталога теста.
func fileModeEnv(addr, dir, auditPath string) []string {
	return []string{
		"SERVER_ADDRESS=" + addr,
		"BASE_URL=http://" + addr + "/",
		"FILE_STORAGE_PATH=" + filepath.Join(dir, "store.json"),
		"COOKIE_KEY=test-key",
		"DATABASE_DSN=",
		"AUDIT_FILE=" + auditPath,
	}
}

// seedStore пишет фикстуру файлового хранилища: одна ссылка под коротким id abc. Без базы
// это единственный способ получить успешный переход по ссылке, то есть событие аудита.
func seedStore(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "store.json")

	fixture := `[{"uuid":"1","short_url":"abc","original_url":"https://example.com/target","user_id":1}]`

	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("фикстура хранилища не записана: %v", err)
	}

	return path
}

// waitServed дожидается первого HTTP-ответа любым кодом.
func waitServed(t *testing.T, addr string, proc *shorterProcess) {
	t.Helper()

	client := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	deadline := time.Now().Add(30 * time.Second)

	var lastErr error

	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + "/abc")
		if err == nil {
			_ = resp.Body.Close()

			return
		}

		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("по %s никто не ответил: %v\nвывод:\n%s", addr, lastErr, proc.output.String())
}

// nonEmptyLines возвращает непустые строки файла.
func nonEmptyLines(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("файл %s не читается: %v", path, err)
	}

	lines := make([]string, 0, 4)

	for line := range strings.SplitSeq(string(data), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}

	return lines
}

// blockingAuditSink — приёмник аудита, который принимает запрос и не отвечает: им удерживают
// остановку в фазе ожидания аудита, не удлиняя её тридцатью секундами. Таймаут URLHandler —
// пять секунд, поэтому процесс живёт ровно столько, сколько нужно проверке.
func blockingAuditSink(t *testing.T) string {
	t.Helper()

	release := make(chan struct{})

	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-release

		w.WriteHeader(http.StatusAccepted)
	}))

	t.Cleanup(func() {
		close(release)
		sink.Close()
	})

	return sink.URL
}

// TestGracefulShutdownOnSIGTERM — основной сценарий: SIGTERM, код 0, адрес свободен, событие
// аудита в файле, временных файлов нет, повторный запуск на тот же адрес проходит.
func TestGracefulShutdownOnSIGTERM(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)
	dir := t.TempDir()
	addr := freeAddr(t)
	auditPath := filepath.Join(dir, "audit.jsonl")
	storePath := seedStore(t, dir)

	proc := startShorter(t, bin, dir, fileModeEnv(addr, dir, auditPath))

	waitServed(t, addr, proc)

	proc.stopBySignal(t, syscall.SIGTERM)

	require.Equal(t, 0, proc.exitCode(t), "чистая остановка обязана завершаться кодом 0; вывод:\n%s", proc.output.String())

	assert.False(t, acceptsConn(addr), "адрес %s занят после завершения процесса", addr)

	lines := nonEmptyLines(t, auditPath)
	if len(lines) == 0 {
		t.Fatal("событие запроса не попало в файл аудита")
	}

	assert.Contains(t, strings.Join(lines, "\n"), string(`"action":"follow"`))
	assert.Contains(t, strings.Join(lines, "\n"), "https://example.com/target")

	stray, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	require.NoError(t, err)
	assert.Empty(t, stray, "рядом с %s остались временные файлы", storePath)

	// Освобождённый адрес доступен сразу: следующий запуск поднимается на нём без ошибки.
	restarted := startShorter(t, bin, dir, fileModeEnv(addr, dir, auditPath))

	waitServed(t, addr, restarted)

	restarted.stopBySignal(t, syscall.SIGTERM)

	assert.Equal(t, 0, restarted.exitCode(t), "повторный запуск на %s не завершившийся нулём", addr)
}

// TestShutdownRefusesNewRequestsWhileWaitingAudit — остановка с удержанием на приёме аудита:
// адрес отпускается в начале фазы слушателей, а процесс живёт, пока приёмник не разберёт
// события. Второй сигнал игнорируется: код тот же, диагностика одна.
//
// Дозавершение запроса, начатого до сигнала, проверено детерминированно на уровне serve
// (TestServeCompletesInFlightRequest): в собранном процессе нет маршрута, который можно
// задержать по команде, а полу-отправленный запрос зависит от того, успел ли accept выбрать
// соединение из очереди до закрытия слушателя.
func TestShutdownRefusesNewRequestsWhileWaitingAudit(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)
	dir := t.TempDir()
	addr := freeAddr(t)
	auditPath := filepath.Join(dir, "audit.jsonl")

	seedStore(t, dir)

	env := append(fileModeEnv(addr, dir, auditPath), "AUDIT_URL="+blockingAuditSink(t))

	proc := startShorter(t, bin, dir, env)

	waitServed(t, addr, proc)

	// Событие аудита прямо перед сигналом: оно зависает в приёмнике и держит процесс в своей
	// фазе около пяти секунд (таймаут URLHandler).
	waitServed(t, addr, proc)

	proc.stopBySignal(t, syscall.SIGTERM)

	assert.True(t, proc.alive(), "процесс завершился, не дождавшись разбора аудита")

	waitRefusing(t, addr)

	assert.True(t, proc.alive(), "адрес отпущен, а процесс уже завершился — порядок фаз нарушен")

	// Повторный сигнал не ускоряет остановку и не добавляет диагностику. Если процесс успел
	// завершиться сам (машине не хватило пяти секунд ожидания приёмника), проверять нечего.
	if proc.stopBySignal(t, syscall.SIGTERM) {
		assert.Equal(t, 0, proc.exitCode(t), "повторный сигнал меняет код завершения")

		assert.Equal(t, 1, strings.Count(proc.output.String(), "перестаем принимать новые соединения"),
			"повторный сигнал напечатал вторую диагностику остановки:\n%s", proc.output.String())

		return
	}

	assert.Equal(t, 0, proc.exitCode(t), "остановка без повторного сигнала завершилась не нулём")
}

// TestSIGQUITWithoutStackDump — сигнатурное отличие от поведения Go по умолчанию: дампа
// горутин в выводе быть не должно, код завершения — тот же нулевой.
func TestSIGQUITWithoutStackDump(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)
	dir := t.TempDir()
	addr := freeAddr(t)
	auditPath := filepath.Join(dir, "audit.jsonl")

	seedStore(t, dir)

	proc := startShorter(t, bin, dir, fileModeEnv(addr, dir, auditPath))

	waitServed(t, addr, proc)

	proc.stopBySignal(t, syscall.SIGQUIT)

	assert.Equal(t, 0, proc.exitCode(t), "SIGQUIT обязан завершить процесс штатно")

	output := proc.output.String()

	assert.NotContains(t, output, "goroutine ", "SIGQUIT напечатал дамп горутин:\n%s", output)
	assert.NotContains(t, output, "signal: quit", "SIGQUIT вернул диспозицию ОС:\n%s", output)
}

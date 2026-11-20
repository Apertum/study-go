package main

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Сквозные проверки внутреннего эндпоинта статистики на собранном бинарнике: неразбираемое значение
// доверенной подсети завершает запуск до создания файла аудита, а доступ к эндпоинту закрыт, когда
// подсеть не задана или адрес запроса вне неё.

// internalStatsPath — проверяемый маршрут.
const internalStatsPath = "/api/internal/stats"

// secretCookieValue — значение секрета в фикстуре файла конфигурации: оно не должно попасть
// в сообщение об ошибке разбора.
const secretCookieValue = "parol-iz-fayla-dlya-testa"

// writeStatsConfig пишет файл конфигурации и возвращает путь к нему.
func writeStatsConfig(t *testing.T, dir, body string) string {
	t.Helper()

	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("файл конфигурации не записан: %v", err)
	}

	return path
}

// statsRequest отправляет запрос к эндпоинту статистики и возвращает код ответа и тело.
func statsRequest(t *testing.T, addr, method string, header http.Header) (int, string) {
	t.Helper()

	req, err := http.NewRequest(method, "http://"+addr+internalStatsPath, nil)
	if err != nil {
		t.Fatalf("запрос не составлен: %v", err)
	}

	req.Header = header

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("запрос к %s не выполнен: %v", internalStatsPath, err)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("тело ответа не прочитано: %v", err)
	}

	return resp.StatusCode, string(body)
}

// realIPHeader — заголовок с адресом клиента так, как его кладёт обратный прокси.
func realIPHeader(addr string) http.Header {
	header := http.Header{}
	header.Set("X-Real-IP", addr)

	return header
}

// assertNoCounters сверяет, что тело ответа не выдаёт счётчики.
func assertNoCounters(t *testing.T, body string) {
	t.Helper()

	assert.NotContains(t, body, "urls", "тело ответа выдало счётчик сокращений: %q", body)
	assert.NotContains(t, body, "users", "тело ответа выдало счётчик пользователей: %q", body)
}

// TestStartupRefusesBadTrustedSubnetFlag — кривое значение флага завершает запуск до хранилища и
// файла аудита: сервис не поднимается с недостижимым контролем доступа к внутреннему эндпоинту.
func TestStartupRefusesBadTrustedSubnetFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)
	dir := t.TempDir()
	addr := freeAddr(t)
	auditPath := filepath.Join(dir, "audit.jsonl")

	proc := startShorter(t, bin, dir, fileModeEnv(addr, dir, auditPath), "-t", "10.0.0.255.0")

	require.NotEqual(t, 0, proc.exitCode(t), "сервер запустился с неразбираемым значением флага")

	output := proc.output.String()

	assert.Contains(t, output, "-t", "в сообщении нет имени флага:\n%s", output)
	assert.Contains(t, output, "10.0.0.255.0", "в сообщении нет значения настройки:\n%s", output)

	assert.False(t, acceptsConn(addr), "адрес %s слушается несмотря на отказ конфигурации", addr)

	_, statErr := os.Stat(auditPath)

	assert.True(t, errors.Is(statErr, fs.ErrNotExist), "файл аудита создан до отказа конфигурации")
}

// TestStartupRefusesBadTrustedSubnetFromEnv — переменная называется в сообщении и переопределяет
// корректное значение файла: источник указан явно, чтобы причину нашли по способу задания.
func TestStartupRefusesBadTrustedSubnetFromEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)
	dir := t.TempDir()
	addr := freeAddr(t)
	auditPath := filepath.Join(dir, "audit.jsonl")

	env := append(fileModeEnv(addr, dir, auditPath), "TRUSTED_SUBNET=10.0.0.1")

	proc := startShorter(t, bin, dir, env, "-c", writeStatsConfig(t, dir, `{"trusted_subnet": "10.0.0.0/8"}`))

	require.NotEqual(t, 0, proc.exitCode(t), "сервер запустился с неразбираемым значением переменной")

	output := proc.output.String()

	assert.Contains(t, output, "TRUSTED_SUBNET", "в сообщении нет имени переменной:\n%s", output)
	assert.False(t, acceptsConn(addr), "адрес %s слушается несмотря на отказ конфигурации", addr)
}

// TestStartupRefusesBadTrustedSubnetFromFile — значение из файла прерывает запуск с указанием
// ключа файла; секретные строки того же файла в вывод не попадают.
func TestStartupRefusesBadTrustedSubnetFromFile(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)
	dir := t.TempDir()
	addr := freeAddr(t)
	auditPath := filepath.Join(dir, "audit.jsonl")

	path := writeStatsConfig(t, dir, `{"trusted_subnet": "not-a-cidr", "cookie_key": "`+secretCookieValue+`"}`)

	proc := startShorter(t, bin, dir, fileModeEnv(addr, dir, auditPath), "-c", path)

	require.NotEqual(t, 0, proc.exitCode(t), "сервер запустился с неразбираемым значением из файла")

	output := proc.output.String()

	assert.Contains(t, output, "trusted_subnet", "в сообщении нет ключа файла:\n%s", output)
	assert.NotContains(t, output, secretCookieValue, "вывод содержит секрет из файла конфигурации:\n%s", output)
	assert.False(t, acceptsConn(addr), "адрес %s слушается несмотря на отказ конфигурации", addr)
}

// TestStartupWarnsOnBrokenConfigFile — неразобранный файл остаётся предупреждением: сервис
// поднимается, а доверенная подсеть пустая, то есть эндпоинт закрыт для всех.
func TestStartupWarnsOnBrokenConfigFile(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)
	dir := t.TempDir()
	addr := freeAddr(t)
	auditPath := filepath.Join(dir, "audit.jsonl")

	seedStore(t, dir)

	path := writeStatsConfig(t, dir, `{"trusted_subnet": "10.0.0.0/8", // профиль`+"\n")

	proc := startShorter(t, bin, dir, fileModeEnv(addr, dir, auditPath), "-c", path)

	waitServed(t, addr, proc)

	assert.Contains(t, proc.output.String(), "конфигурация из файла не применена",
		"в выводе нет предупреждения о неразобранном файле:\n%s", proc.output.String())

	code, body := statsRequest(t, addr, http.MethodGet, nil)

	assert.Equal(t, http.StatusForbidden, code, "эндпоинт открыт при пустой доверенной подсети: %q", body)
	assertNoCounters(t, body)
}

// TestInternalStatsClosedWithoutSubnet — без настройки эндпоинт закрыт для любого запроса, включая
// локальный, а кука авторизации для него не требуется и прочим маршрутам не мешает.
func TestInternalStatsClosedWithoutSubnet(t *testing.T) {
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

	for _, tt := range []struct {
		header http.Header
		name   string
		method string
	}{
		{name: "GET без заголовка", method: http.MethodGet},
		{name: "GET из подсети по заголовку", method: http.MethodGet, header: realIPHeader("10.0.0.5")},
		{name: "POST", method: http.MethodPost},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, body := statsRequest(t, addr, tt.method, tt.header)

			assert.Equal(t, http.StatusForbidden, code, "%s: тело %q", tt.name, body)
			assertNoCounters(t, body)
		})
	}

	// Остальные маршруты работают как до изменения: переход по ссылке и требование куки.
	resp, err := http.Get("http://" + addr + "/abc") //nolint:noctx // ответ нужен только здесь
	if err != nil {
		t.Fatalf("переход по ссылке не выполнен: %v", err)
	}

	_ = resp.Body.Close()

	assert.NotContains(t, []int{http.StatusForbidden, http.StatusUnauthorized}, resp.StatusCode,
		"переход по ссылке %s получил код отказа", "/abc")

	userResp, err := http.Get("http://" + addr + "/api/user/urls") //nolint:noctx // проверяется отсутствие куки
	if err != nil {
		t.Fatalf("запрос списка ссылок не выполнен: %v", err)
	}

	_ = userResp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, userResp.StatusCode, "список ссылок без куки отвечает не отказом авторизации")
}

// TestInternalStatsTrustedSubnetGate проверяет гейт на живом сервере: адрес соединения и адрес из
// X-Real-IP решают, дойдёт ли запрос до счётчиков, а отказ по сети приходит раньше отказа по базе.
func TestInternalStatsTrustedSubnetGate(t *testing.T) {
	if testing.Short() {
		t.Skip("сквозная проверка сервера не запускается с -short")
	}

	bin := buildShorter(t)
	dir := t.TempDir()
	addr := freeAddr(t)
	auditPath := filepath.Join(dir, "audit.jsonl")

	seedStore(t, dir)

	proc := startShorter(t, bin, dir, fileModeEnv(addr, dir, auditPath), "-t", "10.0.0.0/8")

	waitServed(t, addr, proc)

	tests := []struct {
		header   http.Header
		name     string
		wantCode int
	}{
		// Соединение с localhost вне заданной сети.
		{name: "без заголовка", wantCode: http.StatusForbidden},
		{name: "заголовок из подсети", wantCode: http.StatusServiceUnavailable, header: realIPHeader("10.0.0.5")},
		{name: "заголовок вне подсети", wantCode: http.StatusForbidden, header: realIPHeader("8.8.8.8")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, body := statsRequest(t, addr, http.MethodGet, tt.header)

			assert.Equal(t, tt.wantCode, code, "%s: тело %q", tt.name, body)

			// 503 — база недоступна в этом запуске: тело обязано остаться пустым, как и при 403.
			assertNoCounters(t, body)
		})
	}

	// Испорченная кука не должна превращать статистику в 401: эндпоинт её не читает.
	brokenCookie := http.Header{}
	brokenCookie.Set("Cookie", "user_id=не-подпись")

	code, body := statsRequest(t, addr, http.MethodGet, brokenCookie)

	assert.Equal(t, http.StatusForbidden, code, "запрос с битой кукой прошёл гейт: %q", body)
	assertNoCounters(t, body)

	// Отдельно про локальную сеть: запрос с обратной петли проходит, и остаётся только недоступность
	// базы — она в этом контуре без DATABASE_DSN ожидаема.
	localDir := t.TempDir()
	localAddr := freeAddr(t)

	local := startShorter(t, bin, localDir,
		fileModeEnv(localAddr, localDir, filepath.Join(localDir, "audit.jsonl")), "-t", "127.0.0.1/32")

	waitServed(t, localAddr, local)

	code, body = statsRequest(t, localAddr, http.MethodGet, nil)

	assert.NotEqual(t, http.StatusForbidden, code, "запрос с обратной петли отбит настройкой 127.0.0.1/32: %q", body)
	assert.Equal(t, http.StatusServiceUnavailable, code, "без базы счётчики отвечают не 503: %q", body)
}

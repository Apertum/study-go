package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// statsFake — двойник источника счётчиков: запоминает текст и порядок запросов и отвечает
// значениями либо ошибкой по тексту запроса.
type statsFake struct {
	values map[string]int64
	errs   map[string]error
	hook   func(ctx context.Context, query string)
	order  []string
}

func (f *statsFake) row(ctx context.Context, query string) statsRow {
	f.order = append(f.order, query)

	if f.hook != nil {
		f.hook(ctx, query)
	}

	return statsFakeRow{value: f.values[query], err: f.errs[query]}
}

// statsFakeRow — строка с числом, заданным фикстурой.
type statsFakeRow struct {
	err   error
	value int64
}

func (r statsFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}

	target, ok := dest[0].(*int64)
	if !ok {
		return errors.New("в двойнике счётчиков принимается только *int64")
	}

	*target = r.value

	return nil
}

// countingStats — читатель, считающий обращения, для проверки порядка «гейт раньше базы».
type countingStats struct {
	queries int
}

func (c *countingStats) row(context.Context, string) statsRow {
	c.queries++

	return statsFakeRow{value: 1}
}

// countsRequest — GET эндпоинта статистики.
func countsRequest() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/api/internal/stats", nil)
}

func TestShorterStatsCounts(t *testing.T) {
	reader := &statsFake{values: map[string]int64{statsURLsQuery: 3, statsUsersQuery: 2}}

	rec := httptest.NewRecorder()
	ShorterStats(reader).ServeHTTP(rec, countsRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, ожидали 200", rec.Code)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("тип ответа %q, ожидали application/json", got)
	}

	if got := strings.TrimSpace(rec.Body.String()); got != `{"urls":3,"users":2}` {
		t.Errorf("тело %q, ожидали {\"urls\":3,\"users\":2}", got)
	}

	want := []string{statsURLsQuery, statsUsersQuery}
	if len(reader.order) != len(want) {
		t.Fatalf("запросов %d (%v), ожидали %v", len(reader.order), reader.order, want)
	}

	for i, query := range want {
		if reader.order[i] != query {
			t.Errorf("запрос %d = %q, ожидали %q", i, reader.order[i], query)
		}
	}

	if !strings.Contains(statsURLsQuery, "deleted") {
		t.Errorf("в запросе счётчика сокращений %q нет фильтра удалённых", statsURLsQuery)
	}
}

// TestShorterStatsEmptyStorage сверяет пустую базу с нулями: 0 — значение, а не отсутствие данных.
func TestShorterStatsEmptyStorage(t *testing.T) {
	reader := &statsFake{values: map[string]int64{}}

	rec := httptest.NewRecorder()
	ShorterStats(reader).ServeHTTP(rec, countsRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, ожидали 200", rec.Code)
	}

	if got := strings.TrimSpace(rec.Body.String()); got != `{"urls":0,"users":0}` {
		t.Errorf("тело %q, ожидали нули", got)
	}
}

// TestShorterStatsShape проверяет форму ответа стандартным разборщиком: ровно два поля-числа.
func TestShorterStatsShape(t *testing.T) {
	reader := &statsFake{values: map[string]int64{statsURLsQuery: 12, statsUsersQuery: 7}}

	rec := httptest.NewRecorder()
	ShorterStats(reader).ServeHTTP(rec, countsRequest())

	var fields map[string]json.RawMessage

	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatalf("тело %q не разбирается: %v", rec.Body.String(), err)
	}

	if len(fields) != 2 {
		t.Fatalf("полей %d (%v), ожидали urls и users", len(fields), rec.Body.String())
	}

	for _, name := range []string{"urls", "users"} {
		raw, ok := fields[name]
		if !ok {
			t.Fatalf("поля %s нет в %q", name, rec.Body.String())
		}

		var count int64
		if err := json.Unmarshal(raw, &count); err != nil {
			t.Errorf("поле %s = %s не является числом", name, raw)
		}
	}
}

// TestShorterStatsWithoutSource отвечает 503 без паники: сервис работает и без базы, и в файловом
// режиме, а нулевой интерфейс не должен привести к обращению к полям отсутствующего пула соединений.
func TestShorterStatsWithoutSource(t *testing.T) {
	tests := []struct {
		reader StatsReader
		name   string
	}{
		{name: "источник не задан"},
		{name: "соединение с базой отсутствует", reader: StatsFromDB(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			ShorterStats(tt.reader).ServeHTTP(rec, countsRequest())

			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("код %d, ожидали 503", rec.Code)
			}

			if body := rec.Body.String(); strings.Contains(body, "urls") || strings.Contains(body, "users") {
				t.Errorf("тело отказа содержит счётчики: %q", body)
			}
		})
	}

	// Тот же обработчик внутри настоящего сервера: соединение не зависает, сервер остаётся жив.
	server := httptest.NewServer(ShorterStats(nil))
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/api/internal/stats") //nolint:noctx // проверяется ответ живого сервера
	if err != nil {
		t.Fatalf("запрос к серверу не выполнен: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("код %d, ожидали 503", resp.StatusCode)
	}

	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("тело не прочитано: %v", err)
	}
}

// TestShorterStatsSourceErrors сверяет недоступность источников с 503: ошибка любого запроса и
// истечение срока ожидания дают отказ без счётчиков.
func TestShorterStatsSourceErrors(t *testing.T) {
	tests := []struct {
		reader StatsReader
		name   string
	}{
		{
			name: "счётчик сокращений недоступен",
			reader: &statsFake{
				values: map[string]int64{statsUsersQuery: 2},
				errs:   map[string]error{statsURLsQuery: errors.New("pq: отношение не найдено")},
			},
		},
		{
			name: "счётчик пользователей недоступен",
			reader: &statsFake{
				values: map[string]int64{statsURLsQuery: 3},
				errs:   map[string]error{statsUsersQuery: errors.New("pq: отношение не найдено")},
			},
		},
		{
			name: "срок ожидания истёк",
			reader: &statsFake{
				errs: map[string]error{statsURLsQuery: context.DeadlineExceeded, statsUsersQuery: context.DeadlineExceeded},
			},
		},
	}

	var log bytes.Buffer

	out := logrus.StandardLogger().Out
	logrus.SetOutput(&log)

	t.Cleanup(func() { logrus.SetOutput(out) })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log.Reset()

			rec := httptest.NewRecorder()

			ShorterStats(tt.reader).ServeHTTP(rec, countsRequest())

			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("код %d, ожидали 503", rec.Code)
			}

			if body := rec.Body.String(); body != "" {
				t.Errorf("тело отказа непустое: %q", body)
			}

			if !strings.Contains(log.String(), "статистика недоступна") {
				t.Errorf("в журнале нет причины отказа: %q", log.String())
			}
		})
	}
}

// TestShorterStatsTimeoutBounds проверяет, что источник получает контекст со сроком ожидания
// эндпоинта, и что отменённый запрос клиента не держит обращение к базе.
func TestShorterStatsTimeoutBounds(t *testing.T) {
	var deadlines []time.Duration

	reader := &statsFake{
		values: map[string]int64{},
		hook: func(ctx context.Context, _ string) {
			deadline, ok := ctx.Deadline()
			if !ok {
				deadlines = append(deadlines, 0)

				return
			}

			deadlines = append(deadlines, time.Until(deadline).Round(time.Second))
		},
	}

	rec := httptest.NewRecorder()
	ShorterStats(reader).ServeHTTP(rec, countsRequest())

	if len(deadlines) != 2 {
		t.Fatalf("сроков задано %d, ожидали два", len(deadlines))
	}

	for _, until := range deadlines {
		if until <= 0 || until > statsQueryTimeout {
			t.Errorf("до срока ожидания %v, ожидали промежуток до %v", until, statsQueryTimeout)
		}
	}

	// Отменённый родительский контекст доходит до источника: двойник видит отмену, обработчик
	// отвечает сразу, а не ждёт полный срок.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	blocked := &statsFake{errs: map[string]error{statsURLsQuery: context.Canceled}}

	req := countsRequest().WithContext(ctx)

	start := time.Now()

	rec = httptest.NewRecorder()
	ShorterStats(blocked).ServeHTTP(rec, req)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("ответ через %v, ожидали немедленный после отмены", elapsed)
	}

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("код %d, ожидали 503", rec.Code)
	}
}

// TestTrustedSubnetGateRunsBeforeStorage подтверждает порядок: при отказе по сети ни один счётчик
// не запрашивается — в том числе когда база недоступна и ответ был бы 503.
func TestTrustedSubnetGateRunsBeforeStorage(t *testing.T) {
	prefix := mustPrefix(t, "10.0.0.0/8")

	tests := []struct {
		name       string
		prefix     netip.Prefix
		reader     StatsReader
		remoteAddr string
		realIP     []string
	}{
		{
			name:       "настройка не задана",
			reader:     &statsFake{values: map[string]int64{}},
			remoteAddr: "10.9.9.9:45678",
		},
		{
			name:       "адрес вне подсети",
			prefix:     prefix,
			reader:     &statsFake{values: map[string]int64{}},
			remoteAddr: "192.0.2.1:45678",
		},
		{
			name:       "адрес вне подсети, заголовок из подсети",
			prefix:     prefix,
			reader:     &statsFake{values: map[string]int64{}},
			remoteAddr: "192.0.2.1:45678",
			realIP:     []string{"8.8.8.8"},
		},
		{
			name:       "база недоступна",
			prefix:     prefix,
			remoteAddr: "192.0.2.1:45678",
		},
		{
			name:       "адрес не разобран",
			prefix:     prefix,
			reader:     &statsFake{values: map[string]int64{}},
			remoteAddr: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := &countingStats{}

			reader := tt.reader
			if reader == nil {
				reader = counter
			}

			handler := TrustedSubnetGate(tt.prefix)(ShorterStats(reader))

			req := countsRequest()
			req.RemoteAddr = tt.remoteAddr

			for _, value := range tt.realIP {
				req.Header.Add(headerRealIP, value)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("код %d, ожидали 403", rec.Code)
			}

			if counter.queries != 0 {
				t.Errorf("к источнику обращались %d раз при отказе по сети", counter.queries)
			}
		})
	}

	// Контроль самого двойника: разрешённый запрос к счётчикам всё-таки доходит.
	counter := &countingStats{}

	handler := TrustedSubnetGate(prefix)(ShorterStats(counter))

	req := countsRequest()
	req.RemoteAddr = "10.9.9.9:45678"

	handler.ServeHTTP(httptest.NewRecorder(), req)

	if counter.queries != 2 {
		t.Errorf("обращений к источнику %d, ожидали два", counter.queries)
	}
}

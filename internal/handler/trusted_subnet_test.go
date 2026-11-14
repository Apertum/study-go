package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// mustPrefix разбирает CIDR фикстуры; кривая фикстура — падение теста, а не вариант ответа.
func mustPrefix(t *testing.T, cidr string) netip.Prefix {
	t.Helper()

	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		t.Fatalf("фикстура %q не разбирается как CIDR: %v", cidr, err)
	}

	return prefix
}

// statsProbe — обработчик «внутреннего» эндпоинта за гейтом: считает попадания и отвечает формой,
// отличимой от отказа, чтобы тест не принял прохождение гейта за отказ.
func statsProbe(hits *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*hits++

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"urls":3,"users":2}`))
	})
}

// gateRequest отправляет GET /api/internal/stats через гейт с адресом соединения и значениями
// X-Real-IP и возвращает ответ вместе с числом попаданий во внутренний обработчик. Значения
// заголовка добавляются по одному, иначе net/http склеил бы их в одно при чтении.
func gateRequest(t *testing.T, prefix netip.Prefix, remoteAddr string, realIP ...string) (*httptest.ResponseRecorder, int) {
	t.Helper()

	hits := 0

	req := httptest.NewRequest(http.MethodGet, "/api/internal/stats", nil)
	req.RemoteAddr = remoteAddr

	for _, value := range realIP {
		req.Header.Add(headerRealIP, value)
	}

	rec := httptest.NewRecorder()
	TrustedSubnetGate(prefix)(statsProbe(&hits)).ServeHTTP(rec, req)

	return rec, hits
}

// TestTrustedSubnetGateDeniesWhenUnset — пустая настройка закрывает эндпоинт для всех, включая
// адрес обратной петли и запросы без разбораемого адреса соединения: «не задано» не значит «открыто».
func TestTrustedSubnetGateDeniesWhenUnset(t *testing.T) {
	for _, remoteAddr := range []string{"127.0.0.1:51234", "[::1]:51234", "fd12::1:51234", "не-адрес"} {
		t.Run("соединение "+remoteAddr, func(t *testing.T) {
			for _, realIP := range [][]string{{}, {"10.0.0.5"}} {
				rec, hits := gateRequest(t, netip.Prefix{}, remoteAddr, realIP...)

				if rec.Code != http.StatusForbidden {
					t.Errorf("код %d, ожидали 403", rec.Code)
				}

				if hits != 0 {
					t.Errorf("обработчик выполнен %d раз при незакрытой настройке", hits)
				}

				if body := rec.Body.String(); body != "" {
					t.Errorf("тело отказа непустое: %q", body)
				}
			}
		})
	}
}

func TestTrustedSubnetGateAddressSource(t *testing.T) {
	prefix := mustPrefix(t, "10.0.0.0/8")

	tests := []struct {
		name       string
		remoteAddr string
		realIP     []string
		wantCode   int
		wantServed bool
	}{
		{
			name:       "заголовок из подсети при внутреннем соединении",
			remoteAddr: "10.1.2.3:45678",
			realIP:     []string{"10.9.9.9"},
			wantCode:   http.StatusOK,
			wantServed: true,
		},
		{
			name:       "заголовок вне подсети при внутреннем соединении",
			remoteAddr: "10.1.2.3:45678",
			realIP:     []string{"8.8.8.8"},
			wantCode:   http.StatusForbidden,
		},
		{
			// Компромисс design.md: за обратным прокси соединение приходит от адреса балансировщика.
			name:       "заголовок из подсети при внешнем соединении",
			remoteAddr: "192.0.2.1:45678",
			realIP:     []string{"10.9.9.9"},
			wantCode:   http.StatusOK,
			wantServed: true,
		},
		{
			name:       "заголовка нет — действует адрес соединения",
			remoteAddr: "10.9.9.9:45678",
			wantCode:   http.StatusOK,
			wantServed: true,
		},
		{
			name:       "заголовка нет, соединение извне",
			remoteAddr: "192.0.2.1:45678",
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "заголовок не разбирается, адрес соединения из подсети",
			remoteAddr: "10.9.9.9:45678",
			realIP:     []string{"not-an-ip"},
			wantCode:   http.StatusOK,
			wantServed: true,
		},
		{
			name:       "заголовок не разбирается, адрес соединения извне",
			remoteAddr: "192.0.2.1:45678",
			realIP:     []string{"not-an-ip"},
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "ни один источник не разбирается",
			remoteAddr: "unknown",
			realIP:     []string{"not-an-ip"},
			wantCode:   http.StatusForbidden,
		},
		{
			// Неоднозначный заголовок не считается адресом: остаётся адрес соединения.
			name:       "два значения заголовка",
			remoteAddr: "10.9.9.9:45678",
			realIP:     []string{"10.0.0.5", "8.8.8.8"},
			wantCode:   http.StatusOK,
			wantServed: true,
		},
		{
			name:       "два значения заголовка при внешнем соединении",
			remoteAddr: "192.0.2.1:45678",
			realIP:     []string{"10.0.0.5", "10.0.0.6"},
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "нижняя граница сети включительно",
			remoteAddr: "10.0.0.0:45678",
			wantCode:   http.StatusOK,
			wantServed: true,
		},
		{
			name:       "адрес за границей сети",
			remoteAddr: "11.0.0.0:45678",
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "адрес соединения без порта",
			remoteAddr: "10.9.9.9",
			wantCode:   http.StatusOK,
			wantServed: true,
		},
		{
			name:       "адрес IPv4 в форме IPv4-mapped IPv6",
			remoteAddr: "::ffff:10.9.9.9",
			realIP:     []string{"::ffff:10.0.0.5"},
			wantCode:   http.StatusOK,
			wantServed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, hits := gateRequest(t, prefix, tt.remoteAddr, tt.realIP...)

			if rec.Code != tt.wantCode {
				t.Errorf("код %d, ожидали %d", rec.Code, tt.wantCode)
			}

			if served := hits > 0; served != tt.wantServed {
				t.Errorf("прошёл дальше гейта=%v, ожидали %v", served, tt.wantServed)
			}
		})
	}
}

// TestTrustedSubnetGateIPv6 сверяет пару из постановки: сеть fd00::/8, адрес fd12::1 проходит,
// соседняя сеть отказывает.
func TestTrustedSubnetGateIPv6(t *testing.T) {
	prefix := mustPrefix(t, "fd00::/8")

	tests := []struct {
		remoteAddr string
		realIP     []string
		wantCode   int
	}{
		{remoteAddr: "[fd12::1]:45678", wantCode: http.StatusOK},
		{remoteAddr: "[fe80::1]:45678", wantCode: http.StatusForbidden},
		// Прокси кладёт адрес IPv6 без скобок — скобки нужны только в адресе с портом.
		{remoteAddr: "192.0.2.1:45678", realIP: []string{"fd12::1"}, wantCode: http.StatusOK},
		{remoteAddr: "[fd12::1]:45678", realIP: []string{"fe80::1"}, wantCode: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.remoteAddr, func(t *testing.T) {
			rec, _ := gateRequest(t, prefix, tt.remoteAddr, tt.realIP...)

			if rec.Code != tt.wantCode {
				t.Errorf("код %d, ожидали %d", rec.Code, tt.wantCode)
			}
		})
	}
}

// TestTrustedSubnetGateMaskedSingleAddress сверяет маску /32: доступ ровно у одного адреса.
func TestTrustedSubnetGateMaskedSingleAddress(t *testing.T) {
	prefix := mustPrefix(t, "127.0.0.1/32")

	if rec, _ := gateRequest(t, prefix, "127.0.0.1:45678"); rec.Code != http.StatusOK {
		t.Errorf("код %d, ожидали 200", rec.Code)
	}

	if rec, _ := gateRequest(t, prefix, "127.0.0.2:45678"); rec.Code != http.StatusForbidden {
		t.Errorf("код %d, ожидали 403", rec.Code)
	}
}

// TestTrustedSubnetGateKeepsRequest сверяет, что гейт передаёт обработчику запрос без изменений.
func TestTrustedSubnetGateKeepsRequest(t *testing.T) {
	var gotQuery string

	req := httptest.NewRequest(http.MethodGet, "/api/internal/stats?raw=1", nil)
	req.RemoteAddr = "10.9.9.9:45678"
	req.Header.Set(headerRealIP, "10.0.0.5")

	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
	})

	TrustedSubnetGate(mustPrefix(t, "10.0.0.0/8"))(next).ServeHTTP(httptest.NewRecorder(), req)

	if gotQuery != "raw=1" {
		t.Errorf("заметка обработчика %q, ожидали raw=1", gotQuery)
	}
}

// TestTrustedSubnetGateDeniesQuietly проверяет, что отказ отдаётся без тела и что в журнал не
// попадают адреса клиентов и значение X-Real-IP.
func TestTrustedSubnetGateDeniesQuietly(t *testing.T) {
	var log bytes.Buffer

	out := logrus.StandardLogger().Out
	level := logrus.GetLevel()

	logrus.SetOutput(&log)
	logrus.SetLevel(logrus.DebugLevel)

	t.Cleanup(func() {
		logrus.SetOutput(out)
		logrus.SetLevel(level)
	})

	rec, hits := gateRequest(t, mustPrefix(t, "10.0.0.0/8"), "192.0.2.1:45678", "8.8.8.8")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("код %d, ожидали 403", rec.Code)
	}

	if hits != 0 {
		t.Errorf("обработчик выполнен %d раз при отказе", hits)
	}

	body := rec.Body.String()
	if strings.Contains(body, "urls") || strings.Contains(body, "users") {
		t.Errorf("тело отказа содержит счётчики: %q", body)
	}

	if body != "" {
		t.Errorf("тело отказа непустое: %q", body)
	}

	text := log.String()
	if !strings.Contains(text, "запрещён") {
		t.Errorf("в журнале нет записи об отказе: %q", text)
	}

	if !strings.Contains(text, "/api/internal/stats") {
		t.Errorf("в журнале нет пути запроса: %q", text)
	}

	for _, leak := range []string{"192.0.2.1", "8.8.8.8"} {
		if strings.Contains(text, leak) {
			t.Errorf("в журнале оказался адрес %s: %q", leak, text)
		}
	}
}

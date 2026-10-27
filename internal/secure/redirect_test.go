package secure

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRedirectHandler(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		target       string
		host         string
		tlsAddr      string
		wantLocation string
		wantCode     int
	}{
		{
			name:         "путь и строка запроса сохранены",
			method:       http.MethodGet,
			target:       "/api/user/urls?page=2",
			host:         "localhost:8081",
			tlsAddr:      ":8080",
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "https://localhost:8080/api/user/urls?page=2",
		},
		{
			name:         "POST тоже редиректится тем же путём",
			method:       http.MethodPost,
			target:       "/api/shorten",
			host:         "localhost:8081",
			tlsAddr:      ":8080",
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "https://localhost:8080/api/shorten",
		},
		{
			name:         "пустой Host берёт хост из адреса TLS",
			method:       http.MethodGet,
			target:       "/ping",
			host:         "",
			tlsAddr:      ":8080",
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "https://localhost:8080/ping",
		},
		{
			name:         "IPv6 в Host собран в скобках",
			method:       http.MethodGet,
			target:       "/x",
			host:         "[::1]:8081",
			tlsAddr:      ":8080",
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "https://[::1]:8080/x",
		},
		{
			name:         "Host без порта получает порт TLS",
			method:       http.MethodGet,
			target:       "/x",
			host:         "127.0.0.1",
			tlsAddr:      "localhost:8443",
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "https://127.0.0.1:8443/x",
		},
		{
			name:         "корень редиректится в корень",
			method:       http.MethodGet,
			target:       "/",
			host:         "localhost:8081",
			tlsAddr:      ":8080",
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "https://localhost:8080/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.target, nil)
			req.Host = tt.host

			RedirectHandler(tt.tlsAddr).ServeHTTP(rec, req)

			if got := rec.Code; got != tt.wantCode {
				t.Errorf("код ответа %d, ожидали %d", got, tt.wantCode)
			}

			if got := rec.Header().Get("Location"); got != tt.wantLocation {
				t.Errorf("Location %q, ожидали %q", got, tt.wantLocation)
			}
		})
	}
}

func TestAddrConflict(t *testing.T) {
	tests := []struct {
		name           string
		mainAddr       string
		redirectAddr   string
		wantErrMessage string
	}{
		{name: "разные порты не конфликт", mainAddr: ":8080", redirectAddr: ":8081"},
		{name: "одинаковые адреса конфликт", mainAddr: ":8080", redirectAddr: ":8080", wantErrMessage: "адрес редиректа :8080 совпадает с основным адресом :8080"},
		{name: "одинаковые адреса с хостом конфликт", mainAddr: "localhost:8080", redirectAddr: "localhost:8080", wantErrMessage: "localhost:8080"},
		{name: "пустой хост пересекает любой", mainAddr: "127.0.0.1:8080", redirectAddr: ":8080", wantErrMessage: "совпадает"},
		{name: "разные явные хосты не конфликт", mainAddr: "localhost:8080", redirectAddr: "127.0.0.1:8080"},
		{name: "адрес без порта сравнивается целиком", mainAddr: "localhost", redirectAddr: "localhost", wantErrMessage: "совпадает"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AddrConflict(tt.mainAddr, tt.redirectAddr)

			if tt.wantErrMessage == "" {
				if err != nil {
					t.Fatalf("неожиданная ошибка: %v", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("ожидалась ошибка для %s и %s", tt.mainAddr, tt.redirectAddr)
			}

			if !strings.Contains(err.Error(), tt.wantErrMessage) {
				t.Errorf("в тексте %q нет %q", err, tt.wantErrMessage)
			}
		})
	}
}

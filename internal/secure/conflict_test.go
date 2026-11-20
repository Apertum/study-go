package secure

import (
	"strings"
	"testing"
)

func TestAddrConflictAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		addrs   []ListenAddr
		wantErr bool
		// wantContains — фрагменты, которые обязаны оказаться в тексте ошибки.
		wantContains []string
	}{
		{name: "пустой набор"},
		{name: "один адрес", addrs: []ListenAddr{listenerAddr("основного", "-a", ":8080")}},
		{
			name: "три разных адреса",
			addrs: []ListenAddr{
				listenerAddr("основного", "-a", ":8080"),
				listenerAddr("редиректа", "-http-addr", ":8081"),
				listenerAddr("gRPC", "-grpc-addr", ":8082"),
			},
		},
		{
			name: "gRPC совпал с основным адресом",
			addrs: []ListenAddr{
				listenerAddr("основного HTTP-слушателя", "-a/SERVER_ADDRESS", ":8080"),
				listenerAddr("редиректа", "-http-addr", ":8081"),
				listenerAddr("gRPC-слушателя", "-grpc-addr/GRPC_ADDRESS", ":8080"),
			},
			wantErr:      true,
			wantContains: []string{"gRPC-слушателя", "-grpc-addr/GRPC_ADDRESS", "основного HTTP-слушателя", "-a/SERVER_ADDRESS", ":8080"},
		},
		{
			name: "gRPC совпал с адресом редиректа",
			addrs: []ListenAddr{
				listenerAddr("основного", "-a", "localhost:8080"),
				listenerAddr("редиректа", "-http-addr", "localhost:8082"),
				listenerAddr("gRPC", "-grpc-addr", "localhost:8082"),
			},
			wantErr:      true,
			wantContains: []string{"8082"},
		},
		{
			name: "пустой хост пересекается с явным",
			addrs: []ListenAddr{
				listenerAddr("gRPC", "-grpc-addr", ":8082"),
				listenerAddr("основного", "-a", "localhost:8082"),
			},
			wantErr:      true,
			wantContains: []string{"совпадает"},
		},
		{
			name: "разные явные хосты уживаются",
			addrs: []ListenAddr{
				listenerAddr("gRPC", "-grpc-addr", "localhost:8082"),
				listenerAddr("основного", "-a", "127.0.0.1:8082"),
			},
		},
		{
			name: "адрес без порта сравнивается целиком",
			addrs: []ListenAddr{
				listenerAddr("gRPC", "-grpc-addr", "localhost"),
				listenerAddr("основного", "-a", "localhost"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := AddrConflictAll(tt.addrs...)

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("неожиданная ошибка для %v: %v", tt.addrs, err)
				}

				return
			}

			if err == nil {
				t.Fatalf("ожидалась ошибка для %v", tt.addrs)
			}

			for _, want := range tt.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("в тексте %q нет %q", err, want)
				}
			}
		})
	}
}

// listenerAddr собирает адрес слушателя для таблицы проверок.
func listenerAddr(role, setting, addr string) ListenAddr {
	return ListenAddr{Role: role, Setting: setting, Addr: addr}
}

// TestAddrConflictKeepsLegacyText подтверждает, что двухаргументная проверка сохранена: текст её
// ошибки описывает пару «редирект — основной адрес», как до появления третьего слушателя.
func TestAddrConflictKeepsLegacyText(t *testing.T) {
	t.Parallel()

	err := AddrConflict(":8080", ":8080")
	if err == nil {
		t.Fatal("ожидалась ошибка конфликта")
	}

	for _, want := range []string{"адрес редиректа :8080", "основным адресом :8080"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в тексте %q нет %q", err, want)
		}
	}

	if err := AddrConflict(":8080", ":8081"); err != nil {
		t.Errorf("неожиданная ошибка: %v", err)
	}
}

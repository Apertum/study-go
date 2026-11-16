package config

import (
	"io"
	"strings"
	"testing"
)

// TestParseFlagsGRPCAddress прогоняет матрицу приоритетов настройки адреса gRPC: непустая
// переменная, явно переданный флаг, ключ файла, значение флага по умолчанию.
func TestParseFlagsGRPCAddress(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		file string
		want string
	}{
		{name: "без источников — значение по умолчанию", want: ":8082"},
		{
			name: "флаг задаёт адрес",
			args: []string{"-grpc-addr", ":9999"},
			want: ":9999",
		},
		{
			name: "переменная задаёт адрес без флага",
			env:  map[string]string{EnvGRPCAddress: ":9998"},
			want: ":9998",
		},
		{
			name: "переменная переопределяет явно переданный флаг",
			args: []string{"-grpc-addr", ":9999"},
			env:  map[string]string{EnvGRPCAddress: ":9998"},
			want: ":9998",
		},
		{
			name: "пустая переменная сохраняет флаг",
			args: []string{"-grpc-addr", ":9999"},
			env:  map[string]string{EnvGRPCAddress: ""},
			want: ":9999",
		},
		{
			name: "файл задаёт адрес, когда флаги молчат",
			file: `{"grpc_address": "localhost:7777"}`,
			want: "localhost:7777",
		},
		{
			name: "явный флаг старше файла",
			args: []string{"-grpc-addr", ":9999"},
			file: `{"grpc_address": "localhost:7777"}`,
			want: ":9999",
		},
		{
			name: "адрес IPv6 в скобках разобран",
			args: []string{"-grpc-addr", "[::1]:8082"},
			want: "[::1]:8082",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := tt.args

			if tt.file != "" {
				args = append([]string{"-c", writeConfig(t, tt.file)}, args...)
			}

			parseWith(t, args, tt.env)

			if GRPCAddr != tt.want {
				t.Errorf("адрес gRPC %q, ожидали %q", GRPCAddr, tt.want)
			}
		})
	}
}

func TestCheckListenAddr(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "адрес без хоста", raw: ":8082"},
		{name: "имя хоста и порт", raw: "localhost:8082"},
		{name: "IPv6 в скобках", raw: "[::1]:8082"},
		{name: "порт 1 допустим", raw: ":1"},
		{name: "верхняя граница порта", raw: ":65535"},
		{name: "без порта", raw: "localhost", wantErr: true},
		{name: "только порт", raw: "8082", wantErr: true},
		{name: "пустое значение", raw: "", wantErr: true},
		{name: "порт не число", raw: ":grpc", wantErr: true},
		{name: "порт за диапазоном", raw: ":65536", wantErr: true},
		{name: "порт 0 зарезервирован", raw: ":0", wantErr: true},
		{name: "не адрес", raw: "не-адрес", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckListenAddr("-grpc-addr", tt.raw)

			switch {
			case tt.wantErr && err == nil:
				t.Fatalf("ожидали ошибку разбора %q", tt.raw)
			case tt.wantErr:
				if !strings.Contains(err.Error(), "-grpc-addr") || !strings.Contains(err.Error(), tt.raw) {
					t.Errorf("в ошибке %q нет источника и значения настройки", err)
				}
			case err != nil:
				t.Fatalf("неожиданная ошибка для %q: %v", tt.raw, err)
			}
		})
	}
}

// TestParseFlagsBadGRPCAddressFromEnv — переменная называется в ошибке и переопределяет флаг;
// прочая конфигурация к моменту отказа не применена (адрес HTTP задан переменной, чтобы он не
// попал в Addr самим разбором аргументов).
func TestParseFlagsBadGRPCAddressFromEnv(t *testing.T) {
	err := runParse(t, []string{"-grpc-addr", ":9999"},
		map[string]string{EnvGRPCAddress: "не-адрес", "SERVER_ADDRESS": ":6060"}, io.Discard)
	if err == nil {
		t.Fatal("ожидали ошибку неразбираемого адреса gRPC")
	}

	if !strings.Contains(err.Error(), EnvGRPCAddress) || !strings.Contains(err.Error(), "не-адрес") {
		t.Errorf("в ошибке %q нет имени переменной и значения", err)
	}

	if Addr != ":8080" {
		t.Errorf("адрес HTTP изменён до ошибки разбора gRPC: %q", Addr)
	}
}

// TestParseFlagsBadGRPCAddressFromFile — источник в ошибке назван ключом файла, а секретные
// значения того же файла в текст не попадают.
func TestParseFlagsBadGRPCAddressFromFile(t *testing.T) {
	path := writeConfig(t, `{"grpc_address": "без-порта", "cookie_key": "`+secretCookie+`"}`)

	err := runParse(t, []string{"-c", path}, nil, io.Discard)
	if err == nil {
		t.Fatal("ожидали ошибку неразбираемого адреса из файла")
	}

	if !strings.Contains(err.Error(), "grpc_address") {
		t.Errorf("в ошибке %q нет ключа файла", err)
	}

	if strings.Contains(err.Error(), secretCookie) {
		t.Errorf("в ошибке %q оказалось содержимое файла", err)
	}
}

package secure

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startTLSServer поднимает сервер на материале TLS и возвращает адрес слушателя.
func startTLSServer(t *testing.T, material Material) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("слушатель не создан: %v", err)
	}

	server := &http.Server{
		Handler:   http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		TLSConfig: material.ServerTLS(),
	}

	go func() { _ = server.ServeTLS(listener, "", "") }()

	t.Cleanup(func() { _ = server.Close() })

	return listener.Addr().String()
}

// writeMaterialCert кладёт PEM сертификата во временный файл и возвращает путь.
func writeMaterialCert(t *testing.T, material Material) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, material.CertPEM, 0o644); err != nil {
		t.Fatalf("сертификат не записан: %v", err)
	}

	return path
}

func TestServerTLSMinVersion(t *testing.T) {
	dir := t.TempDir()

	material, err := LoadOrGenerate(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		t.Fatalf("материал не выпущен: %v", err)
	}

	addr := startTLSServer(t, material)

	tests := []struct {
		name    string
		version uint16
		wantErr bool
	}{
		{name: "TLS 1.1 отклоняется", version: tls.VersionTLS11, wantErr: true},
		{name: "TLS 1.2 принимается", version: tls.VersionTLS12},
		{name: "TLS 1.3 принимается", version: tls.VersionTLS13},
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(material.CertPEM) {
		t.Fatal("сертификат не попал в пул корней")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := tls.Dial("tcp", addr, &tls.Config{
				RootCAs:    pool,
				ServerName: localhost,
				MinVersion: tt.version,
				MaxVersion: tt.version,
			})

			if tt.wantErr {
				if err == nil {
					_ = conn.Close()
					t.Fatal("рукопожатие состоялось, хотя не должно")
				}

				return
			}

			if err != nil {
				t.Fatalf("рукопожатие не состоялось: %v", err)
			}

			if got := conn.ConnectionState().Version; got != tt.version {
				t.Errorf("договорились о версии %#x, ожидали %#x", got, tt.version)
			}

			if err := conn.Close(); err != nil {
				t.Errorf("соединение не закрылось: %v", err)
			}
		})
	}
}

func TestClientTLSErrors(t *testing.T) {
	dir := t.TempDir()

	if _, err := LoadOrGenerate(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")); err != nil {
		t.Fatalf("материал не выпущен: %v", err)
	}

	garbagePath := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(garbagePath, []byte("просто текст без PEM"), 0o644); err != nil {
		t.Fatalf("фикстура не записана: %v", err)
	}

	keyPath := filepath.Join(dir, "key.pem")

	tests := []struct {
		name     string
		certPath string
	}{
		{name: "файла нет", certPath: filepath.Join(dir, "нет-такого.pem")},
		{name: "файл не сертификат", certPath: garbagePath},
		{name: "вместо сертификата закрытый ключ", certPath: keyPath},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ClientTLS(tt.certPath)
			if err == nil {
				t.Fatalf("ожидалась ошибка, получили конфигурацию %+v", cfg)
			}

			if !strings.Contains(err.Error(), tt.certPath) {
				t.Errorf("в тексте %q нет пути %q", err, tt.certPath)
			}
		})
	}
}

func TestClientTLSTrustsOnlyGivenCertificate(t *testing.T) {
	dir := t.TempDir()

	material, err := LoadOrGenerate(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if err != nil {
		t.Fatalf("материал не выпущен: %v", err)
	}

	foreignMaterial, err := LoadOrGenerate(filepath.Join(dir, "foreign-cert.pem"), filepath.Join(dir, "foreign-key.pem"))
	if err != nil {
		t.Fatalf("чужой материал не выпущен: %v", err)
	}

	addr := startTLSServer(t, material)
	cfg, err := ClientTLS(writeMaterialCert(t, material))
	if err != nil {
		t.Fatalf("конфигурация клиента не собрана: %v", err)
	}

	tests := []struct {
		cfg        *tls.Config
		name       string
		serverName string
		address    string
		wantErr    bool
	}{
		{name: "имя из сертификата принимается", serverName: localhost, address: addr, cfg: cfg},
		{name: "адрес loopback принимается", serverName: "127.0.0.1", address: addr, cfg: cfg},
		{name: "имя вне сертификата отклоняется", serverName: "example.invalid", address: addr, cfg: cfg, wantErr: true},
		{name: "чужой корень отклоняется", serverName: localhost, address: addr, cfg: clientConfigWithRoot(t, foreignMaterial), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg.Clone()
			cfg.ServerName = tt.serverName

			conn, err := tls.Dial("tcp", tt.address, cfg)
			if tt.wantErr {
				if err == nil {
					_ = conn.Close()
					t.Fatal("рукопожатие состоялось, хотя сертификат не доверенный")
				}

				var unknownAuth x509.UnknownAuthorityError
				var hostnameErr x509.HostnameError

				if !errors.As(err, &unknownAuth) && !errors.As(err, &hostnameErr) && !strings.Contains(err.Error(), "certificate") {
					t.Errorf("ошибка %q не про проверку сертификата", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("рукопожатие не состоялось: %v", err)
			}

			if err := conn.Close(); err != nil {
				t.Errorf("соединение не закрылось: %v", err)
			}
		})
	}
}

// clientConfigWithRoot возвращает конфигурацию клиента, которая доверяет чужому сертификату.
func clientConfigWithRoot(t *testing.T, material Material) *tls.Config {
	t.Helper()

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(material.CertPEM) {
		t.Fatal("чужой сертификат не попал в пул корней")
	}

	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

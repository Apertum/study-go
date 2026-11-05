package secure

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// captureLogs перехватывает журналирование пакета и возвращает буфер с записями.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	buf := &bytes.Buffer{}
	out := logrus.StandardLogger().Out
	level := logrus.GetLevel()

	logrus.SetOutput(buf)
	logrus.SetLevel(logrus.InfoLevel)

	t.Cleanup(func() {
		logrus.SetOutput(out)
		logrus.SetLevel(level)
	})

	return buf
}

// expiredPairPEM делает самоподписанную пару с истёкшим сроком действия.
func expiredPairPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("не выпущен ключ: %v", err)
	}

	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: localhost},
		NotBefore:             time.Now().Add(-2 * time.Hour),
		NotAfter:              time.Now().Add(-time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{localhost},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("не выпущен сертификат: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("не кодирован ключ: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: certBlock, Bytes: certDER}),
		pem.EncodeToMemory(&pem.Block{Type: keyBlock, Bytes: keyDER})
}

func TestLoadOrGenerateIssuesUsableSelfSignedPair(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	logs := captureLogs(t)

	material, err := LoadOrGenerate(certPath, keyPath)
	if err != nil {
		t.Fatalf("пара не выпущена: %v", err)
	}

	if len(material.Cert.Certificate) != 1 {
		t.Fatalf("в материале %d сертификатов, ожидался 1", len(material.Cert.Certificate))
	}

	if material.Cert.PrivateKey == nil {
		t.Error("в материале нет закрытого ключа")
	}

	leaf, err := x509.ParseCertificate(material.Cert.Certificate[0])
	if err != nil {
		t.Fatalf("сертификат не разбирается: %v", err)
	}

	if leaf.IsCA {
		t.Error("выпущен центр сертификации, а не серверный сертификат")
	}

	if leaf.Issuer.String() != leaf.Subject.String() {
		t.Errorf("издатель %q не равен субъекту %q — сертификат не самоподписанный", leaf.Issuer, leaf.Subject)
	}

	if sigErr := leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature); sigErr != nil {
		t.Errorf("подпись сертификата не проверяется его же открытым ключом: %v", sigErr)
	}

	if leaf.SignatureAlgorithm != x509.ECDSAWithSHA256 {
		t.Errorf("подпись %s, ожидали ECDSA с SHA-256", leaf.SignatureAlgorithm)
	}

	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("открытый ключ %T, ожидали ECDSA", leaf.PublicKey)
	}

	if pub.Curve != elliptic.P256() {
		t.Errorf("кривая %s, ожидали P-256", pub.Params().Name)
	}

	if leaf.SerialNumber.BitLen() < serialBits/2 {
		t.Errorf("серийный номер всего %d бит", leaf.SerialNumber.BitLen())
	}

	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 || leaf.KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		t.Errorf("ключевые назначения %d, ожидали DigitalSignature и KeyEncipherment", leaf.KeyUsage)
	}

	if !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		t.Errorf("в расширенных назначениях %v нет ServerAuth", leaf.ExtKeyUsage)
	}

	if !slices.Contains(leaf.DNSNames, localhost) {
		t.Errorf("в именах %v нет %s", leaf.DNSNames, localhost)
	}

	if host, hostErr := os.Hostname(); hostErr == nil && host != "" && host != localhost && !slices.Contains(leaf.DNSNames, host) {
		t.Errorf("в именах %v нет имени машины %s", leaf.DNSNames, host)
	}

	for _, ip := range []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback} {
		found := false

		for _, got := range leaf.IPAddresses {
			found = found || got.Equal(ip)
		}

		if !found {
			t.Errorf("в адресах %v нет %s", leaf.IPAddresses, ip)
		}
	}

	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		t.Errorf("текущий момент вне окна %s—%s", leaf.NotBefore, leaf.NotAfter)
	}

	if want := now.Add(validityPeriod); leaf.NotAfter.Before(want.Add(-time.Minute)) {
		t.Errorf("сертификат истекает %s, ожидали не раньше %s", leaf.NotAfter, want)
	}

	certInfo, err := os.Stat(certPath)
	if err != nil {
		t.Fatalf("сертификат не записан: %v", err)
	}

	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("ключ не записан: %v", err)
	}

	assertPerm(t, certPath, certPerm, certInfo)
	assertPerm(t, keyPath, keyPerm, keyInfo)

	assertNoKeyMaterial(t, logs.String(), keyPath)
}

// assertPerm проверяет права файла там, где файловая система их различает: Windows отдаёт
// 0666 для любого файла, а доступ там определяют ACL, которых в os.FileMode нет.
func assertPerm(t *testing.T, path string, want os.FileMode, info os.FileInfo) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Logf("%s: права файла в Windows неотличимы (%v), проверка пропущена", filepath.Base(path), info.Mode().Perm())

		return
	}

	if got := info.Mode().Perm(); got != want {
		t.Errorf("права %s %v, ожидали %v", filepath.Base(path), got, want)
	}
}

func TestLoadOrGenerateReusesExistingPair(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if _, err := LoadOrGenerate(certPath, keyPath); err != nil {
		t.Fatalf("первый выпуск не удался: %v", err)
	}

	wantCert, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("сертификат не читается: %v", err)
	}

	wantKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("ключ не читается: %v", err)
	}

	logs := captureLogs(t)

	_, err = LoadOrGenerate(certPath, keyPath)
	if err != nil {
		t.Fatalf("повторный запуск не удался: %v", err)
	}

	gotCert, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("сертификат после повтора не читается: %v", err)
	}

	gotKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("ключ после повтора не читается: %v", err)
	}

	if !bytes.Equal(wantCert, gotCert) || !bytes.Equal(wantKey, gotKey) {
		t.Error("повторный запуск перевыпустил материалы")
	}

	if text := logs.String(); strings.Contains(text, "выпускаю") || strings.Contains(text, "выпустил") {
		t.Errorf("повторный запуск сообщил о перевыпуске: %s", text)
	}
}

func TestLoadOrGenerateReissues(t *testing.T) {
	tests := []struct {
		material func(t *testing.T, certPath, keyPath string)
		name     string
	}{
		{
			name: "нет файла ключа",
			material: func(t *testing.T, certPath, keyPath string) {
				t.Helper()
				writeFile(t, certPath, []byte("не PEM"))
			},
		},
		{
			name: "нет файла сертификата",
			material: func(t *testing.T, certPath, keyPath string) {
				t.Helper()
				writeFile(t, keyPath, []byte("не PEM"))
			},
		},
		{
			name: "файлы не разбираются",
			material: func(t *testing.T, certPath, keyPath string) {
				t.Helper()
				writeFile(t, certPath, []byte("каракули"))
				writeFile(t, keyPath, []byte("каракули"))
			},
		},
		{
			name: "срок действия истёк",
			material: func(t *testing.T, certPath, keyPath string) {
				t.Helper()
				certPEM, keyPEM := expiredPairPEM(t)
				writeFile(t, certPath, certPEM)
				writeFile(t, keyPath, keyPEM)
			},
		},
		{
			name: "ключ из другой пары",
			material: func(t *testing.T, certPath, keyPath string) {
				t.Helper()
				first := filepath.Join(t.TempDir(), "cert.pem")
				firstKey := filepath.Join(t.TempDir(), "key.pem")

				if _, err := LoadOrGenerate(first, firstKey); err != nil {
					t.Fatalf("первая пара не выпущена: %v", err)
				}

				certPEM, err := os.ReadFile(first)
				if err != nil {
					t.Fatalf("сертификат первой пары не читается: %v", err)
				}

				keyPEM, err := os.ReadFile(keyPath)
				if err != nil {
					t.Fatalf("ключ целевой пары не читается: %v", err)
				}

				writeFile(t, certPath, certPEM)
				writeFile(t, keyPath, keyPEM)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			certPath := filepath.Join(dir, "cert.pem")
			keyPath := filepath.Join(dir, "key.pem")

			if _, err := LoadOrGenerate(certPath, keyPath); err != nil {
				t.Fatalf("исходная пара не выпущена: %v", err)
			}

			tt.material(t, certPath, keyPath)

			logs := captureLogs(t)

			material, err := LoadOrGenerate(certPath, keyPath)
			if err != nil {
				t.Fatalf("перевыпуск не удался: %v", err)
			}

			if len(material.Cert.Certificate) != 1 || material.Cert.PrivateKey == nil {
				t.Fatal("перевыпущенный материал непригоден")
			}

			if text := logs.String(); !strings.Contains(text, "выпускаю новую") {
				t.Errorf("в диагностике %q нет причины перевыпуска", text)
			}

			certPEM, err := os.ReadFile(certPath)
			if err != nil {
				t.Fatalf("сертификат не перезаписан: %v", err)
			}

			keyPEM, err := os.ReadFile(keyPath)
			if err != nil {
				t.Fatalf("ключ не перезаписан: %v", err)
			}

			if _, err := newMaterial(certPEM, keyPEM); err != nil {
				t.Errorf("перезаписанные файлы не образуют пару: %v", err)
			}

			assertNoKeyMaterial(t, logs.String(), keyPath)
		})
	}
}

func TestLoadOrGenerateUnwritableDirectory(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "нет-такого-каталога", "cert.pem")
	keyPath := filepath.Join(t.TempDir(), "нет-такого-каталога", "key.pem")

	logs := captureLogs(t)

	if _, err := LoadOrGenerate(certPath, keyPath); err == nil {
		t.Fatal("ожидалась ошибка записи в несуществующий каталог")
	} else if !strings.Contains(err.Error(), certPath) {
		t.Errorf("в тексте %q нет пути %q", err, certPath)
	}

	if _, err := os.Stat(filepath.Dir(certPath)); !os.IsNotExist(err) {
		t.Errorf("каталог-приёмник не должен был создаться: %v", err)
	}

	assertNoKeyMaterial(t, logs.String(), keyPath)
}

// TestGeneratedKeyNotInLoggerOutput фиксирует, что выпуск материалов не печатает закрытый ключ:
// журналирование пакета попадает и в стандартный поток ошибок процесса, и в файл логов.
func TestGeneratedKeyNotInLoggerOutput(t *testing.T) {
	certPath := filepath.Join(t.TempDir(), "cert.pem")
	keyPath := filepath.Join(t.TempDir(), "key.pem")

	logs := captureLogs(t)

	if _, err := LoadOrGenerate(certPath, keyPath); err != nil {
		t.Fatalf("материалы не выпущены: %v", err)
	}

	text := logs.String()
	if text == "" {
		t.Fatal("журналирование выпуска пусто — проверка ничего не подтверждает")
	}

	assertNoKeyMaterial(t, text, keyPath)

	raw, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("ключ не читается: %v", err)
	}

	if !strings.Contains(string(raw), keyBlock) {
		t.Error("файл ключа не содержит PEM-блок закрытого ключа")
	}
}

// writeFile пишет фикстуру теста.
func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("фикстура %s не записана: %v", path, err)
	}
}

// assertNoKeyMaterial проверяет, что в диагностике нет ни PEM-заголовка, ни тела закрытого ключа.
func assertNoKeyMaterial(t *testing.T, logs, keyPath string) {
	t.Helper()

	if strings.Contains(logs, "PRIVATE KEY") {
		t.Errorf("в диагностике появился PEM-заголовок закрытого ключа: %s", logs)
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}

		t.Fatalf("ключ не читается: %v", err)
	}

	block, _ := pem.Decode(keyPEM)
	if block == nil {
		t.Fatal("ключ не является PEM")
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(keyPEM)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-----") {
			continue
		}

		if strings.Contains(logs, line) {
			t.Fatalf("в диагностике появилось тело закрытого ключа: %s", line)
		}
	}
}

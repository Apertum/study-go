package secure

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// localhost — имя, под которым сертификат годится всегда.
	localhost = "localhost"

	// serialBits — длина случайного серийного номера сертификата.
	serialBits = 128
	// validityPeriod — срок действия выпущенного сертификата.
	validityPeriod = 365 * 24 * time.Hour
	// clockSkew — отодвигает начало действия назад из-за неточных часов.
	clockSkew = time.Hour

	certBlock = "CERTIFICATE"
	keyBlock  = "EC PRIVATE KEY"

	certPerm os.FileMode = 0o644
	keyPerm  os.FileMode = 0o600
)

// Material — готовая пара «сертификат + закрытый ключ» и PEM сертификата для клиента.
type Material struct {
	Cert    tls.Certificate
	CertPEM []byte
}

// ServerTLS возвращает конфигурацию сервера: материал в Certificates и минимальную версию TLS 1.2.
func (m Material) ServerTLS() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{m.Cert},
		MinVersion:   tls.VersionTLS12,
	}
}

// LoadOrGenerate переиспользует пару из certPath/keyPath, если она читается, разбирается
// и её срок действия покрывает текущий момент. Иначе выпускает новую самоподписанную
// пару и записывает её в те же пути, предупреждая о причине перевыпуска.
func LoadOrGenerate(certPath, keyPath string) (Material, error) {
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)

	if certErr != nil || keyErr != nil {
		logrus.Warnf("secure: материал TLS недоступен (%s: %v; %s: %v) — выпускаю новую пару", certPath, certErr, keyPath, keyErr)
		return generate(certPath, keyPath)
	}

	material, err := newMaterial(certPEM, keyPEM)
	if err == nil {
		return material, nil
	}

	logrus.Warnf("secure: пара %s/%s не годится (%v) — выпускаю новую", certPath, keyPath, err)

	return generate(certPath, keyPath)
}

// newMaterial разбирает PEM-пару и проверяет, что срок действия сертификата покрывает текущий момент.
func newMaterial(certPEM, keyPEM []byte) (Material, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return Material{}, fmt.Errorf("разобрать пару сертификат/ключ: %w", err)
	}

	// Leaf заполнен самим X509KeyPair; повторный разбор нужен только под GODEBUG=x509keypairleaf=0.
	leaf := cert.Leaf
	if leaf == nil {
		if leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return Material{}, fmt.Errorf("разобрать сертификат: %w", err)
		}
	}

	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return Material{}, fmt.Errorf("срок действия %s—%s не покрывает текущий момент",
			leaf.NotBefore.Format(time.RFC3339), leaf.NotAfter.Format(time.RFC3339))
	}

	return Material{Cert: cert, CertPEM: certPEM}, nil
}

// generate выпускает самоподписанный сертификат на ECDSA P-256 и пишет пару атомарно.
func generate(certPath, keyPath string) (Material, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Material{}, fmt.Errorf("выпустить закрытый ключ P-256: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return Material{}, fmt.Errorf("выпустить серийный номер: %w", err)
	}

	names, addresses := subjects()
	notBefore := time.Now().Add(-clockSkew)
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: localhost},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(validityPeriod + clockSkew),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              names,
		IPAddresses:           addresses,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return Material{}, fmt.Errorf("выпустить сертификат: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return Material{}, fmt.Errorf("закодировать закрытый ключ: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: certBlock, Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: keyBlock, Bytes: keyDER})

	if err := writeFileAtomic(certPath, certPEM, certPerm); err != nil {
		return Material{}, err
	}

	if err := writeFileAtomic(keyPath, keyPEM, keyPerm); err != nil {
		return Material{}, err
	}

	logrus.Infof("secure: выпустил самоподписанный сертификат %s с ключом %s до %s",
		certPath, keyPath, template.NotAfter.Format(time.RFC3339))

	return newMaterial(certPEM, keyPEM)
}

// subjects возвращает имена и адреса, для которых годится сертификат.
func subjects() ([]string, []net.IP) {
	names := []string{localhost}

	host, err := os.Hostname()
	switch {
	case err != nil:
		logrus.Warnf("secure: имя машины недоступно (%v) — в сертификате только %s", err, localhost)
	case host != "" && host != localhost:
		names = append(names, host)
	}

	return names, []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
}

// writeFileAtomic пишет данные временным файлом рядом с целевым и переименовывает его,
// чтобы обрыв процесса не оставил недописанный ключ.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secure-*.tmp")
	if err != nil {
		return fmt.Errorf("создать временный файл для %s: %w", path, err)
	}

	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		os.Remove(tmpPath)

		return fmt.Errorf("записать %s: %w", path, err)
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)

		return fmt.Errorf("закрыть %s: %w", tmpPath, err)
	}

	if err := os.Chmod(tmpPath, perm); err != nil {
		os.Remove(tmpPath)

		return fmt.Errorf("выставить права %s: %w", path, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)

		return fmt.Errorf("переместить %s на место %s: %w", tmpPath, path, err)
	}

	return nil
}

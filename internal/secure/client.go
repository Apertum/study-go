package secure

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// ClientTLS собирает конфигурацию клиента, которая доверяет ровно одному сертификату
// из certPath: он кладётся в собственный пул RootCAs, системные корни не расширяются.
// Проверка цепочки и имени хоста остаётся штатной — InsecureSkipVerify не выставляется.
func ClientTLS(certPath string) (*tls.Config, error) {
	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("прочитать корневой сертификат %s: %w", certPath, err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("в %s нет PEM-сертификата X.509", certPath)
	}

	return &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}, nil
}

package security

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
)

// TLSConfig содержит конфигурацию TLS
type TLSConfig struct {
	Enabled  bool
	CertFile string
	KeyFile  string
	CAFile   string
	MinVersion uint16
	MaxVersion uint16
}

// LoadTLSConfig загружает TLS конфигурацию
func LoadTLSConfig(certFile, keyFile, caFile string) (*tls.Config, error) {
	// Загружаем сертификат сервера
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12, // PCI DSS compliance
		MaxVersion:   tls.VersionTLS13,
		CipherSuites: []uint16{
			// TLS 1.3 cipher suites
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			
			// TLS 1.2 cipher suites (для совместимости)
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		},
		PreferServerCipherSuites: true,
	}

	// Если указан CA файл, настраиваем mutual TLS (mTLS)
	if caFile != "" {
		caCert, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		tlsConfig.ClientCAs = caCertPool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert

		log.Info().Msg("mTLS enabled - client certificates required")
	}

	log.Info().
		Str("cert_file", certFile).
		Str("key_file", keyFile).
		Uint16("min_version", tlsConfig.MinVersion).
		Msg("TLS configuration loaded")

	return tlsConfig, nil
}

// LoadTLSConfigOrNil загружает TLS конфигурацию или возвращает nil если TLS отключен
func LoadTLSConfigOrNil(enabled bool, certFile, keyFile, caFile string) (*tls.Config, error) {
	if !enabled {
		log.Warn().Msg("TLS is DISABLED - not recommended for production!")
		return nil, nil
	}

	return LoadTLSConfig(certFile, keyFile, caFile)
}

// ValidateTLSFiles проверяет наличие TLS файлов
func ValidateTLSFiles(certFile, keyFile string) error {
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		return fmt.Errorf("certificate file not found: %s", certFile)
	}

	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		return fmt.Errorf("key file not found: %s", keyFile)
	}

	// Проверяем, что можем загрузить сертификат
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return fmt.Errorf("invalid certificate/key pair: %w", err)
	}

	return nil
}


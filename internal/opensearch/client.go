package opensearch

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"github.com/okto/opensearch-backup-manager/internal/config"
	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// Client обертка над OpenSearch клиентом
type Client struct {
	client *opensearchapi.Client
}

// NewClient создает новый OpenSearch API клиент
func NewClient(cfg config.OpenSearchConfig) (*Client, error) {
	osConfig := opensearch.Config{
		Addresses: cfg.Addresses,
		Username:  cfg.Username,
		Password:  cfg.Password,
	}

	// Настройка TLS если указан сертификат
	if cfg.CertPath != "" {
		caCert, err := os.ReadFile(cfg.CertPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read certificate: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse certificate")
		}

		osConfig.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: caCertPool,
			},
		}
	}

	// Создаем opensearchapi клиент
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: osConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenSearch API client: %w", err)
	}

	return &Client{client: client}, nil
}

// GetClient возвращает opensearchapi клиент
func (c *Client) GetClient() *opensearchapi.Client {
	return c.client
}

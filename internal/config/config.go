package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config main application configuration
type Config struct {
	OpenSearch  OpenSearchConfig `yaml:"opensearch"`
	S3          S3Config         `yaml:"s3"`
	CleanupJobs []CleanupJob     `yaml:"cleanup_jobs"`
	BackupJobs  []BackupJob      `yaml:"backup_jobs"`
}

// OpenSearch configuration
type OpenSearchConfig struct {
	Addresses []string `yaml:"addresses"`
	Username  string   `yaml:"username"`
	Password  string   `yaml:"password"`
	CertPath  string   `yaml:"cert_path"`
}

// S3Config configuration
type S3Config struct {
	Endpoint        string `yaml:"endpoint"`
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
	Bucket          string `yaml:"bucket"`
	Region          string `yaml:"region"`
	UseSSL          bool   `yaml:"use_ssl"`
}

// CleanupJob cleanup job
type CleanupJob struct {
	IndexName     string `yaml:"index_name"`
	RetentionDays int    `yaml:"retention_days"`
	Schedule      string `yaml:"schedule"` // cron format
}

// BackupJob backup job
type BackupJob struct {
	IndexName       string `yaml:"index_name"`
	Schedule        string `yaml:"schedule"`       // cron format
	IntervalHours   int    `yaml:"interval_hours"` // interval of splitting (2, 4, 6, 24)
	S3Path          string `yaml:"s3_path"`        // path in S3 bucket
	RequestInterval int    `yaml:"request_interval_seconds"`
}

func LoadConfig() (*Config, error) {
	configPath := getEnv("CONFIG_PATH", "/app/config/config.yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Override from environment variables
	if val := os.Getenv("OPENSEARCH_USERNAME"); val != "" {
		cfg.OpenSearch.Username = val
	}
	if val := os.Getenv("OPENSEARCH_PASSWORD"); val != "" {
		cfg.OpenSearch.Password = val
	}
	if val := os.Getenv("OPENSEARCH_ADDRESSES"); val != "" {
		cfg.OpenSearch.Addresses = []string{val}
	}
	if val := os.Getenv("OPENSEARCH_CERT_PATH"); val != "" {
		cfg.OpenSearch.CertPath = val
	}

	if val := os.Getenv("S3_ENDPOINT"); val != "" {
		cfg.S3.Endpoint = val
	}
	if val := os.Getenv("S3_ACCESS_KEY_ID"); val != "" {
		cfg.S3.AccessKeyID = val
	}
	if val := os.Getenv("S3_SECRET_ACCESS_KEY"); val != "" {
		cfg.S3.SecretAccessKey = val
	}
	if val := os.Getenv("S3_BUCKET"); val != "" {
		cfg.S3.Bucket = val
	}
	if val := os.Getenv("S3_REGION"); val != "" {
		cfg.S3.Region = val
	}

	return &cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

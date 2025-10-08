package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/okto/opensearch-backup-manager/internal/config"
	log "github.com/sirupsen/logrus"
)

// S3Client клиент для работы с S3/MinIO
type S3Client struct {
	client *minio.Client
	bucket string
}

// NewS3Client создает новый S3/MinIO клиент
func NewS3Client(cfg config.S3Config) (*S3Client, error) {
	// Определяем endpoint (по умолчанию s3.amazonaws.com)
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}

	log.WithFields(log.Fields{
		"endpoint": endpoint,
		"bucket":   cfg.Bucket,
		"region":   cfg.Region,
		"use_ssl":  cfg.UseSSL,
	}).Info("Initializing S3 client")

	// Создаем MinIO клиент
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	// Проверяем доступ к бакету
	ctx := context.Background()
	exists, err := minioClient.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		log.Warnf("Failed to check bucket existence: %v", err)
	} else if !exists {
		log.Warnf("Bucket %s does not exist or no access", cfg.Bucket)
	} else {
		log.Infof("Successfully connected to bucket: %s", cfg.Bucket)
	}

	return &S3Client{
		client: minioClient,
		bucket: cfg.Bucket,
	}, nil
}

// Upload загружает файл в S3/MinIO с retry механизмом
func (c *S3Client) Upload(ctx context.Context, filePath, key string, documentCount int) error {
	const maxRetries = 3
	const baseDelay = 2 * time.Second

	// Получаем информацию о файле
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	log.Infof("Uploading %s (%d documents) to s3://%s/%s", filePath, documentCount, c.bucket, key)

	// Определяем content type
	contentType := "application/gzip"
	if filepath.Ext(filePath) == ".json" {
		contentType = "application/json"
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Открываем файл для каждой попытки
		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}

		// Загружаем файл
		info, err := c.client.PutObject(
			ctx,
			c.bucket,
			key,
			file,
			fileInfo.Size(),
			minio.PutObjectOptions{
				ContentType: contentType,
			},
		)
		file.Close()

		if err == nil {
			log.Infof("Successfully uploaded %d documents to %s/%s (etag: %s)",
				documentCount, c.bucket, key, info.ETag)
			return nil
		}

		lastErr = err
		log.WithFields(log.Fields{
			"attempt":      attempt,
			"max_attempts": maxRetries,
			"error":        err.Error(),
		}).Errorf("Upload attempt %d failed", attempt)

		// Если это не последняя попытка, ждем перед повтором
		if attempt < maxRetries {
			delay := baseDelay * time.Duration(attempt)
			log.Infof("Retrying in %v...", delay)
			time.Sleep(delay)
		}
	}

	return fmt.Errorf("failed to upload file after %d attempts: %w", maxRetries, lastErr)
}

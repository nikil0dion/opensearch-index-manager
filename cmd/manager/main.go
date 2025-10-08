package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/okto/opensearch-backup-manager/internal/backup"
	"github.com/okto/opensearch-backup-manager/internal/cleanup"
	"github.com/okto/opensearch-backup-manager/internal/config"
	"github.com/okto/opensearch-backup-manager/internal/opensearch"
	"github.com/okto/opensearch-backup-manager/internal/storage"
	"github.com/robfig/cron/v3"
	log "github.com/sirupsen/logrus"
)

func logConfig(cfg *config.Config) {
	log.Info("=== Configuration ===")

	// OpenSearch configuration
	log.WithFields(log.Fields{
		"addresses": cfg.OpenSearch.Addresses,
		"username":  cfg.OpenSearch.Username,
		"password":  cfg.OpenSearch.Password,
		"cert_path": cfg.OpenSearch.CertPath,
	}).Info("OpenSearch configuration")

	// S3/MinIO configuration
	log.WithFields(log.Fields{
		"endpoint":          cfg.S3.Endpoint,
		"access_key_id":     cfg.S3.AccessKeyID,
		"secret_access_key": cfg.S3.SecretAccessKey,
		"bucket":            cfg.S3.Bucket,
		"region":            cfg.S3.Region,
		"use_ssl":           cfg.S3.UseSSL,
	}).Info("S3/MinIO configuration")

	// Cleanup jobs
	log.Infof("Cleanup jobs configured: %d", len(cfg.CleanupJobs))
	for i, job := range cfg.CleanupJobs {
		log.WithFields(log.Fields{
			"index":          job.IndexName,
			"retention_days": job.RetentionDays,
			"schedule":       job.Schedule,
		}).Infof("Cleanup job #%d", i+1)
	}

	// Backup jobs
	log.Infof("Backup jobs configured: %d", len(cfg.BackupJobs))
	for i, job := range cfg.BackupJobs {
		log.WithFields(log.Fields{
			"index":            job.IndexName,
			"schedule":         job.Schedule,
			"interval_hours":   job.IntervalHours,
			"s3_path":          job.S3Path,
			"request_interval": job.RequestInterval,
		}).Infof("Backup job #%d", i+1)
	}
}

func main() {
	log.SetFormatter(&log.JSONFormatter{
		DisableTimestamp: true,
	})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)

	log.Info("Starting OpenSearch Backup Manager")

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logConfig(cfg)

	// Initialize OpenSearch client
	osClient, err := opensearch.NewClient(cfg.OpenSearch)
	if err != nil {
		log.Fatalf("Failed to create OpenSearch client: %v", err)
	}

	// Initialize S3 client
	s3Client, err := storage.NewS3Client(cfg.S3)
	if err != nil {
		log.Fatalf("Failed to create S3 client: %v", err)
	}

	cleanupService := cleanup.NewService(osClient, cfg)
	backupService := backup.NewService(osClient, s3Client, cfg)

	// Setup cron scheduler
	c := cron.New()
	ctx, cancel := context.WithCancel(context.Background())

	// Mutex to prevent concurrent execution of jobs
	cleanupMutexes := make(map[string]*sync.Mutex)
	backupMutexes := make(map[string]*sync.Mutex)

	// Register cleanup jobs
	for _, job := range cfg.CleanupJobs {
		job := job
		cleanupMutexes[job.IndexName] = &sync.Mutex{}
		mutex := cleanupMutexes[job.IndexName]

		_, err := c.AddFunc(job.Schedule, func() {
			// Try to lock mutex
			if !mutex.TryLock() {
				log.Warnf("Cleanup job for %s is already running, skipping", job.IndexName)
				return
			}
			defer mutex.Unlock()

			log.Infof("Running cleanup job for index: %s", job.IndexName)
			if err := cleanupService.Cleanup(ctx, job); err != nil {
				log.Errorf("Cleanup failed for %s: %v", job.IndexName, err)
			}
		})
		if err != nil {
			log.Fatalf("Failed to add cleanup job for %s: %v", job.IndexName, err)
		}
		log.Infof("Registered cleanup job for %s (schedule: %s, retention: %d days)",
			job.IndexName, job.Schedule, job.RetentionDays)
	}

	for _, job := range cfg.BackupJobs {
		job := job
		backupMutexes[job.IndexName] = &sync.Mutex{}
		mutex := backupMutexes[job.IndexName]

		_, err := c.AddFunc(job.Schedule, func() {
			// Try to lock mutex
			if !mutex.TryLock() {
				log.Warnf("Backup job for %s is already running, skipping", job.IndexName)
				return
			}
			defer mutex.Unlock()

			log.Infof("Running backup job for index: %s", job.IndexName)
			if err := backupService.Backup(ctx, job); err != nil {
				log.Errorf("Backup failed for %s: %v", job.IndexName, err)
			}
		})
		if err != nil {
			log.Fatalf("Failed to add backup job for %s: %v", job.IndexName, err)
		}
		log.Infof("Registered backup job for %s (schedule: %s, interval: %d hours)",
			job.IndexName, job.Schedule, job.IntervalHours)
	}

	c.Start()
	log.Info("Scheduler started")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	log.Info("Shutting down...")

	cancel()
	c.Stop()

	log.Info("Shutdown complete")
}

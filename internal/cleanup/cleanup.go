package cleanup

import (
	"context"
	"fmt"
	"strings"

	"github.com/okto/opensearch-backup-manager/internal/config"
	"github.com/okto/opensearch-backup-manager/internal/opensearch"
	opensearchapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	log "github.com/sirupsen/logrus"
)

// Service for cleaning up old records
type Service struct {
	client *opensearch.Client
	config *config.Config
}

// NewService create new cleanup service
func NewService(client *opensearch.Client, cfg *config.Config) *Service {
	return &Service{
		client: client,
		config: cfg,
	}
}

// Cleanup delete old records from index
func (s *Service) Cleanup(ctx context.Context, job config.CleanupJob) error {
	log.Infof("Starting cleanup for index %s (retention: %d days)", job.IndexName, job.RetentionDays)

	// Form request for deletion
	deleteQuery := opensearchapi.DocumentDeleteByQueryReq{
		Indices: []string{job.IndexName},
		Body: strings.NewReader(fmt.Sprintf(`{
			"query": {
				"range": {
					"@timestamp": {
						"lte": "now-%dd/d"
					}
				}
			}
		}`, job.RetentionDays)),
	}

	// Execute request with retry
	var resp *opensearchapi.DocumentDeleteByQueryResp
	var err error

	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err = s.client.GetClient().Document.DeleteByQuery(ctx, deleteQuery)
		if err == nil {
			break
		}

		if attempt < maxRetries {
			log.Warnf("Cleanup attempt %d/%d failed for %s: %v (retrying...)", attempt, maxRetries, job.IndexName, err)
		}
	}

	if err != nil {
		return fmt.Errorf("delete by query failed after %d attempts: %w", maxRetries, err)
	}

	// Check for failures in response
	if len(resp.Failures) > 0 {
		log.Errorf("Cleanup for %s completed with %d failures (deleted: %d)", job.IndexName, len(resp.Failures), resp.Deleted)
		for i, failure := range resp.Failures {
			if i < 3 { // Log first 3 failures
				log.Errorf("  Failure %d: %+v", i+1, failure)
			}
		}
	} else {
		log.Infof("Cleanup completed for %s: deleted %d documents", job.IndexName, resp.Deleted)
	}

	return nil
}

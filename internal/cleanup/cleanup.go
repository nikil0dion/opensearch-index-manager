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

	// Execute request
	resp, err := s.client.GetClient().Document.DeleteByQuery(ctx, deleteQuery)
	if err != nil {
		return fmt.Errorf("delete by query failed: %w", err)
	}

	log.Infof("Cleanup completed for %s: deleted %d documents", job.IndexName, resp.Deleted)

	return nil
}

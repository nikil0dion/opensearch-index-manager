package cleanup

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/okto/opensearch-backup-manager/internal/config"
	"github.com/okto/opensearch-backup-manager/internal/opensearch"
	opensearchapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	log "github.com/sirupsen/logrus"
)

const (
	maxRetries = 3
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

// Cleanup delete old records from index by intervals
func (s *Service) Cleanup(ctx context.Context, job config.CleanupJob) error {
	log.Infof("Starting cleanup for index %s (retention: %d days)", job.IndexName, job.RetentionDays)

	// Find oldest document to delete
	oldestTime, err := s.findOldestDocument(ctx, job)
	if err != nil {
		return fmt.Errorf("failed to find oldest document: %w", err)
	}

	if oldestTime == nil {
		log.Infof("No documents to delete for %s", job.IndexName)
		return nil
	}

	// Calculate cutoff date
	cutoffDate := time.Now().UTC().AddDate(0, 0, -job.RetentionDays).Truncate(24 * time.Hour)

	// Use defaults if not set
	intervalHours := job.IntervalHours
	if intervalHours == 0 {
		intervalHours = 2 // Default 2 hours
	}
	requestInterval := job.RequestInterval
	if requestInterval == 0 {
		requestInterval = 15 // Default 15 seconds
	}

	log.Infof("Oldest document: %s, Cutoff date: %s, Interval: %dh, Pause: %ds",
		oldestTime.Format(time.RFC3339),
		cutoffDate.Format(time.RFC3339),
		intervalHours,
		requestInterval)

	// Delete by intervals
	totalDeleted := 0
	failedIntervals := 0
	intervalNum := 1

	currentTime := *oldestTime
	for currentTime.Before(cutoffDate) {
		endTime := currentTime.Add(time.Duration(intervalHours) * time.Hour)
		if endTime.After(cutoffDate) {
			endTime = cutoffDate
		}

		deleted, err := s.deleteInterval(ctx, job.IndexName, currentTime, endTime, intervalNum)
		if err != nil {
			log.Errorf("Failed to delete interval %d (%s - %s): %v",
				intervalNum, currentTime.Format(time.RFC3339), endTime.Format(time.RFC3339), err)
			failedIntervals++
		} else {
			totalDeleted += deleted
		}

		intervalNum++
		currentTime = endTime

		// Pause between requests (except last one)
		if currentTime.Before(cutoffDate) {
			time.Sleep(time.Duration(requestInterval) * time.Second)
		}
	}

	log.Infof("Cleanup completed for %s: deleted %d documents in %d intervals (%d failed)",
		job.IndexName, totalDeleted, intervalNum-1, failedIntervals)

	return nil
}

// findOldestDocument find oldest document to delete
func (s *Service) findOldestDocument(ctx context.Context, job config.CleanupJob) (*time.Time, error) {
	searchReq := opensearchapi.SearchReq{
		Indices: []string{job.IndexName},
		Body: strings.NewReader(fmt.Sprintf(`{
			"query": {
				"range": {
					"@timestamp": {
						"lte": "now-%dd/d"
					}
				}
			},
			"size": 1,
			"sort": [{"@timestamp": "asc"}]
		}`, job.RetentionDays)),
	}

	resp, err := s.client.GetClient().Search(ctx, &searchReq)
	if err != nil {
		return nil, err
	}

	if len(resp.Hits.Hits) == 0 {
		return nil, nil
	}

	// Extract @timestamp from first document
	hit := resp.Hits.Hits[0]

	// Parse JSON from RawMessage
	var source map[string]interface{}
	if err := json.Unmarshal(hit.Source, &source); err != nil {
		return nil, fmt.Errorf("failed to unmarshal source: %w", err)
	}

	if timestampStr, ok := source["@timestamp"].(string); ok {
		timestamp, err := time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse timestamp: %w", err)
		}
		return &timestamp, nil
	}

	return nil, fmt.Errorf("failed to extract @timestamp from document")
}

// deleteInterval delete documents in time interval
func (s *Service) deleteInterval(ctx context.Context, indexName string, startTime, endTime time.Time, intervalNum int) (int, error) {
	// First get count
	count, err := s.getCount(ctx, indexName, startTime, endTime)
	if err != nil {
		return 0, fmt.Errorf("failed to get count: %w", err)
	}

	if count == 0 {
		log.Infof("[%d] No documents in interval %s - %s",
			intervalNum, startTime.Format("2006-01-02 15:04"), endTime.Format("2006-01-02 15:04"))
		return 0, nil
	}

	log.Infof("[%d] Deleting %d documents in interval %s - %s",
		intervalNum, count, startTime.Format("2006-01-02 15:04"), endTime.Format("2006-01-02 15:04"))

	// Delete with retry
	requestsPerSecond := 500
	deleteQuery := opensearchapi.DocumentDeleteByQueryReq{
		Indices: []string{indexName},
		Body: strings.NewReader(fmt.Sprintf(`{
			"query": {
				"range": {
					"@timestamp": {
						"gte": "%s",
						"lt": "%s"
					}
				}
			}
		}`, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))),
		Params: opensearchapi.DocumentDeleteByQueryParams{
			Timeout:           30 * time.Minute,
			RequestsPerSecond: &requestsPerSecond,
			Conflicts:         "proceed",
		},
	}

	var resp *opensearchapi.DocumentDeleteByQueryResp
	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err = s.client.GetClient().Document.DeleteByQuery(ctx, deleteQuery)
		if err == nil && len(resp.Failures) == 0 {
			break
		}

		if attempt < maxRetries {
			log.Warnf("  Retry %d/%d for interval %d", attempt, maxRetries, intervalNum)
			time.Sleep(5 * time.Second)
		}
	}

	if err != nil {
		return 0, fmt.Errorf("delete failed after %d attempts: %w", maxRetries, err)
	}

	if len(resp.Failures) > 0 {
		log.Warnf("  Deleted %d/%d documents (failures: %d)", resp.Deleted, count, len(resp.Failures))
		for i, failure := range resp.Failures {
			if i < 2 {
				log.Errorf("    Failure: %+v", failure)
			}
		}
	} else {
		log.Infof("  ✓ Deleted %d documents", resp.Deleted)
	}

	return int(resp.Deleted), nil
}

// getCount get count of documents for interval
func (s *Service) getCount(ctx context.Context, indexName string, startTime, endTime time.Time) (int, error) {
	countReq := opensearchapi.IndicesCountReq{
		Indices: []string{indexName},
		Body: strings.NewReader(fmt.Sprintf(`{
			"query": {
				"range": {
					"@timestamp": {
						"gte": "%s",
						"lt": "%s"
					}
				}
			}
		}`, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))),
	}

	resp, err := s.client.GetClient().Indices.Count(ctx, &countReq)
	if err != nil {
		return 0, err
	}

	return resp.Count, nil
}

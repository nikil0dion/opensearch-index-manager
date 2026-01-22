package backup

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/okto/opensearch-backup-manager/internal/config"
	"github.com/okto/opensearch-backup-manager/internal/opensearch"
	"github.com/okto/opensearch-backup-manager/internal/storage"
	opensearchapi "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	log "github.com/sirupsen/logrus"
)

type Service struct {
	client   *opensearch.Client
	s3Client *storage.S3Client
	config   *config.Config
	workDir  string
}

func NewService(client *opensearch.Client, s3Client *storage.S3Client, cfg *config.Config) *Service {
	workDir := "/tmp/opensearch-backups"
	os.MkdirAll(workDir, 0755)

	return &Service{
		client:   client,
		s3Client: s3Client,
		config:   cfg,
		workDir:  workDir,
	}
}

func (s *Service) Backup(ctx context.Context, job config.BackupJob) error {
	// By default backup for yesterday
	targetDate := time.Now().AddDate(0, 0, -1)

	log.Infof("Starting backup for index %s, date: %s", job.IndexName, targetDate.Format("2006-01-02"))

	var allFiles []string
	periodsCount := 24 / job.IntervalHours

	// Download data by intervals
	for i := 0; i < periodsCount; i++ {
		startHour := i * job.IntervalHours
		endHour := startHour + job.IntervalHours

		filename, err := s.downloadPeriod(ctx, job, targetDate, startHour, endHour, i+1)
		if err != nil {
			log.Errorf("Failed to download period %d: %v", i+1, err)
			continue
		}

		if filename != "" {
			allFiles = append(allFiles, filename)
		}

		// Pause between requests
		if i < periodsCount-1 && job.RequestInterval > 0 {
			log.Infof("Waiting %d seconds before next request...", job.RequestInterval)
			time.Sleep(time.Duration(job.RequestInterval) * time.Second)
		}
	}

	if len(allFiles) == 0 {
		log.Warnf("No data downloaded for %s", job.IndexName)
		return nil
	}

	// Count total documents from all files
	totalCount, err := s.countDocumentsInFiles(allFiles)
	if err != nil {
		log.Warnf("Failed to count documents: %v", err)
		totalCount = 0
	}

	// Compress all files together into one gzip archive
	compressedFile, err := s.compressMultipleFiles(allFiles, job.IndexName, targetDate)
	if err != nil {
		return fmt.Errorf("failed to compress files: %w", err)
	}

	// Upload to S3
	s3Key := filepath.Join(job.S3Path, filepath.Base(compressedFile))
	if err := s.s3Client.Upload(ctx, compressedFile, s3Key, totalCount); err != nil {
		return fmt.Errorf("failed to upload to S3: %w", err)
	}

	// Cleanup temporary files
	s.cleanupSimple(allFiles, compressedFile)

	log.Infof("Backup completed for %s: %s", job.IndexName, s3Key)
	return nil
}

// downloadPeriod download data for period
func (s *Service) downloadPeriod(ctx context.Context, job config.BackupJob, date time.Time, startHour, endHour, fileNum int) (string, error) {
	startTime := time.Date(date.Year(), date.Month(), date.Day(), startHour, 0, 0, 0, time.UTC)

	var endTime time.Time
	if endHour >= 24 {
		endTime = time.Date(date.Year(), date.Month(), date.Day(), 23, 59, 59, 999000000, time.UTC)
	} else {
		endTime = time.Date(date.Year(), date.Month(), date.Day(), endHour, 0, 0, 0, time.UTC).Add(-time.Millisecond)
	}

	log.Infof("Downloading period %d: %s - %s", fileNum, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))

	// Get count of documents
	count, err := s.getCount(ctx, job.IndexName, startTime, endTime)
	if err != nil {
		return "", fmt.Errorf("failed to get count: %w", err)
	}

	if count == 0 {
		log.Infof("No documents found for period %d", fileNum)
		return "", nil
	}

	log.Infof("Found %d documents for period %d", count, fileNum)

	// Download documents
	filename := filepath.Join(s.workDir, fmt.Sprintf("%s-%s-%d.json",
		date.Format("01-02-06"), job.IndexName, fileNum))

	if err := s.searchAndSave(ctx, job.IndexName, startTime, endTime, count, filename); err != nil {
		return "", fmt.Errorf("failed to search and save: %w", err)
	}

	return filename, nil
}

// getCount get count of documents for period
func (s *Service) getCount(ctx context.Context, indexName string, startTime, endTime time.Time) (int, error) {
	countReq := opensearchapi.IndicesCountReq{
		Indices: []string{indexName},
		Body: strings.NewReader(fmt.Sprintf(`{
			"query": {
				"range": {
					"@timestamp": {
						"gte": "%s",
						"lte": "%s"
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

// searchAndSave search and save results
func (s *Service) searchAndSave(ctx context.Context, indexName string, startTime, endTime time.Time, size int, filename string) error {
	searchReq := opensearchapi.SearchReq{
		Indices: []string{indexName},
		Body: strings.NewReader(fmt.Sprintf(`{
			"query": {
				"range": {
					"@timestamp": {
						"gte": "%s",
						"lte": "%s"
					}
				}
			},
			"sort": [
				{"@timestamp": {"order": "asc"}}
			],
			"size": %d
		}`, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339), size)),
	}

	resp, err := s.client.GetClient().Search(ctx, &searchReq)
	if err != nil {
		return err
	}

	// Save results to file
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Serialize response to JSON and save to file
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(resp); err != nil {
		return fmt.Errorf("failed to encode response: %w", err)
	}

	return nil
}

// mergeFiles merge files into one and count total documents
func (s *Service) mergeFiles(files []string, indexName string, date time.Time) (string, int, error) {
	mergedFilename := filepath.Join(s.workDir, fmt.Sprintf("%s-%s.json",
		date.Format("01-02-06"), indexName))

	merged, err := os.Create(mergedFilename)
	if err != nil {
		return "", 0, err
	}
	defer merged.Close()

	totalCount := 0

	for _, filename := range files {
		file, err := os.Open(filename)
		if err != nil {
			return "", 0, err
		}

		// Read and parse JSON to count documents
		var searchResponse struct {
			Hits struct {
				Total struct {
					Value int `json:"value"`
				} `json:"total"`
				Hits []interface{} `json:"hits"`
			} `json:"hits"`
		}

		decoder := json.NewDecoder(file)
		if err := decoder.Decode(&searchResponse); err != nil {
			file.Close()
			return "", 0, fmt.Errorf("failed to decode JSON from %s: %w", filename, err)
		}

		// Add to total count
		totalCount += len(searchResponse.Hits.Hits)

		// Reset file position to beginning
		file.Seek(0, 0)

		// Copy file content to merged file
		_, err = io.Copy(merged, file)
		file.Close()
		if err != nil {
			return "", 0, err
		}
	}

	log.Infof("Merged %d files into %s (total documents: %d)", len(files), mergedFilename, totalCount)
	return mergedFilename, totalCount, nil
}

// compressFile compress file with gzip
func (s *Service) compressFile(filename string) (string, error) {
	compressedFilename := filename + ".gz"

	source, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer source.Close()

	dest, err := os.Create(compressedFilename)
	if err != nil {
		return "", err
	}
	defer dest.Close()

	gzipWriter := gzip.NewWriter(dest)
	gzipWriter.Name = filepath.Base(filename)

	_, err = io.Copy(gzipWriter, source)
	if err != nil {
		return "", err
	}

	if err := gzipWriter.Close(); err != nil {
		return "", err
	}

	log.Infof("Compressed %s to %s", filename, compressedFilename)
	return compressedFilename, nil
}

// countDocumentsInFiles count total documents from all files
func (s *Service) countDocumentsInFiles(files []string) (int, error) {
	totalCount := 0

	for _, filename := range files {
		file, err := os.Open(filename)
		if err != nil {
			log.Warnf("Failed to open file %s: %v", filename, err)
			continue
		}

		// Read and parse JSON to count documents
		var searchResponse struct {
			Hits struct {
				Total struct {
					Value int `json:"value"`
				} `json:"total"`
				Hits []interface{} `json:"hits"`
			} `json:"hits"`
		}

		decoder := json.NewDecoder(file)
		if err := decoder.Decode(&searchResponse); err != nil {
			file.Close()
			log.Warnf("Failed to decode JSON from %s: %v", filename, err)
			continue
		}

		// Add to total count
		totalCount += len(searchResponse.Hits.Hits)
		file.Close()
	}

	return totalCount, nil
}

// compressMultipleFiles compress all files together into one gzip archive with maximum compression
func (s *Service) compressMultipleFiles(files []string, indexName string, date time.Time) (string, error) {
	// Create compressed filename
	compressedFilename := filepath.Join(s.workDir, fmt.Sprintf("%s-%s.json.gz",
		date.Format("01-02-06"), indexName))

	// Create compressed file
	dest, err := os.Create(compressedFilename)
	if err != nil {
		return "", err
	}
	defer dest.Close()

	// Create gzip writer with maximum compression level (gzip -9)
	gzipWriter, err := gzip.NewWriterLevel(dest, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	defer gzipWriter.Close()

	gzipWriter.Name = fmt.Sprintf("%s-%s.json", date.Format("01-02-06"), indexName)

	// Write all files into the gzip stream
	for _, filename := range files {
		file, err := os.Open(filename)
		if err != nil {
			return "", fmt.Errorf("failed to open file %s: %w", filename, err)
		}

		// Copy file content directly to gzip writer
		_, err = io.Copy(gzipWriter, file)
		file.Close()
		if err != nil {
			return "", fmt.Errorf("failed to compress file %s: %w", filename, err)
		}
	}

	log.Infof("Compressed %d files into %s with maximum compression (gzip -9)", len(files), compressedFilename)
	return compressedFilename, nil
}

// cleanupSimple delete temporary files
func (s *Service) cleanupSimple(tempFiles []string, compressedFile string) {
	for _, file := range tempFiles {
		os.Remove(file)
	}
	os.Remove(compressedFile)
	log.Infof("Cleaned up temporary files")
}

// cleanup delete temporary files (legacy function for compatibility)
func (s *Service) cleanup(tempFiles []string, mergedFile, compressedFile string) {
	for _, file := range tempFiles {
		os.Remove(file)
	}
	os.Remove(mergedFile)
	log.Infof("Cleaned up temporary files")
}

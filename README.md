# OpenSearch Index Manager

Automated backup and cleanup tool for OpenSearch/Elasticsearch indexes with S3 upload support.

## Features

- 🗑️ **Automatic cleanup** of old records from indexes (with configurable retention)
- 💾 **Log backups** with time interval splitting
- 📦 **Data compression** using gzip
- ☁️ **Upload to S3-compatible storage** (AWS S3, MinIO, Cloudflare R2, Wasabi, etc.)
- ⏰ **Task scheduler** based on cron
- 🐳 **Docker support**
- 📊 **JSON logging**

## Quick Start


### Configure

Edit `config/config.yaml`:

```yaml

cleanup_jobs:
  - index_name: "your-index"
    retention_days: 30
    schedule: "0 2 * * *"  # Every day at 2:00 AM

backup_jobs:
  - index_name: "your-index"
    schedule: "0 6 * * *"  # Every day at 6:00 AM
    interval_hours: 2
    s3_path: "your-index/"
    request_interval_seconds: 30
```

### Add OpenSearch Certificate

Place your OpenSearch cluster CA certificate:
```bash
mkdir -p certs
cp /path/to/your/root.crt certs/
```

### Configure Docker Compose

```yaml
    environment:
      # OpenSearch setting
      OPENSEARCH_ADDRESSES: "https://your-opensearch-host:9200"
      OPENSEARCH_USERNAME: "your-username"
      OPENSEARCH_PASSWORD: "your-password"
      OPENSEARCH_CERT_PATH: "/certs/root.crt"
      
      # S3/MinIO settings (MinIO Go Client supports all S3-compatible storages)
      # For AWS S3: leave empty or set to s3.amazonaws.com
      # For MinIO: set to minio:9000 (without https://)
      S3_ENDPOINT: "s3.amazonaws.com"
      S3_ACCESS_KEY_ID: "your-access-key"
      S3_SECRET_ACCESS_KEY: "your-secret-key"
      S3_BUCKET: "backups"
      S3_REGION: "us-east-1"
```

## Configuration

### Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `OPENSEARCH_ADDRESSES` | OpenSearch address | `https://localhost:9200` |
| `OPENSEARCH_USERNAME` | Username | `admin` |
| `OPENSEARCH_PASSWORD` | Password | `MySecretPassword123` |
| `OPENSEARCH_CERT_PATH` | Path to CA certificate | `/certs/root.crt` |
| `S3_ENDPOINT` | S3/MinIO endpoint | `s3.amazonaws.com` |
| `S3_ACCESS_KEY_ID` | Access Key ID | `AKIAIOSFODNN7EXAMPLE` |
| `S3_SECRET_ACCESS_KEY` | Secret Access Key | `wJalrXUtnFEMI/K7MDENG/...` |
| `S3_BUCKET` | Bucket name | `backups` |
| `S3_REGION` | S3 region | `us-east-1` |
| `CONFIG_PATH` | Path to config.yaml | `/app/config/config.yaml` |
| `TZ` | Timezone | `Etc/UTC` |


## Project Structure

```
opensearch-backup-manager/
├── cmd/
│   └── manager/          # Application entry point
├── internal/
│   ├── config/          # Configuration
│   ├── opensearch/      # OpenSearch client
│   ├── backup/          # Backup logic
│   ├── cleanup/         # Cleanup logic
│   └── storage/         # S3 client
├── config/
│   └── config.yaml      # Configuration file
├── certs/               # SSL certificates
├── tmp/                 # Temporary files
├── Dockerfile
├── docker-compose.yml
├── Makefile
└── README.md
```

## How It Works

### Cleanup Process

1. Runs on schedule (cron)
2. Executes `DELETE_BY_QUERY` in OpenSearch
3. Deletes documents older than N days (retention_days)
4. Logs number of deleted documents

### Backup Process

1. Runs on schedule (cron)
2. Downloads data for previous day
3. Splits day into intervals (e.g., every 2 hours)
4. For each interval:
   - Gets document count
   - Downloads documents
   - Saves to JSON file
5. Merges all files into one
6. Compresses with gzip (-9)
7. Uploads to S3 with retry mechanism (3 attempts)
8. Cleans up temporary files



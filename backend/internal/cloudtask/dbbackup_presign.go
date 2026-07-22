package cloudtask

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// DBBackupPresigner mints short-lived presigned GET URLs for logical database
// dump objects in the Kanister dump bucket, so the browser downloads a backup
// straight from object storage without the dump bytes transiting the API. When
// the dump-bucket S3 access is not configured the presigner is disabled
// (Enabled() == false) and the download handler degrades to a 503.
//
// PresignGet returns a URL that downloads objectKey as an attachment named
// downloadFilename, valid for ttl.
type DBBackupPresigner interface {
	Enabled() bool
	PresignGet(ctx context.Context, objectKey, downloadFilename string, ttl time.Duration) (string, error)
}

// minioDBBackupPresigner presigns against the dump bucket with static keys.
type minioDBBackupPresigner struct {
	client *minio.Client
	bucket string
}

// NewDBBackupPresigner builds a presigner for the dump bucket. Empty endpoint,
// bucket or credentials yields a disabled presigner whose PresignGet fails with
// a clear "not configured" error rather than crashing at startup.
func NewDBBackupPresigner(endpoint, bucket, region, accessKey, secretKey string, insecure bool) DBBackupPresigner {
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return disabledDBBackupPresigner{}
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: !insecure,
		Region: region,
	})
	if err != nil {
		return disabledDBBackupPresigner{err: err}
	}
	return &minioDBBackupPresigner{client: client, bucket: bucket}
}

func (p *minioDBBackupPresigner) Enabled() bool { return true }

func (p *minioDBBackupPresigner) PresignGet(ctx context.Context, objectKey, downloadFilename string, ttl time.Duration) (string, error) {
	reqParams := make(url.Values)
	if downloadFilename != "" {
		reqParams.Set("response-content-disposition", fmt.Sprintf("attachment; filename=%q", downloadFilename))
	}
	u, err := p.client.PresignedGetObject(ctx, p.bucket, objectKey, ttl, reqParams)
	if err != nil {
		return "", fmt.Errorf("presign dump object: %w", err)
	}
	return u.String(), nil
}

// disabledDBBackupPresigner is returned when the dump-bucket S3 access is not
// configured; every PresignGet fails identically.
type disabledDBBackupPresigner struct{ err error }

func (d disabledDBBackupPresigner) Enabled() bool { return false }

func (d disabledDBBackupPresigner) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	if d.err != nil {
		return "", fmt.Errorf("backup download not configured: %w", d.err)
	}
	return "", fmt.Errorf("backup download not configured")
}

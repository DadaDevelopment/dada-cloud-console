package cloudtask

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// dbBackupPutPartSize is the multipart chunk size used when streaming an
// object of unknown length (size=-1) into the dump bucket, e.g. a live
// tar.gz being piped straight from a pod-exec stream.
const dbBackupPutPartSize = 16 * 1024 * 1024

// DBBackupPresigner mints short-lived presigned GET URLs for objects in the
// Kanister dump bucket, so the browser downloads a backup straight from
// object storage without the bytes transiting the API. When the dump-bucket
// S3 access is not configured the presigner is disabled (Enabled() == false)
// and the download handler degrades to a 503.
//
// PresignGet returns a URL that downloads objectKey as an attachment named
// downloadFilename, valid for ttl. PutObject streams r (size may be -1 for an
// unknown-length stream) into objectKey under the same bucket/credentials;
// used for artifacts the backend itself writes (e.g. volume-export
// tarballs), not the Kanister dump/ hierarchy.
type DBBackupPresigner interface {
	Enabled() bool
	PresignGet(ctx context.Context, objectKey, downloadFilename string, ttl time.Duration) (string, error)
	PutObject(ctx context.Context, objectKey string, r io.Reader, size int64, contentType string) error
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

func (p *minioDBBackupPresigner) PutObject(ctx context.Context, objectKey string, r io.Reader, size int64, contentType string) error {
	_, err := p.client.PutObject(ctx, p.bucket, objectKey, r, size, minio.PutObjectOptions{
		PartSize:    dbBackupPutPartSize,
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("put dump-bucket object: %w", err)
	}
	return nil
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

func (d disabledDBBackupPresigner) PutObject(context.Context, string, io.Reader, int64, string) error {
	if d.err != nil {
		return fmt.Errorf("backup download not configured: %w", d.err)
	}
	return fmt.Errorf("backup download not configured")
}

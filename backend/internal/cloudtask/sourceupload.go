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

// SourceUploader stores an uploaded source archive (upload-deploy) in object
// storage. The stored key is what ends up in git_repos.clone_url as
// "s3://<bucket>/<key>" for provider='archive' rows — build-agent's
// gitCreds presigns a GET against the same key (build-agent's
// archivesource.go, sharing this env var family).
//
// Enabled() == false when SOURCE_UPLOAD_S3_* is unset; PutObject then fails
// with a clear "not configured" error instead of the handler crashing.
//
// PresignGet mints a short-lived download URL for the same key, so a user who
// lost their local checkout can pull back the exact archive they uploaded.
//
// GetObject reads the archive back into the API process, bounded by maxBytes,
// so a rebuild can re-run framework detection against the source the user
// already uploaded instead of asking them to upload it again.
type SourceUploader interface {
	Enabled() bool
	Bucket() string
	PutObject(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	PresignGet(ctx context.Context, key, filename string, ttl time.Duration) (string, error)
	GetObject(ctx context.Context, key string, maxBytes int64) ([]byte, error)
}

type minioSourceUploader struct {
	client *minio.Client
	bucket string
}

// NewSourceUploader builds an uploader against the source-upload bucket.
// Empty endpoint, bucket or credentials yields a disabled uploader.
func NewSourceUploader(endpoint, bucket, region, accessKey, secretKey string, insecure bool) SourceUploader {
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return disabledSourceUploader{}
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: !insecure,
		Region: region,
	})
	if err != nil {
		return disabledSourceUploader{err: err}
	}
	return &minioSourceUploader{client: client, bucket: bucket}
}

func (u *minioSourceUploader) Enabled() bool { return true }

func (u *minioSourceUploader) Bucket() string { return u.bucket }

func (u *minioSourceUploader) PutObject(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := u.client.PutObject(ctx, u.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put source archive: %w", err)
	}
	return nil
}

func (u *minioSourceUploader) PresignGet(ctx context.Context, key, filename string, ttl time.Duration) (string, error) {
	reqParams := make(url.Values)
	if filename != "" {
		reqParams.Set("response-content-disposition", fmt.Sprintf("attachment; filename=%q", filename))
	}
	presigned, err := u.client.PresignedGetObject(ctx, u.bucket, key, ttl, reqParams)
	if err != nil {
		return "", fmt.Errorf("presign source archive: %w", err)
	}
	return presigned.String(), nil
}

func (u *minioSourceUploader) GetObject(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	obj, err := u.client.GetObject(ctx, u.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get source archive: %w", err)
	}
	defer obj.Close()
	data, err := io.ReadAll(io.LimitReader(obj, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("read source archive: %w", err)
	}
	return data, nil
}

type disabledSourceUploader struct{ err error }

func (d disabledSourceUploader) Enabled() bool { return false }

func (d disabledSourceUploader) Bucket() string { return "" }

func (d disabledSourceUploader) PutObject(context.Context, string, io.Reader, int64, string) error {
	if d.err != nil {
		return fmt.Errorf("source upload not configured: %w", d.err)
	}
	return fmt.Errorf("source upload not configured")
}

func (d disabledSourceUploader) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	if d.err != nil {
		return "", fmt.Errorf("source upload not configured: %w", d.err)
	}
	return "", fmt.Errorf("source upload not configured")
}

func (d disabledSourceUploader) GetObject(context.Context, string, int64) ([]byte, error) {
	if d.err != nil {
		return nil, fmt.Errorf("source upload not configured: %w", d.err)
	}
	return nil, fmt.Errorf("source upload not configured")
}

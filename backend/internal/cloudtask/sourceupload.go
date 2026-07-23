package cloudtask

import (
	"context"
	"fmt"
	"io"

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
type SourceUploader interface {
	Enabled() bool
	Bucket() string
	PutObject(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
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

type disabledSourceUploader struct{ err error }

func (d disabledSourceUploader) Enabled() bool { return false }

func (d disabledSourceUploader) Bucket() string { return "" }

func (d disabledSourceUploader) PutObject(context.Context, string, io.Reader, int64, string) error {
	if d.err != nil {
		return fmt.Errorf("source upload not configured: %w", d.err)
	}
	return fmt.Errorf("source upload not configured")
}

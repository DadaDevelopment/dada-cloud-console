package worker

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const archivePresignTTL = time.Hour

type ArchivePresigner interface {
	Enabled() bool
	PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}

func NewArchivePresigner(endpoint, region, accessKey, secretKey string, insecure bool) ArchivePresigner {
	if endpoint == "" || accessKey == "" || secretKey == "" {
		return disabledArchivePresigner{}
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: !insecure,
		Region: region,
	})
	if err != nil {
		return disabledArchivePresigner{err: err}
	}
	return &minioArchivePresigner{client: client}
}

type minioArchivePresigner struct {
	client *minio.Client
}

func (p *minioArchivePresigner) Enabled() bool { return true }

func (p *minioArchivePresigner) PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	u, err := p.client.PresignedGetObject(ctx, bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("presign archive object: %w", err)
	}
	return u.String(), nil
}

type disabledArchivePresigner struct{ err error }

func (d disabledArchivePresigner) Enabled() bool { return false }

func (d disabledArchivePresigner) PresignGet(context.Context, string, string, time.Duration) (string, error) {
	if d.err != nil {
		return "", fmt.Errorf("archive presign not configured: %w", d.err)
	}
	return "", fmt.Errorf("archive presign not configured")
}

func parseS3URL(u string) (bucket, key string, err error) {
	const prefix = "s3://"
	if !strings.HasPrefix(u, prefix) {
		return "", "", fmt.Errorf("not an s3 url: %q", u)
	}
	rest := strings.TrimPrefix(u, prefix)
	bucket, key, ok := strings.Cut(rest, "/")
	if !ok || bucket == "" || key == "" {
		return "", "", fmt.Errorf("malformed s3 url: %q", u)
	}
	return bucket, key, nil
}

func archiveUploadID(key string) string {
	base := path.Base(key)
	for _, ext := range []string{".tar.gz", ".tgz", ".zip"} {
		if strings.HasSuffix(base, ext) {
			return strings.TrimSuffix(base, ext)
		}
	}
	return strings.TrimSuffix(base, path.Ext(base))
}

func archiveUploadBranch(key string) string {
	id := archiveUploadID(key)
	if len(id) > 8 {
		id = id[:8]
	}
	return "upload-" + id
}

package box

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// WorkspaceStore is where a sleeping box's workspace lives while it has no body.
//
// It exists so a box does not need a network block device to survive a sleep.
// The Longhorn claim was the honest implementation of "the work is still there
// when you come back", and it cost the customer 11 of the 16.7 seconds of every
// cold start (provision plus attach) plus a parked disk billed for every minute
// the box was asleep - on 2026-08-04 that was 96% of all metered box minutes and
// 15.6% of the platform bill with no external demand. A tar in object storage
// answers the same question for cents and restores at network speed into a
// workspace the kubelet hands over instantly.
//
// Enabled() == false when BOX_WORKSPACE_S3_* (and its SOURCE_UPLOAD_S3_*
// fallback) are unset. That is not a degraded mode: it is the switch that keeps
// the PVC path in force, because an ephemeral workspace with nowhere to archive
// to would turn every sleep into data loss.
type WorkspaceStore interface {
	Enabled() bool
	Put(ctx context.Context, key string, r io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Remove(ctx context.Context, key string) error
}

// ErrNoWorkspaceArchive is what Get reports when the key has never been written.
//
// Callers must NOT read it as "empty workspace". A box whose body is gone and
// whose archive is missing has lost the customer's work, and the only honest
// thing a resume can do is fail loudly rather than hand back a clean /workspace
// and let them discover it.
var ErrNoWorkspaceArchive = errors.New("no workspace archive for this box")

type minioWorkspaceStore struct {
	client *minio.Client
	bucket string
	prefix string
}

// NewWorkspaceStore builds a store against an S3-compatible bucket. Missing
// endpoint, bucket or credentials yields a disabled store, which keeps the
// PVC-backed workspace in force rather than failing box creation.
func NewWorkspaceStore(endpoint, bucket, region, accessKey, secretKey, prefix string, insecure bool) WorkspaceStore {
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return disabledWorkspaceStore{}
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: !insecure,
		Region: region,
	})
	if err != nil {
		return disabledWorkspaceStore{err: err}
	}
	return &minioWorkspaceStore{client: client, bucket: bucket, prefix: strings.Trim(prefix, "/")}
}

func (s *minioWorkspaceStore) Enabled() bool { return true }

func (s *minioWorkspaceStore) objectName(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}

// Put streams the archive with an unknown length, so the control plane never
// buffers a customer's workspace in memory to learn how big it is.
func (s *minioWorkspaceStore) Put(ctx context.Context, key string, r io.Reader) error {
	_, err := s.client.PutObject(ctx, s.bucket, s.objectName(key), r, -1, minio.PutObjectOptions{
		ContentType: "application/gzip",
	})
	if err != nil {
		return fmt.Errorf("put workspace archive: %w", err)
	}
	return nil
}

// Get opens the archive and proves it exists before returning a reader.
//
// minio's Get is lazy - it does not talk to the server until the first Read -
// so without the Stat a missing archive would surface as a tar decompression
// error inside the box instead of as a missing archive here.
func (s *minioWorkspaceStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, s.objectName(key), minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("open workspace archive: %w", err)
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, fmt.Errorf("%w: %s", ErrNoWorkspaceArchive, key)
		}
		return nil, fmt.Errorf("stat workspace archive: %w", err)
	}
	return obj, nil
}

func (s *minioWorkspaceStore) Remove(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, s.objectName(key), minio.RemoveObjectOptions{})
	if err != nil && minio.ToErrorResponse(err).Code != "NoSuchKey" {
		return fmt.Errorf("remove workspace archive: %w", err)
	}
	return nil
}

type disabledWorkspaceStore struct{ err error }

func (d disabledWorkspaceStore) Enabled() bool { return false }

func (d disabledWorkspaceStore) Put(context.Context, string, io.Reader) error {
	return d.reason()
}

func (d disabledWorkspaceStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, d.reason()
}

func (d disabledWorkspaceStore) Remove(context.Context, string) error { return nil }

func (d disabledWorkspaceStore) reason() error {
	if d.err != nil {
		return fmt.Errorf("box workspace store not configured: %w", d.err)
	}
	return errors.New("box workspace store not configured")
}

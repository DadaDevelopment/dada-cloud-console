package cloudtask

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestDBBackupPresignerDisabled asserts an unconfigured presigner degrades to a
// clear, non-panicking failure so the download handler can return 503.
func TestDBBackupPresignerDisabled(t *testing.T) {
	cases := map[string]DBBackupPresigner{
		"empty endpoint": NewDBBackupPresigner("", "bucket", "us-east-1", "ak", "sk", false),
		"empty bucket":   NewDBBackupPresigner("s3.example.com", "", "us-east-1", "ak", "sk", false),
		"empty access":   NewDBBackupPresigner("s3.example.com", "bucket", "us-east-1", "", "sk", false),
		"empty secret":   NewDBBackupPresigner("s3.example.com", "bucket", "us-east-1", "ak", "", false),
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if p.Enabled() {
				t.Fatal("expected disabled presigner")
			}
			if _, err := p.PresignGet(context.Background(), "dumps/x/y/z.dump", "z.dump", time.Minute); err == nil {
				t.Fatal("expected error from disabled presigner")
			}
			if err := p.PutObject(context.Background(), "volexports/x/y/z.tar.gz", strings.NewReader("x"), 1, "application/gzip"); err == nil {
				t.Fatal("expected error from disabled presigner PutObject")
			}
			deleted, err := p.DeleteOldObjects(context.Background(), "volexports/", 24*time.Hour)
			if err != nil {
				t.Fatalf("expected disabled presigner DeleteOldObjects to be a no-op, got error: %v", err)
			}
			if deleted != 0 {
				t.Fatalf("expected disabled presigner DeleteOldObjects to delete 0, got %d", deleted)
			}
		})
	}
}

// TestDBBackupPresignerSignsAttachmentURL asserts a configured presigner mints a
// signed GET URL for the dump object with an attachment content-disposition, so
// the browser downloads the dump straight from object storage. minio-go signs
// locally, so this makes no network call.
func TestDBBackupPresignerSignsAttachmentURL(t *testing.T) {
	p := NewDBBackupPresigner("s3.example.com", "dada-db-dumps", "us-east-1", "ak", "sk", false)
	if !p.Enabled() {
		t.Fatal("expected enabled presigner")
	}
	url, err := p.PresignGet(context.Background(), "dumps/proj/db/backup.dump", "mydb-abcd1234.dump", 5*time.Minute)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	for _, want := range []string{
		"https://s3.example.com/dada-db-dumps/dumps/proj/db/backup.dump",
		"X-Amz-Signature=",
		"response-content-disposition=",
		"mydb-abcd1234.dump",
	} {
		if !strings.Contains(url, want) {
			t.Errorf("presigned url missing %q\ngot: %s", want, url)
		}
	}
}

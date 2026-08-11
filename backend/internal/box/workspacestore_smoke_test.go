package box

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

// TestWorkspaceStoreRoundTripAgainstRealS3 is a manual probe, skipped unless the
// four BOX_WORKSPACE_S3_* values are exported. It exists because every wrong
// answer about the object store - endpoint, region, path style, bucket policy -
// looks identical from inside a unit test with a fake, and only shows up as a
// suspend that refuses to put a customer's box to sleep.
func TestWorkspaceStoreRoundTripAgainstRealS3(t *testing.T) {
	endpoint := os.Getenv("BOX_WORKSPACE_S3_ENDPOINT")
	bucket := os.Getenv("BOX_WORKSPACE_S3_BUCKET")
	access := os.Getenv("BOX_WORKSPACE_S3_ACCESS_KEY")
	secret := os.Getenv("BOX_WORKSPACE_S3_SECRET_KEY")
	if endpoint == "" || bucket == "" || access == "" || secret == "" {
		t.Skip("no BOX_WORKSPACE_S3_* credentials in the environment")
	}
	store := NewWorkspaceStore(endpoint, bucket, os.Getenv("BOX_WORKSPACE_S3_REGION"), access, secret, "box-workspaces-smoke", false)
	if !store.Enabled() {
		t.Fatal("store reports itself disabled with a full set of credentials")
	}
	ctx := context.Background()
	body := []byte("workspace archive round trip")
	if err := store.Put(ctx, "smoke.tar.gz", bytes.NewReader(body)); err != nil {
		t.Fatalf("put: %v", err)
	}
	rc, err := store.Get(ctx, "smoke.tar.gz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("round trip changed the bytes: %q", got)
	}
	if err := store.Remove(ctx, "smoke.tar.gz"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := store.Get(ctx, "smoke.tar.gz"); !errors.Is(err, ErrNoWorkspaceArchive) {
		t.Fatalf("after remove, get returned %v, want ErrNoWorkspaceArchive: a Destroy that leaves the archive behind is a leak nobody bills for", err)
	}
}

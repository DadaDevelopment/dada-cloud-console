package box

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/dada-tuda/console/backend/internal/boxcatalog"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeWorkspaceStore is an in-memory stand-in for object storage.
type fakeWorkspaceStore struct {
	objects map[string][]byte
	on      bool
	putErr  error
}

func newFakeWorkspaceStore() *fakeWorkspaceStore {
	return &fakeWorkspaceStore{objects: map[string][]byte{}, on: true}
}

func (f *fakeWorkspaceStore) Enabled() bool { return f.on }

func (f *fakeWorkspaceStore) Put(_ context.Context, key string, r io.Reader) error {
	if f.putErr != nil {
		return f.putErr
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.objects[key] = body
	return nil
}

func (f *fakeWorkspaceStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	body, ok := f.objects[key]
	if !ok {
		return nil, ErrNoWorkspaceArchive
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (f *fakeWorkspaceStore) Remove(_ context.Context, key string) error {
	delete(f.objects, key)
	return nil
}

// TestBuildPodDropsTheClaimWhenWorkspacesAreArchived is the shape of the cold
// start fix. A box-up on a node that already holds the image measured 16.7s, and
// ~13s of that was provisioning and attaching a Longhorn volume for a workspace
// that starts empty. An emptyDir needs neither.
func TestBuildPodDropsTheClaimWhenWorkspacesAreArchived(t *testing.T) {
	rt := newClusterRuntime(fake.NewSimpleClientset(), nil, "dada-boxes", nil)
	rt.Workspaces = newFakeWorkspaceStore()

	img, _ := boxcatalog.LookupImage("warm-v1")
	size := boxcatalog.DefaultSize()
	pod := rt.BuildPod("w1", img, size, "")

	var workspace *corev1.Volume
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == clusterWorkspaceVolume {
			workspace = &pod.Spec.Volumes[i]
		}
	}
	if workspace == nil {
		t.Fatal("the pod has no workspace volume at all")
	}
	if workspace.PersistentVolumeClaim != nil {
		t.Fatalf("workspace is still a claim (%s): the whole point is that a box no longer waits for Longhorn to provision and attach one",
			workspace.PersistentVolumeClaim.ClaimName)
	}
	if workspace.EmptyDir == nil || workspace.EmptyDir.SizeLimit == nil {
		t.Fatal("workspace emptyDir has no sizeLimit: without one a box can fill the node's disk, which took the platform's own Postgres down on 2026-07-30")
	}
	if want := resource.MustParse("10Gi"); workspace.EmptyDir.SizeLimit.Cmp(want) != 0 {
		t.Fatalf("workspace sizeLimit = %s, want %s: the customer was sold %dGB", workspace.EmptyDir.SizeLimit, &want, size.DiskGB)
	}

	res := pod.Spec.Containers[0].Resources
	wantEphemeral := resource.MustParse("13Gi")
	got := res.Requests[corev1.ResourceEphemeralStorage]
	if got.Cmp(wantEphemeral) != 0 {
		t.Fatalf("ephemeral-storage request = %s, want %s: the scheduler only refuses to overcommit a node's disk for space that was REQUESTED", &got, &wantEphemeral)
	}
	gotLimit := res.Limits[corev1.ResourceEphemeralStorage]
	if gotLimit.Cmp(wantEphemeral) != 0 {
		t.Fatalf("ephemeral-storage limit = %s, want %s", &gotLimit, &wantEphemeral)
	}
}

// TestBuildPodKeepsTheClaimWithoutAStore proves the feature is off by default.
// An ephemeral workspace with nowhere to archive to is a sleep that loses the
// customer's work, so the absence of a store must leave the PVC path in force.
func TestBuildPodKeepsTheClaimWithoutAStore(t *testing.T) {
	rt := newClusterRuntime(fake.NewSimpleClientset(), nil, "dada-boxes", nil)

	img, _ := boxcatalog.LookupImage("warm-v1")
	pod := rt.BuildPod("w1", img, boxcatalog.DefaultSize(), "")

	for _, v := range pod.Spec.Volumes {
		if v.Name != clusterWorkspaceVolume {
			continue
		}
		if v.PersistentVolumeClaim == nil {
			t.Fatal("workspace is not a claim with no archive store configured: every sleep would silently discard the workspace")
		}
		if v.PersistentVolumeClaim.ClaimName != clusterPVCName("w1") {
			t.Fatalf("claim = %q, want %q", v.PersistentVolumeClaim.ClaimName, clusterPVCName("w1"))
		}
	}
	req := pod.Spec.Containers[0].Resources.Requests[corev1.ResourceEphemeralStorage]
	if want := resource.MustParse("1Gi"); req.Cmp(want) != 0 {
		t.Fatalf("ephemeral-storage request = %s, want %s: a claim-backed box writes the workspace to Longhorn, not to the node", &req, &want)
	}
}

// TestSuspendKeepsTheBodyWhenTheArchiveFails is the safety property the whole
// design rests on. Deleting the pod is what makes an ephemeral workspace
// unrecoverable, so it must not happen unless the archive is already written.
func TestSuspendKeepsTheBodyWhenTheArchiveFails(t *testing.T) {
	pod := parkedPod("box-w1", "warm-v1", "", true)
	cs := fake.NewSimpleClientset(pod)
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	store := newFakeWorkspaceStore()
	store.putErr = errors.New("object storage is down")
	rt.Workspaces = store

	err := rt.Suspend(context.Background(), &Instance{ID: "w1", InstanceRef: "box-w1"})
	if err == nil {
		t.Fatal("Suspend reported success with no archive written: the box's work is gone and the API called it non-destructive")
	}
	if _, getErr := cs.CoreV1().Pods("dada-boxes").Get(context.Background(), "box-w1", metav1.GetOptions{}); getErr != nil {
		t.Fatalf("the pod was deleted anyway (%v): a failed archive has to leave the box running, because an idle box costs money and a lost workspace costs the customer their prototype", getErr)
	}
}

// TestSuspendRefusesAnEphemeralBoxWithNoStore covers the configuration that
// would otherwise be silent data loss: a box built on an emptyDir while a store
// was configured, suspended after the store was taken away.
func TestSuspendRefusesAnEphemeralBoxWithNoStore(t *testing.T) {
	cs := fake.NewSimpleClientset(parkedPod("box-w1", "warm-v1", "", true))
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)

	err := rt.Suspend(context.Background(), &Instance{ID: "w1", InstanceRef: "box-w1"})
	if err == nil {
		t.Fatal("Suspend deleted a body whose workspace lived only in that body and had nowhere to go")
	}
	if _, getErr := cs.CoreV1().Pods("dada-boxes").Get(context.Background(), "box-w1", metav1.GetOptions{}); getErr != nil {
		t.Fatalf("the pod was deleted anyway: %v", getErr)
	}
}

// TestSuspendLeavesAClaimedBoxAlone is the migration guarantee. A box created
// before ephemeral workspaces existed owns a Longhorn volume with the customer's
// work on it; suspending it must keep behaving exactly as it did.
func TestSuspendLeavesAClaimedBoxAlone(t *testing.T) {
	claim := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "box-w1-workspace", Namespace: "dada-boxes"},
	}
	cs := fake.NewSimpleClientset(parkedPod("box-w1", "warm-v1", "", true), claim)
	rt := newClusterRuntime(cs, nil, "dada-boxes", nil)
	store := newFakeWorkspaceStore()
	rt.Workspaces = store

	if err := rt.Suspend(context.Background(), &Instance{ID: "w1", InstanceRef: "box-w1"}); err != nil {
		t.Fatalf("Suspend of a claim-backed box failed: %v", err)
	}
	if len(store.objects) != 0 {
		t.Fatalf("a claim-backed box was archived as well (%v): the claim already holds the work, and the archive would be restored over it on resume", store.objects)
	}
	if _, err := cs.CoreV1().PersistentVolumeClaims("dada-boxes").Get(context.Background(), "box-w1-workspace", metav1.GetOptions{}); err != nil {
		t.Fatalf("the workspace claim was deleted by a sleep: %v", err)
	}
	if _, err := cs.CoreV1().Pods("dada-boxes").Get(context.Background(), "box-w1", metav1.GetOptions{}); err == nil {
		t.Fatal("the pod survived a suspend: the customer keeps paying for compute they are not using")
	}
}

// TestWorkspaceArchiveKeyFollowsThePodName pins the identity the archive is
// stored under. A box loaded out of the boxes table carries the control plane's
// uuid in ID and the pod name in InstanceRef, so keying on ID would archive under
// one name and look for another - which reads as a missing archive.
func TestWorkspaceArchiveKeyFollowsThePodName(t *testing.T) {
	fromPool := workspaceArchiveKey(&Instance{ID: "w1", InstanceRef: "box-w1"})
	fromRow := workspaceArchiveKey(&Instance{ID: "0f0d1f8e-0000-0000-0000-000000000000", InstanceRef: "box-w1"})
	if fromPool != fromRow {
		t.Fatalf("archive key differs by how the instance was built: %q vs %q", fromPool, fromRow)
	}
	if fromPool != "box-w1.tar.gz" {
		t.Fatalf("archive key = %q, want box-w1.tar.gz", fromPool)
	}
}

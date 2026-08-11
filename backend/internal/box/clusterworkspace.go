package box

import (
	"context"
	"fmt"
	"io"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ephemeralWorkspace reports whether new bodies get a node-local workspace
// instead of a Longhorn claim.
//
// Ephemeral workspaces are the answer to a measured cost. A cold box-up on a
// node that already holds the image took 16.7s wall, and 13 of those seconds
// were the workspace disk: ~2s for Longhorn to provision the volume and ~11s for
// the engine to come up, for the CSI attacher to be asked again, and for the
// kubelet to stage and mount it. None of that work is about the customer's files
// - a brand-new box's workspace is EMPTY - it is the price of asking for a
// network block device before the pod can start. An emptyDir is handed over by
// the kubelet at pod start with no provisioner, no attach and no engine, so a box
// that does not need to survive its own body does not pay for one. It also ends
// the parked-disk bill: on 2026-08-04, 96% of all metered box minutes were
// suspended_disk.
//
// What replaces the durability. The claim was not decoration: a sleep must give
// the customer their work back. So durability moves to the moment it is actually
// needed - Suspend tars /workspace out of the live pod into object storage BEFORE
// the body is deleted, and Resume streams it back once the new body answers. The
// archive is the thing that must succeed; if it does not, the suspend fails and
// the box stays up, because a box left running costs money and a box put down
// without its archive costs the customer their prototype.
//
// Both directions go through the control plane's own exec channel, so the box
// never receives an S3 credential. A box is untrusted by construction - it is a
// container the tenant has root in - and handing it a bucket key so it could
// upload its own workspace would hand it every other tenant's archive too.
//
// The value is derived rather than configured so the two facts cannot drift
// apart: an ephemeral workspace is only safe when there is somewhere to archive
// it to, and this is that somewhere.
func (c *ClusterRuntime) ephemeralWorkspace() bool {
	return c.Workspaces != nil && c.Workspaces.Enabled()
}

// workspaceArchiveKey names a box's archive.
//
// It keys off InstanceRef (the pod name) rather than Instance.ID for the same
// reason clusterClaimNameFor does: a box loaded out of the boxes table carries
// the control plane's uuid in ID, so keying on it would archive under one name
// and restore from another - which reads as "no archive" and loses the work.
func workspaceArchiveKey(inst *Instance) string {
	name := inst.InstanceRef
	if name == "" {
		name = clusterPodName(inst.ID)
	}
	return name + ".tar.gz"
}

// hasWorkspaceClaim reports whether this box owns a PVC.
//
// It is the migration seam. Boxes created before ephemeral workspaces existed
// hold a Longhorn claim with the customer's work on it, and rebuilding such a box
// on an emptyDir would silently discard it. The claim, where one exists,
// therefore always wins: no archive is taken, no archive is restored, and the pod
// is rebuilt on the disk it already has.
func (c *ClusterRuntime) hasWorkspaceClaim(ctx context.Context, inst *Instance) (bool, error) {
	_, err := c.clientset.CoreV1().PersistentVolumeClaims(c.Namespace).
		Get(ctx, clusterClaimNameFor(inst), metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("look up workspace claim: %w", err)
}

// archiveWorkspace tars the live box's workspace into object storage.
//
// GNU tar's own -z is used rather than a shell pipe into gzip. The exec channel
// runs /bin/sh -c, dash has no pipefail, and in a pipeline the exit status is
// gzip's - so a tar that failed halfway would report success and the suspend
// would delete the body over a truncated archive.
//
// No timeout is imposed beyond the caller's context on purpose: a ceiling that
// fires mid-transfer would cut a large workspace into an archive that looks like
// a successful upload and restores as garbage.
func (c *ClusterRuntime) archiveWorkspace(ctx context.Context, inst *Instance) error {
	if c.restCfg == nil {
		return fmt.Errorf("archive box workspace: this runtime has no exec channel")
	}
	pr, pw := io.Pipe()
	stderr := &syncBuffer{}
	go func() {
		err := c.execStream(ctx, inst.InstanceRef, clusterContainerName,
			fmt.Sprintf("tar -cz --numeric-owner -f - -C %s .", clusterWorkspacePath),
			nil, pw, stderr)
		if err != nil {
			err = fmt.Errorf("tar out of the box: %w: %s", err, stderr.String())
		}
		_ = pw.CloseWithError(err)
	}()

	err := c.Workspaces.Put(ctx, workspaceArchiveKey(inst), pr)
	_ = pr.CloseWithError(err)
	if err != nil {
		return fmt.Errorf("archive box workspace: %w", err)
	}
	return nil
}

// restoreWorkspace streams a box's archive back into its new body.
//
// --overwrite is deliberate: the fresh body's /workspace holds only whatever the
// image's entrypoint put there, and the archive is the truth about what the
// customer had.
func (c *ClusterRuntime) restoreWorkspace(ctx context.Context, inst *Instance) error {
	if c.restCfg == nil {
		return fmt.Errorf("restore box workspace: this runtime has no exec channel")
	}
	src, err := c.Workspaces.Get(ctx, workspaceArchiveKey(inst))
	if err != nil {
		return fmt.Errorf("restore box workspace: %w", err)
	}
	defer func() { _ = src.Close() }()

	stderr := &syncBuffer{}
	err = c.execStream(ctx, inst.InstanceRef, clusterContainerName,
		fmt.Sprintf("tar -xz --numeric-owner --overwrite -f - -C %s", clusterWorkspacePath),
		src, nil, stderr)
	if err != nil {
		return fmt.Errorf("restore box workspace: untar into the box: %w: %s", err, stderr.String())
	}
	return nil
}

// dropWorkspaceArchive removes a destroyed box's archive.
//
// Failures are swallowed rather than returned. A leaked object costs a fraction
// of a cent and the next box of the same name overwrites it, while a Destroy that
// fails on the object store leaves the box stuck in Deleting with its pod already
// gone - the expensive half of the cleanup done and the cheap half blocking the
// row.
func (c *ClusterRuntime) dropWorkspaceArchive(ctx context.Context, inst *Instance) {
	if c.Workspaces == nil || !c.Workspaces.Enabled() {
		return
	}
	_ = c.Workspaces.Remove(ctx, workspaceArchiveKey(inst))
}

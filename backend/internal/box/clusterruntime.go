package box

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/dada-tuda/console/backend/internal/boxcatalog"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/utils/ptr"
)

// ClusterRuntime runs boxes as Pods in the existing cluster (ADR-019).
//
// A box is a container, not a micro-VM. The whole isolation story rests on ONE
// field, hostUsers: false, which gives the agent real root inside a user
// namespace while the pod still passes Pod Security Standards restricted. Every
// other hardening knob here is defence in depth; that one is load-bearing, and
// backend/internal/box/manifests/box-pod.yaml is the canon this builder is pinned
// against by test.
//
// The pool is pods, so warming means "pods that already exist, parked". Pulling a
// multi-GB warm image is the expensive part of a cold start and it happens once
// per node ahead of demand, which is the same reason the local adapter pre-builds
// trees rather than creating them per claim.
//
// Fields:
//
//   - Namespace holds every box pod. It carries the restricted PSS labels, a
//     ResourceQuota and a LimitRange, so a box that escapes its own limits still
//     cannot take the namespace past what an operator sized it for.
//   - PullSecret is the imagePullSecret name inside Namespace. Empty means the
//     warm image is public or already present on the node.
//   - StorageClass backs the per-box workspace PVC.
//   - BrokerPort is the port the box's own door binds inside the pod. It is also
//     the pod's readiness signal, because a box whose door does not accept is not
//     a box a tenant can use.
//   - ReadyTimeout overrides clusterDefaultReadyTimeout when positive. Left at
//     zero in production; a test shrinks it to keep the failed-create path fast
//     instead of waiting out the real default.
type ClusterRuntime struct {
	clientset kubernetes.Interface
	restCfg   *rest.Config
	clock     Clock

	Namespace    string
	PullSecret   string
	StorageClass string
	BrokerPort   int
	ReadyTimeout time.Duration
}

// readyTimeout is the bound waitReady applies, defaulting when unset.
func (c *ClusterRuntime) readyTimeout() time.Duration {
	if c.ReadyTimeout > 0 {
		return c.ReadyTimeout
	}
	return clusterDefaultReadyTimeout
}

const (
	clusterBrokerPort       = 8080
	clusterBrokerDir        = "/run/dada-broker"
	clusterTokensPath       = clusterBrokerDir + "/tokens"
	clusterWorkspacePath    = "/workspace"
	clusterContainerName    = "box"
	clusterWorkspaceVolume  = "workspace"
	clusterReadyPollPeriod  = time.Second
	clusterDestroyGraceSecs = 10
)

// clusterDefaultReadyTimeout bounds a single pod's wait for its startup probe.
// It is deliberately much shorter than the 20-minute budget Warm's caller gives
// the whole pool fill: that budget is sized for warming several slots in
// sequence, not for how long one stuck pod may sit before createParked gives up
// and destroys it. A pod stuck in ImagePullBackOff for the full 20 minutes
// outlives the console backend's own startupProbe deadline (150s, see
// helm/dada-cloud-console/templates/backend-deployment.yaml) many times over — if
// the backend gets restarted before its own createParked call reaches this
// timeout, the pod and its PVC are orphaned with no goroutine left alive to clean
// them up. Keeping this well under a process's typical lifetime is what lets a
// failed create clean up after itself instead of depending on staying alive long
// enough to notice.
const clusterDefaultReadyTimeout = 3 * time.Minute

// clusterOrphanAfter is the reaper's threshold: a box pod that has not gone Ready
// this long after creation is treated as abandoned, regardless of which process
// created it. It matches clusterDefaultReadyTimeout because a healthy create
// path never leaves a pod parked-and-not-Ready past that point on its own — every
// pod slower than this is either mid-fix by another createParked call or already
// dead and simply not yet swept.
const clusterOrphanAfter = clusterDefaultReadyTimeout

// clusterClaimOrphanAfter is the reaper's threshold for the other way a pod is
// abandoned: claimed out of the pool and never moved on to live.
//
// A claimed pod is Ready, so the not-Ready rule above cannot see it, and its
// creation time says nothing — it may have sat parked for hours before the claim.
// What bounds it is the claim itself: Spawn goes from Claim to ProgramNetwork in
// seconds, so a pod still claimed long afterwards belongs to a spawn that died in
// between, and nothing will ever come back for it. Twice the ready timeout leaves
// room for the slowest honest spawn, whose Unfreeze can burn the whole of one.
const clusterClaimOrphanAfter = 2 * clusterDefaultReadyTimeout

// labelBox, labelBoxID and labelBoxPhase mark every object this adapter owns, so
// a sweep can find them all without parsing names.
const (
	labelBox      = "dada.io/box"
	labelBoxID    = "dada.io/box-id"
	labelBoxPhase = "dada.io/box-phase"
)

// phaseParked is a warm pod nobody has claimed: it exists, its door is up, and
// the egress NetworkPolicy that selects this label lets it reach nothing.
// phaseLive is a claimed pod, selected by the tenant-egress policy instead. The
// label IS the network transition, which is why ProgramNetwork is a patch rather
// than a per-box policy object: one policy per box would put a write on the claim
// path and leave garbage behind whenever a delete raced a reconcile.
const (
	phaseParked = "parked"
	phaseLive   = "live"
)

// NewClusterRuntime builds an adapter against the in-cluster API server, using
// the pod's own service-account credentials. Off-cluster it returns an error
// rather than a crippled adapter: unlike a credentials resolver, a half-working
// box runtime cannot answer any verb usefully, and initBoxRuntime already knows
// how to leave the whole subsystem off.
func NewClusterRuntime(namespace string, clock Clock) (*ClusterRuntime, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	return newClusterRuntime(clientset, cfg, namespace, clock), nil
}

func newClusterRuntime(clientset kubernetes.Interface, restCfg *rest.Config, namespace string, clock Clock) *ClusterRuntime {
	if clock == nil {
		clock = SystemClock{}
	}
	return &ClusterRuntime{
		clientset:    clientset,
		restCfg:      restCfg,
		clock:        clock,
		Namespace:    namespace,
		StorageClass: "longhorn-box",
		BrokerPort:   clusterBrokerPort,
	}
}

// clusterPodName is the pod name for a box id. Names are derived rather than
// stored so a reconcile can find a box's objects from its id alone.
func clusterPodName(boxID string) string { return "box-" + boxID }

// clusterPVCName is the workspace claim name for a box id.
func clusterPVCName(boxID string) string { return clusterPodName(boxID) + "-workspace" }

// clusterClaimNameFor is the workspace claim of an instance the caller may have
// rebuilt from a database row rather than from the pool.
//
// It reads InstanceRef first because that is the only identity that survives the
// round trip. A pooled instance carries the runtime's own id ("w<nanos>"), from
// which both object names derive; a box loaded out of the boxes table carries the
// CONTROL PLANE's uuid in ID and the pod name in InstanceRef. Deriving the claim
// from ID in that second case names a claim that has never existed, the delete
// succeeds against nothing, and a 20Gi volume is left behind for a box the
// customer was told is gone — six of those exhaust the fleet quota and every
// later box fails to create.
func clusterClaimNameFor(inst *Instance) string {
	if inst.InstanceRef != "" {
		return inst.InstanceRef + "-workspace"
	}
	return clusterPVCName(inst.ID)
}

// Warm brings the pool up to n free pods, parking each one once its door accepts.
//
// It reconciles rather than creates: n is the TARGET, and only the shortfall is
// built. That is what makes it safe to call on a ticker, which is what the caller
// does — a pool filled once at boot is empty forever after the first claim, so
// the second customer of the day gets pool_exhausted from a control plane that
// has been up for hours and is not even trying. Calling this with the same n
// repeatedly is therefore a no-op while the pool is full and a refill the moment
// it is not.
//
// Failures are per-pod and non-fatal: a partially warmed pool is a real pool with
// fewer slots, and reporting the shortfall is more useful than refusing to serve.
func (c *ClusterRuntime) Warm(ctx context.Context, pool ParkingPool, image, region string, n int) error {
	pool.SetTarget(image, region, n)
	have, err := poolInventory(ctx, pool, image, region)
	if err != nil {
		return fmt.Errorf("count warm boxes: %w", err)
	}
	if have > n {
		if _, err := poolTrim(ctx, pool, image, region, n); err != nil {
			return err
		}
		return nil
	}
	deficit := n - have
	var firstErr error
	created := 0
	for i := 0; i < deficit; i++ {
		inst, err := c.createParked(ctx, image, region)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		pool.Add(image, region, inst)
		created++
	}
	if created == 0 && firstErr != nil {
		return fmt.Errorf("warm %d boxes: %w", deficit, firstErr)
	}
	if firstErr != nil {
		return fmt.Errorf("warmed %d of %d boxes: %w", created, deficit, firstErr)
	}
	return nil
}

// createParked creates one PVC + Pod pair and waits for the door to accept. A pod
// that never becomes ready is destroyed rather than parked, because a pool slot
// that fails on claim is worse than a smaller pool.
func (c *ClusterRuntime) createParked(ctx context.Context, image, region string) (*Instance, error) {
	return c.createBody(ctx, image, region, phaseParked)
}

// createClaimed creates a body that belongs to its caller from birth.
//
// It exists for the cold start the pool pays when a burst empties it. Creating
// the pod parked and claiming it afterwards would be a race with every other
// claimer and with the fill loop: the customer who waited three minutes for the
// pod would watch someone else take it and get pool_exhausted anyway. Born
// claimed, no NetworkPolicy selects it and nothing else can list it as free.
func (c *ClusterRuntime) createClaimed(ctx context.Context, image, region string) (*Instance, error) {
	return c.createBody(ctx, image, region, phaseClaimed)
}

func (c *ClusterRuntime) createBody(ctx context.Context, image, region, phase string) (*Instance, error) {
	img, ok := boxcatalog.LookupImage(image)
	if !ok {
		return nil, fmt.Errorf("unknown warm image %q", image)
	}
	size := boxcatalog.DefaultSize()
	boxID := fmt.Sprintf("w%d", c.clock.Now().UnixNano())

	pvc := c.buildPVC(boxID, size)
	if _, err := c.clientset.CoreV1().PersistentVolumeClaims(c.Namespace).Create(ctx, pvc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("create workspace claim: %w", err)
	}
	pod := c.BuildPod(boxID, img, size, region)
	pod.Labels[labelBoxPhase] = phase
	if phase == phaseClaimed {
		pod.Annotations[annBoxClaimedAt] = c.clock.Now().UTC().Format(time.RFC3339)
	}
	if _, err := c.clientset.CoreV1().Pods(c.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		_ = c.deleteClaim(ctx, clusterPVCName(boxID))
		return nil, fmt.Errorf("create box pod: %w", err)
	}

	inst := &Instance{
		ID:          boxID,
		InstanceRef: pod.Name,
		Image:       image,
		Region:      region,
	}
	if err := c.waitReady(ctx, inst); err != nil {
		_ = c.Destroy(context.WithoutCancel(ctx), inst)
		return nil, err
	}
	return inst, nil
}

// buildPVC is the per-box workspace claim. It is a separate object from the pod
// so a crash-looping pod can be replaced without losing the agent's work, which
// is the difference between a restart and a lost session.
func (c *ClusterRuntime) buildPVC(boxID string, size boxcatalog.Size) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterPVCName(boxID),
			Namespace: c.Namespace,
			Labels: map[string]string{
				labelBox:   "true",
				labelBoxID: boxID,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: ptr.To(c.StorageClass),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", size.DiskGB)),
				},
			},
		},
	}
}

// BuildPod renders the box pod. Exported so the canon test can compare it field
// by field against manifests/box-pod.yaml: the YAML is what an operator reads and
// what the isolation probe applied, so the two must not be allowed to drift.
//
// hostUsers: false is the field that cannot be removed. Without it the agent's
// root is the node's root; with it the box passes PSS restricted AND the agent
// gets a real uid 0 to apt-install with. Every capability is dropped for the same
// reason: inside the user namespace the box does not need them, and outside it
// they would be the escape.
func (c *ClusterRuntime) BuildPod(boxID string, img boxcatalog.Image, size boxcatalog.Size, region string) *corev1.Pod {
	ref := img.Ref
	if img.Digest != "" {
		ref = img.Ref + "@" + img.Digest
	}
	port := c.BrokerPort
	if port == 0 {
		port = clusterBrokerPort
	}
	cpuLimit := resource.MustParse(fmt.Sprintf("%d", size.VCPU))
	cpuRequest := resource.MustParse(fmt.Sprintf("%dm", size.VCPU*1000/3))
	mem := resource.MustParse(fmt.Sprintf("%dMi", size.MemoryMB))

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterPodName(boxID),
			Namespace: c.Namespace,
			Labels: map[string]string{
				labelBox:      "true",
				labelBoxID:    boxID,
				labelBoxPhase: phaseParked,
			},
			Annotations: map[string]string{
				"dada.io/box-image":  img.Name,
				"dada.io/box-size":   size.Name,
				"dada.io/box-region": region,
			},
		},
		Spec: corev1.PodSpec{
			HostUsers:                     ptr.To(false),
			AutomountServiceAccountToken:  ptr.To(false),
			EnableServiceLinks:            ptr.To(false),
			ServiceAccountName:            "box-runner",
			PriorityClassName:             "other",
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: ptr.To(int64(clusterDestroyGraceSecs)),
			ActiveDeadlineSeconds:         ptr.To(clusterDeadlineFor(size)),
			SecurityContext: &corev1.PodSecurityContext{
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				FSGroup:        ptr.To(int64(0)),
			},
			Volumes: []corev1.Volume{
				{
					Name: clusterWorkspaceVolume,
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: clusterPVCName(boxID)},
					},
				},
				{
					Name: "tmp",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptr.To(resource.MustParse("2Gi"))},
					},
				},
				{
					Name: "run",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{
							Medium:    corev1.StorageMediumMemory,
							SizeLimit: ptr.To(resource.MustParse("64Mi")),
						},
					},
				},
			},
			Containers: []corev1.Container{{
				Name:            clusterContainerName,
				Image:           ref,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{"/usr/local/bin/box-init"},
				WorkingDir:      clusterWorkspacePath,
				Env: []corev1.EnvVar{
					{Name: "BOX_ID", Value: boxID},
					{Name: "BOX_BROKER_ADDR", Value: fmt.Sprintf("0.0.0.0:%d", port)},
					{Name: "HOME", Value: "/root"},
				},
				Ports: []corev1.ContainerPort{{Name: "broker", ContainerPort: int32(port)}},
				SecurityContext: &corev1.SecurityContext{
					RunAsUser:                ptr.To(int64(0)),
					RunAsGroup:               ptr.To(int64(0)),
					AllowPrivilegeEscalation: ptr.To(false),
					ReadOnlyRootFilesystem:   ptr.To(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				},
				ResizePolicy: []corev1.ContainerResizePolicy{
					{ResourceName: corev1.ResourceCPU, RestartPolicy: corev1.NotRequired},
					{ResourceName: corev1.ResourceMemory, RestartPolicy: corev1.NotRequired},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:              cpuRequest,
						corev1.ResourceMemory:           mem,
						corev1.ResourceEphemeralStorage: resource.MustParse("1Gi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:              cpuLimit,
						corev1.ResourceMemory:           mem,
						corev1.ResourceEphemeralStorage: resource.MustParse("4Gi"),
					},
				},
				VolumeMounts: []corev1.VolumeMount{
					{Name: clusterWorkspaceVolume, MountPath: clusterWorkspacePath},
					{Name: "tmp", MountPath: "/tmp"},
					{Name: "run", MountPath: "/run/dada"},
				},
				StartupProbe: &corev1.Probe{
					ProbeHandler:     corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("broker")}},
					PeriodSeconds:    1,
					FailureThreshold: 60,
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler:     corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("broker")}},
					PeriodSeconds:    10,
					FailureThreshold: 3,
				},
			}},
		},
	}
	if c.PullSecret != "" {
		pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: c.PullSecret}}
	}
	return pod
}

// clusterDeadlineFor is the kubelet's backstop, deliberately LOOSER than the
// catalog's MaxTTL rather than equal to it.
//
// The reaper is what enforces the product's TTL, and it can extend a box a caller
// asked to keep. activeDeadlineSeconds cannot be extended after creation, so
// setting it to MaxTTL would make the kubelet kill boxes the control plane had
// legitimately prolonged. Half again as long leaves the reaper in charge while
// still guaranteeing that a box outliving a dead control plane eventually dies.
func clusterDeadlineFor(size boxcatalog.Size) int64 {
	return int64(size.MaxTTLSeconds) + int64(size.MaxTTLSeconds)/2
}

// waitReady blocks until the pod reports Ready or the context expires. Readiness
// here is only the door accepting; the toolchain canary in EvaluateReadiness is
// what decides the box is actually usable, and it runs on the claim path.
func (c *ClusterRuntime) waitReady(ctx context.Context, inst *Instance) error {
	ctx, cancel := context.WithTimeout(ctx, c.readyTimeout())
	defer cancel()
	ticker := time.NewTicker(clusterReadyPollPeriod)
	defer ticker.Stop()
	for {
		pod, err := c.clientset.CoreV1().Pods(c.Namespace).Get(ctx, inst.InstanceRef, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get box pod: %w", err)
		}
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			return fmt.Errorf("box pod %s reached %s before becoming ready: %s", pod.Name, pod.Status.Phase, pod.Status.Reason)
		}
		if clusterPodReady(pod) {
			inst.NodeRef = pod.Spec.NodeName
			inst.SSHHost = pod.Status.PodIP
			inst.SSHPort = c.BrokerPort
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("box pod %s not ready: %w", inst.InstanceRef, ctx.Err())
		case <-ticker.C:
		}
	}
}

// clusterPodReady reports whether the kubelet has marked the pod Ready, which for
// a box means its startup probe saw the door accept.
func clusterPodReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// Bind attaches tenant identity to a parked pod.
//
// The pod already runs; binding writes the tenant's environment and public key
// into it. Env goes to a file rather than to the pod spec because a container's
// env is fixed at creation, and re-creating the pod to set a variable would throw
// away the warm pull that makes a claim fast.
//
// The file is 0600: it holds credentials handed to this tenant, and the default
// mask on the pod's setgid tree would otherwise leave them group-readable.
func (c *ClusterRuntime) Bind(ctx context.Context, inst *Instance, spec Spec) error {
	var b strings.Builder
	b.WriteString("set -e\n")
	b.WriteString("mkdir -p /root/.ssh /etc/dada\n")
	for k, v := range spec.Env {
		if !validEnvKey(k) {
			return fmt.Errorf("invalid environment key %q", k)
		}
		fmt.Fprintf(&b, "printf '%%s\\n' %s >> /etc/dada/env\n", shellQuote(k+"="+v))
	}
	if len(spec.Env) > 0 {
		b.WriteString("chmod 600 /etc/dada/env\n")
	}
	if spec.SSHPublicKey != "" {
		fmt.Fprintf(&b, "printf '%%s\\n' %s >> /root/.ssh/authorized_keys\n", shellQuote(spec.SSHPublicKey))
		b.WriteString("chmod 700 /root/.ssh && chmod 600 /root/.ssh/authorized_keys\n")
	}
	res, err := c.Exec(ctx, inst, b.String())
	if err != nil {
		return fmt.Errorf("bind identity: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("bind identity: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stdout))
	}
	return c.labelBoxName(ctx, inst, spec.Env["BOX_NAME"])
}

// labelBoxName puts the control plane's name for this box on the pod, because
// that is what a Service selector can be built from. The pool's own id changes
// with every claim, so an exposure selecting on it would point at nothing the
// next time the same box name was claimed.
func (c *ClusterRuntime) labelBoxName(ctx context.Context, inst *Instance, boxName string) error {
	if boxName == "" {
		return nil
	}
	patch := fmt.Sprintf(`{"metadata":{"labels":{%q:%q}}}`, labelBoxName, boxName)
	_, err := c.clientset.CoreV1().Pods(c.Namespace).Patch(ctx, inst.InstanceRef, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("label box pod with its name: %w", err)
	}
	return nil
}

// ProgramNetwork moves the pod from the parked label to the live one, which is
// what the tenant-egress NetworkPolicy selects. Nothing here creates a policy: the
// policies are namespace-level and static, and the pod's label is the only thing
// that moves per claim.
func (c *ClusterRuntime) ProgramNetwork(ctx context.Context, inst *Instance) error {
	patch := fmt.Sprintf(`{"metadata":{"labels":{%q:%q}}}`, labelBoxPhase, phaseLive)
	_, err := c.clientset.CoreV1().Pods(c.Namespace).Patch(ctx, inst.InstanceRef, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("mark box live: %w", err)
	}
	return nil
}

// Unfreeze waits for the door to accept. A pod is never actually frozen, so this
// is a re-check rather than a state change — kept as a distinct phase because the
// ready path measures it, and a phase that honestly costs nothing is information.
func (c *ClusterRuntime) Unfreeze(ctx context.Context, inst *Instance) error {
	return c.waitReady(ctx, inst)
}

// Exec runs one command inside the box and reports its exit status.
//
// Stderr is folded into Stdout on purpose: the canary reports tool versions as
// key=value and a missing tool complains on stderr, so keeping both in one stream
// is what lets EvaluateReadiness see "node=sh: node: not found" and reject it.
func (c *ClusterRuntime) Exec(ctx context.Context, inst *Instance, cmd string) (CanaryResult, error) {
	return c.execWithStdin(ctx, inst, cmd, nil)
}

// execWithStdin is the one place that talks to pods/exec. Writing a file into a
// box goes through stdin rather than through a shell heredoc, so a token never
// appears in a command line that a node's process list would show.
func (c *ClusterRuntime) execWithStdin(ctx context.Context, inst *Instance, cmd string, stdin *bytes.Buffer) (CanaryResult, error) {
	out := &syncBuffer{}
	var in io.Reader
	if stdin != nil {
		in = stdin
	}
	err := c.execStream(ctx, inst.InstanceRef, clusterContainerName, cmd, in, out, out)
	if err != nil {
		if code, ok := exitCodeFrom(err); ok {
			return CanaryResult{ExitCode: code, Stdout: out.String()}, nil
		}
		if apierrors.IsNotFound(err) {
			return CanaryResult{Stdout: out.String()}, fmt.Errorf("exec in box: %w: %v", ErrBodyGone, err)
		}
		return CanaryResult{Stdout: out.String()}, fmt.Errorf("exec in box: %w", err)
	}
	return CanaryResult{ExitCode: 0, Stdout: out.String()}, nil
}

// execStream is the raw pods/exec call: a shell command in a named container with
// caller-supplied streams.
//
// Crystallization needs this shape rather than execWithStdin's: a userland
// transfer is a tar of hundreds of megabytes piped out of one pod and into
// another, and buffering that in a bytes.Buffer inside the control plane would
// make the transfer's memory cost the size of the customer's data. Streams also
// keep stdout and stderr APART here, because a tar stream with a warning folded
// into it is a corrupt tar.
//
// The error is returned unwrapped so callers can still classify a non-zero exit
// with exitCodeFrom.
func (c *ClusterRuntime) execStream(ctx context.Context, podName, container, cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	opts := &corev1.PodExecOptions{
		Container: container,
		Command:   []string{"/bin/sh", "-c", cmd},
		Stdout:    stdout != nil,
		Stderr:    stderr != nil,
		Stdin:     stdin != nil,
	}
	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(c.Namespace).
		Name(podName).
		SubResource("exec").
		VersionedParams(opts, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.restCfg, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("build box exec: %w", err)
	}
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
	})
}

// syncBuffer is a bytes.Buffer that survives being written from two goroutines at
// once.
//
// THIS IS NOT DEFENSIVE PROGRAMMING, IT IS THE FIX FOR A LIVE FAILURE. remotecommand
// copies the remote stdout and the remote stderr in SEPARATE goroutines, and Exec
// deliberately points both at one writer so the canary sees "node=sh: node: not
// found" the way the shell produced it. A plain bytes.Buffer under two concurrent
// writers is a data race whose realistic outcome is not a crash but LOST BYTES —
// which surfaced as a box whose canary exited 0 with empty output and was rejected
// for "canary output missing dada-ready marker" while the exact same command run by
// hand in the exact same pod printed it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// exitCodeFrom separates "the command failed" from "we could not run it". A
// non-zero exit is a RESULT the ready path classifies; a transport failure is an
// error. Collapsing the two would report an unreachable box as a box with a
// broken toolchain.
func exitCodeFrom(err error) (int, bool) {
	var codeErr interface{ ExitStatus() int }
	if errors.As(err, &codeErr) {
		return codeErr.ExitStatus(), true
	}
	return 0, false
}

// Destroy deletes the pod and its workspace claim. The claim delete runs even
// when the pod delete failed, because a leaked PVC costs money silently.
func (c *ClusterRuntime) Destroy(ctx context.Context, inst *Instance) error {
	grace := int64(clusterDestroyGraceSecs)
	err := c.clientset.CoreV1().Pods(c.Namespace).Delete(ctx, inst.InstanceRef, metav1.DeleteOptions{GracePeriodSeconds: &grace})
	if err != nil && !apierrors.IsNotFound(err) {
		_ = c.deleteClaim(ctx, clusterClaimNameFor(inst))
		return fmt.Errorf("delete box pod: %w", err)
	}
	return c.deleteClaim(ctx, clusterClaimNameFor(inst))
}

// Suspend deletes the pod and KEEPS the workspace claim.
//
// That asymmetry with Destroy is the entire feature: a suspended box costs the
// customer storage instead of compute and comes back with its work intact, so
// deleting the claim here would turn a sleep into a data loss the API calls
// non-destructive. The pod name is derived from the box id, so the body that
// Resume creates lands on the same claim.
func (c *ClusterRuntime) Suspend(ctx context.Context, inst *Instance) error {
	grace := int64(clusterDestroyGraceSecs)
	err := c.clientset.CoreV1().Pods(c.Namespace).Delete(ctx, inst.InstanceRef, metav1.DeleteOptions{GracePeriodSeconds: &grace})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete box pod: %w", err)
	}
	return nil
}

// Resume rebuilds the pod of a suspended box and waits for its door to accept.
//
// It does NOT go through the warm pool. A pool body is a fresh disk, and this box
// already owns one; claiming a warm instance would hand the customer somebody
// else's empty workspace under their own box's name. The cost is a cold start,
// which is the honest price of having stopped paying for the running body.
//
// The rebuilt pod is born LIVE rather than parked, and that is not cosmetic. The
// pool's inventory is every parked pod the kubelet calls Ready, so a resumed pod
// wearing the parked label is claimable from the instant it goes Ready until
// ProgramNetwork gets around to relabelling it — a window in which another
// tenant's spawn can be handed a body that already holds this customer's disk.
// ProgramNetwork still runs after this and simply finds nothing to change.
//
// The name label is written at creation rather than left to the Bind that
// follows, for the same class of reason: a box's published Service selects on
// it, so every second the rebuilt pod goes without it is a second the box's
// public hostname answers 503 with no endpoint behind it — and a resume that
// dies before Bind leaves it that way for good.
func (c *ClusterRuntime) Resume(ctx context.Context, inst *Instance, spec Spec) error {
	boxID := clusterBoxIDFromPodName(inst.InstanceRef)
	if boxID == "" {
		return fmt.Errorf("cannot resume box: %q is not a box pod name", inst.InstanceRef)
	}
	image := spec.Image
	if image == "" {
		image = inst.Image
	}
	img, ok := boxcatalog.LookupImage(image)
	if !ok {
		return fmt.Errorf("unknown warm image %q", image)
	}
	region := spec.Region
	if region == "" {
		region = inst.Region
	}
	pod := c.BuildPod(boxID, img, boxcatalog.DefaultSize(), region)
	pod.Labels[labelBoxPhase] = phaseLive
	if boxName := spec.Env["BOX_NAME"]; boxName != "" {
		pod.Labels[labelBoxName] = boxName
	}
	if _, err := c.clientset.CoreV1().Pods(c.Namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("recreate box pod: %w", err)
	}
	inst.InstanceRef = pod.Name
	return c.waitReady(ctx, inst)
}

// clusterBoxIDFromPodName recovers the runtime's box id from a pod name, which is
// the only place it survives a round trip through the database.
func clusterBoxIDFromPodName(podName string) string {
	if !strings.HasPrefix(podName, "box-") {
		return ""
	}
	return strings.TrimPrefix(podName, "box-")
}

// deleteClaim removes a box's workspace PVC by name, tolerating an already-gone claim.
func (c *ClusterRuntime) deleteClaim(ctx context.Context, claim string) error {
	err := c.clientset.CoreV1().PersistentVolumeClaims(c.Namespace).Delete(ctx, claim, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete workspace claim: %w", err)
	}
	return nil
}

// ReapOrphans deletes every box pod in Namespace that has sat not-Ready for
// longer than clusterOrphanAfter, along with its workspace PVC.
//
// This is the backstop for the gap createParked's own cleanup cannot close on
// its own: that cleanup runs Destroy after waitReady fails, but it only runs if
// the goroutine that called createParked is still alive to see the failure. A
// console backend restart — whatever triggers it, including its own
// startupProbe deadline — kills that goroutine first, leaving the half-created
// pod and PVC behind with nothing left to notice. ReapOrphans finds them from
// the outside: it does not care which process created a pod or whether that
// process still exists, only whether the pod has been sitting there, not
// accepting traffic, longer than a healthy create path ever takes.
//
// A pod that is genuinely mid-create by a live createParked call is not at
// risk here: clusterOrphanAfter equals that call's own readyTimeout, so by the
// time this sweep would consider a pod orphaned, the create path that made it
// has already given up and destroyed it itself. The two never race for a pod
// that is actually healthy.
func (c *ClusterRuntime) ReapOrphans(ctx context.Context) (int, error) {
	pods, err := c.clientset.CoreV1().Pods(c.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelBox + "=true",
	})
	if err != nil {
		return 0, fmt.Errorf("list box pods: %w", err)
	}
	now := c.clock.Now()
	reaped := 0
	var firstErr error
	for i := range pods.Items {
		pod := pods.Items[i]
		if !clusterOrphaned(&pod, now) {
			continue
		}
		boxID := pod.Labels[labelBoxID]
		if boxID == "" {
			continue
		}
		inst := &Instance{ID: boxID, InstanceRef: pod.Name}
		if err := c.Destroy(ctx, inst); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		reaped++
	}
	return reaped, firstErr
}

// clusterOrphaned reports whether a box pod has been abandoned, in either of the
// two ways a pod can be.
//
// The first is a create that never finished: the pod is not Ready and has not
// been since it was made. The second is a claim that never finished: the pod is
// Ready, it left the parked set, and it never arrived at live. Both are pods no
// live goroutine will ever come back for, and both hold a slot of the fleet
// quota — which is the whole cost, since a namespace full of abandoned pods
// answers a paying customer's create with pool_exhausted.
//
// A claimed pod is judged by its stamp whether or not it is Ready, because a
// cold start is born claimed: it never passes through the parked set, so a cold
// start whose process died before the pod came up would otherwise be a pod no
// rule here can ever see. Its disk is empty by construction — nothing has been
// handed to a tenant until ProgramNetwork moves it to live — so the reap is free
// of the data risk the paragraph below is about.
//
// A pod whose claim is not yet stamped is left alone. The stamp is written by the
// same Update that makes the claim, so its absence means the pod predates this
// code, and guessing an age for it would reap boxes that are merely old.
//
// ONLY A PARKED POD COUNTS AS A FAILED CREATE, and that restriction is what keeps
// this sweep from destroying data. Destroy takes the workspace PVC with the pod,
// which is right for a warm slot whose disk is empty and catastrophic for a box a
// customer is using: a live box whose pod is merely slow — a node under pressure,
// a volume waiting to attach, a scheduler with nowhere to put it — would come
// back with its work erased. That is not hypothetical. It is how box
// m2-boxup-0801c lost its workspace on 2026-08-01, when Resume still rebuilt
// customer pods wearing the parked label and this sweep could not tell one from
// an abandoned warm create. A stuck live box leaks a quota slot until something
// else reclaims it, which is the cheaper of the two mistakes by a wide margin.
func clusterOrphaned(pod *corev1.Pod, now time.Time) bool {
	phase := pod.Labels[labelBoxPhase]
	if phase == phaseParked {
		return !clusterPodReady(pod) && pod.CreationTimestamp.Time.Before(now.Add(-clusterOrphanAfter))
	}
	if phase != phaseClaimed {
		return false
	}
	claimedAt, err := time.Parse(time.RFC3339, pod.Annotations[annBoxClaimedAt])
	if err != nil {
		return false
	}
	return claimedAt.Before(now.Add(-clusterClaimOrphanAfter))
}

// BrokerConfigured reports true: the broker is baked into the warm image and
// started by box-init as PID 1, so a cluster box always has a door of its own.
// The local adapter has to answer this honestly because its broker is a
// bind-mounted binary that may be absent; here its absence means the image is
// wrong, which surfaces as a pod that never becomes ready.
func (c *ClusterRuntime) BrokerConfigured() bool { return true }

// InstallSessionDigests makes the box's credential file match the live session
// set. It is a MIRROR, not a log: the file is rewritten from the given digests,
// so a revoked session disappears on the next install rather than lingering until
// something prunes it. The write is atomic because the broker re-reads the file
// per request and must never observe a half-written one.
func (c *ClusterRuntime) InstallSessionDigests(ctx context.Context, inst *Instance, digests []SessionDigest) error {
	var body strings.Builder
	for _, d := range digests {
		hash := strings.TrimSpace(d.Hash)
		if hash == "" {
			continue
		}
		if !isSHA256Hex(hash) {
			return fmt.Errorf("box: refusing to install %q as a session digest: not a sha256 hex digest", hash)
		}
		if d.ExpiresAt.IsZero() {
			return fmt.Errorf("box: refusing to install session digest %s… with no expiry: a credential the box cannot time out on its own is a standing credential", hash[:8])
		}
		fmt.Fprintf(&body, "%s %d\n", hash, d.ExpiresAt.Unix())
	}
	cmd := fmt.Sprintf("umask 077 && mkdir -p %s && chmod 0700 %s && cat > %s.tmp && chmod 0600 %s.tmp && mv %s.tmp %s",
		clusterBrokerDir, clusterBrokerDir, clusterTokensPath, clusterTokensPath, clusterTokensPath, clusterTokensPath)
	res, err := c.execWithStdin(ctx, inst, cmd, bytes.NewBufferString(body.String()))
	if err != nil {
		return fmt.Errorf("install session digests: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("install session digests: exit %d: %s", res.ExitCode, strings.TrimSpace(res.Stdout))
	}
	return nil
}

// RevokeAllSessionDigests empties the box's credential file, which is how a
// revocation lands: the broker re-reads the file per request, so the next call
// with an old token is refused without restarting anything.
func (c *ClusterRuntime) RevokeAllSessionDigests(ctx context.Context, inst *Instance) error {
	return c.InstallSessionDigests(ctx, inst, nil)
}

// StartBroker reports where the box's door answers. Unlike the local adapter this
// starts nothing: box-init already runs the broker as PID 1's child, because a
// pod whose door is not up fails its startup probe and never joins the pool.
func (c *ClusterRuntime) StartBroker(ctx context.Context, inst *Instance, boxName string) (string, error) {
	if inst.SSHHost == "" {
		if err := c.waitReady(ctx, inst); err != nil {
			return "", err
		}
	}
	if inst.SSHHost == "" {
		return "", fmt.Errorf("box %s has no address yet", inst.ID)
	}
	return fmt.Sprintf("%s:%d", inst.SSHHost, c.BrokerPort), nil
}

// validEnvKey keeps a tenant-supplied name from becoming shell. The value is
// quoted, so the key is the only place a newline or a quote could break out of
// the printf.
func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

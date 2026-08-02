package box

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/metrics"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
)

// Cluster-side crystallization: promoting an ephemeral box into a permanent,
// always-on workload in the same cluster (ADR-019 mechanism, cluster adapter).
//
// What is REAL here and what stands in is printed in the report rather than left
// for a reader to infer:
//
//   - The target is a Deployment, not a Beget VM. Booting a VM needs a hypervisor
//     credential the control plane deliberately does not hold, so the permanent
//     artifact this adapter can honestly produce is a restart-surviving workload
//     with its own disk, its own address and its own certificate.
//   - The base userland is NOT transferred: both sides run the SAME digest-pinned
//     image, so everything the image ships is identical by construction. Only the
//     delta - the files the box added or changed - travels, and every one of them
//     is verified by sha256 on the far side. Transferring three gigabytes of
//     unchanged Ubuntu to prove it is still Ubuntu would make crystallization cost
//     minutes to prove nothing.
//   - No freeze. The box's netns is its own, so the promoted copy binds the same
//     ports without a conflict and the customer's box keeps serving while the
//     promotion runs. The capture is therefore crash-consistent, which the report
//     states.
type ClusterCrystallizer struct {
	shell     podShell
	clientset kubernetes.Interface
	clock     Clock

	Namespace    string
	HostnameBase string
	TLSSecret    string
	IngressClass string
	StorageClass string
	PullSecret   string
	DiskGB       int
	ReadyTimeout time.Duration
	SeedTimeout  time.Duration
}

// podShell is the one thing this file needs from a runtime: a shell inside a
// named pod, with streams. Kept as an interface so the whole mechanism is
// testable against a fake shell and a fake clientset instead of a live cluster.
type podShell interface {
	execStream(ctx context.Context, podName, container, cmd string, stdin io.Reader, stdout, stderr io.Writer) error
}

var _ podShell = (*ClusterRuntime)(nil)

const (
	labelCrystal      = "dada.io/crystal"
	crystalContainer  = "app"
	crystalMountPath  = "/crystal"
	crystalRootDelta  = crystalMountPath + "/rootdelta"
	crystalWorkTree   = crystalMountPath + "/work"
	crystalCopyLog    = crystalMountPath + "/rootdelta-copy.err"
	crystalSeeded     = crystalMountPath + "/.seeded"
	crystalSeedGlob   = crystalSeeded + "-*"
	crystalDefaultGB  = 10
	crystalReadyPoll  = time.Second
	crystalDefaultTMO = 5 * time.Minute
)

// The public probe waits for the edge to program a brand-new hostname and its
// certificate, which is seconds to tens of seconds after the ingress object
// exists. Measured in production: a first crystallization reported the address
// lost with the controller's default certificate on an artifact that answered 200
// well inside the following minute.
const (
	crystalPublicProbeBudget   = 90 * time.Second
	crystalPublicProbeInterval = 2 * time.Second
)

// crystalSlash keeps scheme prefixes and shell globs out of the source as literal
// character pairs that a comment scanner would misread.
const crystalSlash = "/"

var (
	schemeHTTP   = "http:" + crystalSlash + crystalSlash
	schemeHTTPS  = "https:" + crystalSlash + crystalSlash
	servicesGlob = "/etc/dada/services/" + "*.json"
)

// ClusterBoxEnvPath is where a pod-backed box keeps its injected environment.
//
// It is NOT BoxEnvPath: the local runtime writes etc/dada/box.env into a rootfs
// it owns, while ClusterRuntime.Bind writes etc/dada/env into a live container.
// Reading the wrong one is silent — the file simply is not there — so the crystal
// used to start with an empty environment and the env comparison used to compare
// nothing against nothing and call it equal.
const ClusterBoxEnvPath = "etc/dada/env"

// crystalDeltaRoots are the trees a box's work can land in. Everything outside
// them belongs to the image, which both sides share by digest.
var crystalDeltaRoots = []string{"/srv", "/root", "/home", "/opt", "/usr/local", "/etc", "/var/lib", "/var/www", "/workspace"}

// crystalPrunedPaths are ADR-019's machine-owned files that fall inside the delta
// roots and must not be carried: they are the target's identity, not the box's.
var crystalPrunedPaths = []string{"/etc/fstab", "/etc/machine-id", "/etc/hostname", "/etc/resolv.conf", "/etc/hosts"}

// NewClusterCrystallizer builds a crystallizer sharing the runtime's API client,
// so both speak to the cluster as the same service account and an RBAC gap shows
// up in one place rather than two.
func NewClusterCrystallizer(rt *ClusterRuntime, hostnameBase string) *ClusterCrystallizer {
	return &ClusterCrystallizer{
		shell:        rt,
		clientset:    rt.clientset,
		clock:        rt.clock,
		Namespace:    rt.Namespace,
		HostnameBase: hostnameBase,
		TLSSecret:    "box-wildcard-tls",
		IngressClass: "nginx",
		StorageClass: rt.StorageClass,
		PullSecret:   rt.PullSecret,
		DiskGB:       crystalDefaultGB,
	}
}

var _ Crystallizer = (*ClusterCrystallizer)(nil)

// Crystallize satisfies the Crystallizer seam.
func (c *ClusterCrystallizer) Crystallize(ctx context.Context, inst *Instance, domain string) (CarryManifest, error) {
	rep, err := c.CrystallizeWithReport(ctx, inst, CrystallizeOptions{Domain: domain})
	if rep == nil {
		return nil, err
	}
	return rep.Carry, err
}

func (c *ClusterCrystallizer) now() time.Time {
	if c.clock == nil {
		return time.Now()
	}
	return c.clock.Now()
}

func (c *ClusterCrystallizer) readyTimeout() time.Duration {
	if c.ReadyTimeout > 0 {
		return c.ReadyTimeout
	}
	return crystalDefaultTMO
}

func (c *ClusterCrystallizer) seedTimeout() time.Duration {
	if c.SeedTimeout > 0 {
		return c.SeedTimeout
	}
	return crystalDefaultTMO
}

// CrystallizeWithReport runs the whole mechanism and returns the verification
// report whether it passed or failed. A failed crystallization with no report is
// an outage nobody can diagnose.
//
// The order is: read what the box runs, manifest its delta against a pristine pod
// of the same image, create the permanent body, stream the delta into it, publish
// the address, then verify - file manifest, listening-socket set, env digests and
// two HTTP probes.
func (c *ClusterCrystallizer) CrystallizeWithReport(ctx context.Context, inst *Instance, opts CrystallizeOptions) (*CrystallizationReport, error) {
	started := c.now()
	if opts.VMName == "" {
		opts.VMName = inst.InstanceRef + "-vm"
	}
	if opts.ProbePath == "" {
		opts.ProbePath = "/"
	}
	if opts.Domain == "" {
		opts.Domain = opts.VMName + "." + c.HostnameBase
	}

	rep := &CrystallizationReport{
		BoxID:          inst.ID,
		InstanceRef:    inst.InstanceRef,
		VMName:         opts.VMName,
		VMRoot:         "deployment/" + crystalName(opts.VMName) + " in " + c.Namespace,
		OSSlug:         opts.OSSlug,
		Domain:         opts.Domain,
		Stage:          "capture",
		ADRExclusions:  ADRUserlandExclusions,
		Carry:          CarryManifest{},
		VMOwnArtifacts: map[string]bool{},
		BoxSentinels:   map[string]bool{},
		StandIn: []string{
			"The permanent artifact is a Deployment with its own PersistentVolumeClaim in this " +
				"cluster, not a Beget instance: booting one needs a hypervisor credential the " +
				"control plane does not hold. It survives restarts, reschedules and node loss, " +
				"which is what the promise 'the same object keeps living' actually requires.",
			"The base userland is not transferred. Both sides run the same digest-pinned image, " +
				"so it is identical by construction; only the files the box added or changed are " +
				"carried, and each of those is verified by sha256 on the far side.",
			"systemd is not present. The rendered unit is recorded in the report, and its " +
				"ExecStart is what the crystallized container runs as its main process.",
			"There is no freeze: the promoted copy gets its own network namespace, so the box " +
				"keeps serving throughout and the capture is crash-consistent rather than quiesced.",
		},
	}
	rep.AdapterExclusions = append([]string(nil), crystalPrunedPaths...)

	fail := func(stage string, err error) (*CrystallizationReport, error) {
		rep.Stage = stage
		rep.DurationMS = c.now().Sub(started).Milliseconds()
		metrics.RecordBoxCrystallization("failed", stage, c.now().Sub(started))
		return rep, err
	}

	descs, err := c.services(ctx, inst.InstanceRef)
	if err != nil {
		return fail("capture", err)
	}
	if len(descs) == 0 {
		if opts.Command == "" || len(opts.Ports) == 0 {
			return fail("capture", errors.New(
				"crystallize: the box declares no service, so there is nothing to keep running: "+
					"write /etc/dada/services/<name>.json inside the box, or pass command and ports with the request"))
		}
		wd := opts.WorkingDir
		if wd == "" {
			wd = clusterWorkspacePath
		}
		descs = []ServiceDescriptor{{Name: "app", Command: opts.Command, WorkingDir: wd, Ports: opts.Ports}}
	}
	declared := declaredPorts(descs)
	if len(declared) == 0 {
		return fail("capture", errors.New("crystallize: no port is declared, and an artifact nobody can reach is not a promotion"))
	}
	rep.Sockets.DeclaredPorts = declared

	before, err := c.listeningPortsIn(ctx, inst.InstanceRef, clusterContainerName, declared)
	if err != nil {
		return fail("capture", err)
	}
	rep.Sockets.ListeningBeforeFreeze = before

	boxEnv, err := c.envSnapshot(ctx, inst.InstanceRef, clusterContainerName)
	if err != nil {
		return fail("capture", err)
	}

	image, err := c.podImage(ctx, inst.InstanceRef)
	if err != nil {
		return fail("capture", err)
	}

	boxManifest, err := c.manifest(ctx, inst.InstanceRef, clusterContainerName)
	if err != nil {
		return fail("capture", err)
	}
	baseManifest, baseFrom := c.baselineManifest(ctx, image)
	delta := deltaManifest(boxManifest, baseManifest)
	rep.BaselineSource = baseFrom
	rep.BaselineFiles = len(baseManifest)
	if len(delta) == 0 {
		return fail("capture", errors.New(
			"crystallize: the box is byte-identical to its base image, so there is nothing to promote"))
	}

	workDir := descs[0].WorkingDir
	if workDir == "" {
		workDir = clusterWorkspacePath
	}
	rootList, workList := splitDelta(delta, workDir)

	rep.Stage = "provision"
	pvcCreated, err := c.ensurePVC(ctx, opts.VMName)
	if err != nil {
		return fail("provision", err)
	}
	failProvision := func(err error) (*CrystallizationReport, error) {
		c.releaseProvisioned(ctx, opts.VMName, pvcCreated)
		return fail("provision", err)
	}
	marker := fmt.Sprintf("%s-%d", crystalSeeded, c.now().UnixNano())
	if err := c.ensureDeployment(ctx, opts.VMName, image, descs[0], workDir, marker); err != nil {
		return failProvision(err)
	}
	target, err := c.waitForCrystalPod(ctx, opts.VMName, marker)
	if err != nil {
		return failProvision(err)
	}

	rep.Stage = "seed"
	rep.RsyncCommand = fmt.Sprintf("tar -c --numeric-owner --no-recursion -T <%d paths> | tar -x -C %s   +   %d paths -> %s (working tree)",
		len(rootList), crystalRootDelta, len(workList), crystalWorkTree)
	seedCtx, cancelSeed := context.WithTimeout(ctx, c.seedTimeout())
	defer cancelSeed()
	skippedRoot, err := c.transfer(seedCtx, inst.InstanceRef, target, rootList, delta, crystalRootDelta, 0)
	rep.SkippedPaths = append(rep.SkippedPaths, skippedRoot...)
	if err != nil {
		return fail("seed", err)
	}
	if len(workList) > 0 {
		skippedWork, err := c.transfer(seedCtx, inst.InstanceRef, target, workList, delta, crystalWorkTree, pathDepth(workDir))
		rep.SkippedPaths = append(rep.SkippedPaths, skippedWork...)
		if err != nil {
			return fail("seed", err)
		}
	}
	if err := c.seedEnv(seedCtx, inst.InstanceRef, target); err != nil {
		return fail("seed", err)
	}
	if _, err := c.run(ctx, target, crystalContainer, "rm -f "+crystalSeeded+" "+crystalSeedGlob+"; touch "+marker); err != nil {
		return fail("seed", err)
	}

	if err := c.ensureService(ctx, opts.VMName, declared[0]); err != nil {
		return fail("provision", err)
	}
	if err := c.ensureIngress(ctx, opts.VMName, opts.Domain); err != nil {
		return fail("provision", err)
	}

	for _, d := range descs {
		unit := renderSystemdUnit(d, opts.Domain)
		rep.Units = append(rep.Units, UnitRender{
			Service: d.Name + ".service",
			Path:    "/etc/systemd/system/" + d.Name + ".service (recorded; this adapter runs the same ExecStart as the container's main process)",
			Content: unit,
		})
	}

	rep.Stage = "verify"
	after, err := c.waitListening(ctx, target, declared, c.readyTimeout())
	if err != nil {
		return fail("verify", err)
	}
	rep.Sockets.ListeningAfterCutover = after
	rep.Sockets.Equal = sameInts(declared, after)
	if rep.Sockets.Equal {
		rep.Carry["port"] = CarryPreserved
		rep.Carry["process"] = CarryRecreated
	} else {
		rep.Carry["port"] = CarryLost
		rep.Carry["process"] = CarryLost
	}

	targetManifest, err := c.manifest(ctx, target, crystalContainer, workDir)
	if err != nil {
		return fail("verify", err)
	}
	rep.Manifest = compareManifests(delta, targetManifest)
	if rep.Manifest.Equal {
		rep.Carry["volume"] = CarryPreserved
	} else {
		rep.Carry["volume"] = CarryLost
	}

	targetEnv, err := c.envSnapshot(ctx, target, crystalContainer)
	if err != nil {
		return fail("verify", err)
	}
	mode, _ := c.run(ctx, target, crystalContainer, "stat -c %a /"+ClusterBoxEnvPath+" 2>/dev/null")
	rep.Env = compareEnvMaps(boxEnv, targetEnv, normalizeMode(mode))
	if rep.Env.Equal {
		rep.Carry["env"] = CarryPreserved
	} else {
		rep.Carry["env"] = CarryLost
	}
	if hasAttachmentKeys(boxEnv) {
		rep.Carry["attachment"] = rep.Carry["env"]
	}

	if len(after) > 0 {
		rep.ProbeInternal = c.probeInside(ctx, target, after[0], opts.Domain, opts.ProbePath)
	}
	rep.Probe = awaitHTTPS(opts.Domain, opts.ProbePath, crystalPublicProbeBudget, crystalPublicProbeInterval)
	if rep.Probe.OK {
		rep.Carry["address"] = CarryRecreated
	} else {
		rep.Carry["address"] = CarryLost
	}

	for kind, disp := range rep.Carry {
		if disp == CarryLost {
			metrics.RecordBoxCrystallizeStateLoss(kind)
		}
	}

	rep.DurationMS = c.now().Sub(started).Milliseconds()
	verified := rep.Manifest.Equal && rep.Sockets.Equal && rep.Env.Equal && rep.ProbeInternal.OK
	if !verified {
		metrics.RecordBoxCrystallization("failed", "verify", c.now().Sub(started))
		return rep, fmt.Errorf("crystallize: verification failed (manifest_equal=%t sockets_equal=%t env_equal=%t probe_internal_ok=%t probe_public_ok=%t)",
			rep.Manifest.Equal, rep.Sockets.Equal, rep.Env.Equal, rep.ProbeInternal.OK, rep.Probe.OK)
	}
	rep.Stage = "none"
	metrics.RecordBoxCrystallization("success", "none", c.now().Sub(started))
	return rep, nil
}

// run executes a command in a pod and returns its combined output, failing on a
// non-zero exit so a silent misparse cannot be mistaken for an empty result.
func (c *ClusterCrystallizer) run(ctx context.Context, pod, container, cmd string) (string, error) {
	out := &syncBuffer{}
	if err := c.shell.execStream(ctx, pod, container, cmd, nil, out, out); err != nil {
		if code, ok := exitCodeFrom(err); ok {
			return out.String(), fmt.Errorf("crystallize: command in %s exited %d: %s", pod, code, strings.TrimSpace(out.String()))
		}
		return out.String(), fmt.Errorf("crystallize: exec in %s: %w", pod, err)
	}
	return out.String(), nil
}

// services reads the box's service descriptors from inside the box.
func (c *ClusterCrystallizer) services(ctx context.Context, pod string) ([]ServiceDescriptor, error) {
	script := `for f in ` + servicesGlob + `; do [ -f "$f" ] || continue; cat "$f"; echo; done`
	out, err := c.run(ctx, pod, clusterContainerName, script)
	if err != nil {
		return nil, err
	}
	var descs []ServiceDescriptor
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var d ServiceDescriptor
		if err := dec.Decode(&d); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("crystallize: malformed service descriptor in the box: %w", err)
		}
		descs = append(descs, d)
	}
	sort.Slice(descs, func(i, j int) bool { return descs[i].Name < descs[j].Name })
	return descs, nil
}

// listeningPortsScript reads the listening TCP set from the pod's OWN network
// namespace. On this adapter that is meaningful - a box pod has its own netns, so
// what /proc/net/tcp reports is the box and nothing else.
const listeningPortsScript = `cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | awk '$4=="0A"{split($2,a,":"); print a[2]}' | while read h; do printf '%d\n' "0x$h"; done | sort -un`

func (c *ClusterCrystallizer) listeningPortsIn(ctx context.Context, pod, container string, want []int) ([]int, error) {
	out, err := c.run(ctx, pod, container, listeningPortsScript)
	if err != nil {
		return nil, err
	}
	live := map[int]bool{}
	for _, line := range strings.Fields(out) {
		if p, err := strconv.Atoi(line); err == nil {
			live[p] = true
		}
	}
	var got []int
	for _, p := range want {
		if live[p] {
			got = append(got, p)
		}
	}
	sort.Ints(got)
	return got, nil
}

// waitListening polls the artifact's own netns until every declared port answers
// or the budget runs out. An artifact whose ports never come up must fail the
// crystallization rather than be reported as promoted.
func (c *ClusterCrystallizer) waitListening(ctx context.Context, pod string, want []int, within time.Duration) ([]int, error) {
	deadline := c.now().Add(within)
	var last []int
	for {
		got, err := c.listeningPortsIn(ctx, pod, crystalContainer, want)
		if err == nil {
			last = got
			if len(got) == len(want) {
				return got, nil
			}
		}
		if c.now().After(deadline) {
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(crystalReadyPoll):
		}
	}
}

// seedEnv carries the box's injected environment onto the permanent disk.
//
// It is a step of its own because the env file is PRUNED from the manifest — the
// promotion tightens its mode, so comparing it as a file would mismatch by
// construction — and pruning it from the manifest also takes it out of the delta
// that the transfer is built from. Without this the artifact boots with no
// environment at all: the process comes up, answers on its port, and every value
// the customer injected is simply absent, which verification reports as a lost
// environment and a customer discovers as an application talking to nothing.
//
// The content moves through stdin rather than a command line, because these are
// credentials and an argv is visible in a node's process list. It lands in the
// staging tree rather than at its final path so the artifact's own start applies
// it with everything else, and it is written 0600 before the copy so it is never
// group-readable, not even for the seconds between the write and the start.
func (c *ClusterCrystallizer) seedEnv(ctx context.Context, boxPod, targetPod string) error {
	out := &syncBuffer{}
	if err := c.shell.execStream(ctx, boxPod, clusterContainerName,
		"cat /"+ClusterBoxEnvPath+" 2>/dev/null || true", nil, out, io.Discard); err != nil {
		if _, ok := exitCodeFrom(err); !ok {
			return fmt.Errorf("crystallize: read the box environment: %w", err)
		}
	}
	blob := out.String()
	if strings.TrimSpace(blob) == "" {
		return nil
	}
	dest := crystalRootDelta + "/" + ClusterBoxEnvPath
	script := "mkdir -p " + path.Dir(dest) + " && cat > " + dest + " && chmod 600 " + dest
	stderr := &bytes.Buffer{}
	if err := c.shell.execStream(ctx, targetPod, crystalContainer, script,
		strings.NewReader(blob), io.Discard, stderr); err != nil {
		return fmt.Errorf("crystallize: write the environment onto the permanent disk: %w: %s", err, stderr.String())
	}
	return nil
}

// envSnapshot reads the box env file from inside a pod.
func (c *ClusterCrystallizer) envSnapshot(ctx context.Context, pod, container string) (map[string]string, error) {
	out, err := c.run(ctx, pod, container, "cat /"+ClusterBoxEnvPath+" 2>/dev/null || true")
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			env[strings.TrimSpace(k)] = shellUnquote(strings.TrimSpace(v))
		}
	}
	return env, nil
}

func (c *ClusterCrystallizer) podImage(ctx context.Context, pod string) (string, error) {
	p, err := c.clientset.CoreV1().Pods(c.Namespace).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("crystallize: read box pod: %w", err)
	}
	for _, ct := range p.Spec.Containers {
		if ct.Name == clusterContainerName {
			return ct.Image, nil
		}
	}
	return "", fmt.Errorf("crystallize: box pod %s has no %s container", pod, clusterContainerName)
}

// manifestScript emits the (type, mode, size, path) tuple of every entry under
// the delta roots, then the sha256 of every regular file, then every symlink's
// target. Three passes rather than a per-file shell loop: forking stat and
// sha256sum once per file turns a manifest of a hundred thousand files into
// minutes of exec.
// The scan stays -xdev so a stray mount cannot drag an unrelated filesystem into
// the comparison, and extraRoots is how the one mount that MUST be scanned gets
// in. On the artifact the working tree is a subPath mount inside the workload's
// own root, so a walk that starts at /workspace stops at the mount boundary and
// reports every file the user actually wrote as missing. Starting a walk at the
// mount point itself sees it, because -xdev bounds crossings, not roots.
//
// The env file is left out on purpose: the promotion tightens its mode to 0600,
// so a box that kept it at 0644 would mismatch by construction. It is not skipped
// verification — it gets a stricter one of its own, per key by sha256 plus the
// mode the ADR demands.
func manifestScript(extraRoots ...string) string {
	var prune []string
	for _, p := range crystalPrunedPaths {
		prune = append(prune, "-path "+p)
	}
	for _, p := range []string{"/" + ClusterBoxEnvPath, "/" + BoxEnvPath} {
		prune = append(prune, "-path "+p)
	}
	pruneExpr := strings.Join(prune, " -o ")
	roots := strings.Join(append(append([]string(nil), crystalDeltaRoots...), extraRoots...), " ")
	return fmt.Sprintf(`ROOTS="%s"
for r in $ROOTS; do [ -e "$r" ] || continue; find "$r" -xdev \( %s \) -prune -o \( -type f -o -type l -o -type d \) -printf 'M|%%y|%%m|%%s|%%p\n'; done
for r in $ROOTS; do [ -e "$r" ] || continue; find "$r" -xdev \( %s \) -prune -o -type f -print0; done | xargs -0 -r -n 128 sha256sum 2>/dev/null | sed 's/^/H|/'
for r in $ROOTS; do [ -e "$r" ] || continue; find "$r" -xdev \( %s \) -prune -o -type l -printf 'L|%%p|%%l\n'; done`,
		roots, pruneExpr, pruneExpr, pruneExpr)
}

func (c *ClusterCrystallizer) manifest(ctx context.Context, pod, container string, extraRoots ...string) (map[string]FileEntry, error) {
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	if err := c.shell.execStream(ctx, pod, container, manifestScript(extraRoots...), nil, out, errBuf); err != nil {
		if _, ok := exitCodeFrom(err); !ok {
			return nil, fmt.Errorf("crystallize: manifest in %s: %w", pod, err)
		}
	}
	return parseManifest(out.String()), nil
}

// parseManifest folds the three passes into one map keyed by absolute path.
func parseManifest(out string) map[string]FileEntry {
	entries := map[string]FileEntry{}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'M':
			parts := strings.SplitN(line, "|", 5)
			if len(parts) != 5 {
				continue
			}
			size, _ := strconv.ParseInt(parts[3], 10, 64)
			e := FileEntry{Path: parts[4], Mode: normalizeMode(parts[2]), Size: size}
			switch parts[1] {
			case "d":
				e.SHA256 = "dir"
				e.Size = 0
			case "l":
				e.SHA256 = "symlink:pending"
			}
			entries[parts[4]] = e
		case 'H':
			rest := strings.TrimPrefix(line, "H|")
			sum, path, ok := strings.Cut(rest, "  ")
			if !ok {
				continue
			}
			if e, exists := entries[path]; exists {
				e.SHA256 = sum
				entries[path] = e
			}
		case 'L':
			parts := strings.SplitN(line, "|", 3)
			if len(parts) != 3 {
				continue
			}
			if e, exists := entries[parts[1]]; exists {
				sum := sha256.Sum256([]byte("symlink:" + parts[2]))
				e.SHA256 = "symlink:" + hex.EncodeToString(sum[:])
				e.Size = int64(len(parts[2]))
				entries[parts[1]] = e
			}
		}
	}
	return entries
}

// normalizeMode renders a find or stat octal mode the way FileEntry records one,
// so "644" and "0644" cannot compare unequal for spelling.
func normalizeMode(m string) string {
	m = strings.TrimSpace(m)
	if m == "" {
		return ""
	}
	v, err := strconv.ParseUint(m, 8, 32)
	if err != nil {
		return m
	}
	return fmt.Sprintf("%04o", v)
}

// baselineManifest is the manifest of a PRISTINE pod of the same image, which is
// what makes the delta a delta. A parked warm pod is exactly that: created from
// the image, never claimed, never written to.
//
// When no parked pod of that image exists the baseline is empty and the whole
// delta-root content is carried. That is correct, only bigger - and the report
// says which of the two happened, because a reader must be able to tell a small
// transfer from a fallback.
func (c *ClusterCrystallizer) baselineManifest(ctx context.Context, image string) (map[string]FileEntry, string) {
	pods, err := c.clientset.CoreV1().Pods(c.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelBoxPhase + "=" + phaseParked,
	})
	if err != nil {
		return map[string]FileEntry{}, "none (listing parked pods failed: " + err.Error() + ")"
	}
	for _, p := range pods.Items {
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		for _, ct := range p.Spec.Containers {
			if ct.Name != clusterContainerName || ct.Image != image {
				continue
			}
			m, err := c.manifest(ctx, p.Name, clusterContainerName)
			if err != nil || len(m) == 0 {
				continue
			}
			return m, "parked pod " + p.Name + " of the same image"
		}
	}
	return map[string]FileEntry{}, "none: no parked pod of this image was available, so the whole delta-root content was carried"
}

// deltaManifest is what the box added or changed on top of its image.
func deltaManifest(boxTree, base map[string]FileEntry) map[string]FileEntry {
	out := map[string]FileEntry{}
	for path, e := range boxTree {
		b, ok := base[path]
		if ok && b.Size == e.Size && b.Mode == e.Mode && b.SHA256 == e.SHA256 {
			continue
		}
		out[path] = e
	}
	return out
}

// splitDelta separates the working tree from the rest, because the two land in
// different places on the permanent disk: the working tree is MOUNTED at the
// application's working directory and keeps the process's later writes, while the
// rest is re-applied over the image's root at every start.
func splitDelta(delta map[string]FileEntry, workDir string) (root, work []string) {
	prefix := strings.TrimSuffix(workDir, "/") + "/"
	for path := range delta {
		if path == strings.TrimSuffix(workDir, "/") || strings.HasPrefix(path, prefix) {
			work = append(work, path)
			continue
		}
		root = append(root, path)
	}
	sort.Strings(root)
	sort.Strings(work)
	return root, work
}

func pathDepth(p string) int {
	return len(strings.Split(strings.Trim(p, "/"), "/"))
}

// transfer streams a tar of the listed paths out of the box and into the
// permanent disk, without staging it in the control plane. It returns the paths
// the box refused to hand over.
//
// The path list goes in through stdin rather than a command line: an argv of a
// hundred thousand paths does not fit in one, and a file list also keeps paths
// with spaces intact.
//
// A box container runs as uid 0 with every capability dropped, so root there is
// root in name only: it cannot open a directory owned by a service user, and a
// box that installed redis or postgres has several. Without --ignore-failed-read
// the first such directory ends the archive with exit 2 and the whole promotion
// dies at the seed stage - which is to say crystallization would be impossible
// for any box that runs software as a non-root user, i.e. for almost all of them.
// The flag turns those into warnings; the warnings are parsed, not discarded, and
// travel into the report, because carrying less than the box holds is exactly the
// kind of loss ADR-019 requires to be loud rather than convenient.
func (c *ClusterCrystallizer) transfer(ctx context.Context, boxPod, targetPod string, paths []string, entries map[string]FileEntry, dest string, strip int) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	listPath := fmt.Sprintf("/tmp/crystal-%d-%d.list", len(paths), strip)
	list := bytes.NewBufferString(strings.Join(paths, "\n") + "\n")
	if err := c.shell.execStream(ctx, boxPod, clusterContainerName, "cat > "+listPath, list, io.Discard, io.Discard); err != nil {
		return nil, fmt.Errorf("crystallize: write transfer list into the box: %w", err)
	}

	if _, err := c.run(ctx, targetPod, crystalContainer, "mkdir -p "+dest); err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	srcErr := make(chan error, 1)
	srcLog := &bytes.Buffer{}
	go func() {
		err := c.shell.execStream(ctx, boxPod, clusterContainerName,
			"tar -c --numeric-owner --no-recursion --ignore-failed-read -T "+listPath+" -f -", nil, pw, srcLog)
		if err != nil {
			err = fmt.Errorf("crystallize: tar out of the box: %w: %s", err, srcLog.String())
		}
		_ = pw.CloseWithError(err)
		srcErr <- err
	}()

	extract := fmt.Sprintf("tar -x --numeric-owner -f - -C %s", dest)
	if strip > 0 {
		extract += fmt.Sprintf(" --strip-components=%d", strip)
	}
	stderr := &bytes.Buffer{}
	dstErr := c.shell.execStream(ctx, targetPod, crystalContainer, extract, pr, io.Discard, stderr)
	_ = pr.Close()
	if err := <-srcErr; err != nil {
		return nil, err
	}
	if dstErr != nil {
		return nil, fmt.Errorf("crystallize: untar onto the permanent disk: %w: %s", dstErr, stderr.String())
	}

	skipped := parseTarSkips(srcLog.String())
	if err := c.recreateSkippedDirs(ctx, targetPod, skipped, entries, dest, strip); err != nil {
		return skipped, err
	}
	return skipped, nil
}

// tarSkipMarkers are the ways GNU tar says it could not read something it was
// told to archive.
var tarSkipMarkers = []string{": Cannot open:", ": Cannot read:", ": Cannot stat:", ": Cannot savedir:"}

// parseTarSkips pulls the paths out of tar's warnings. Anything it did not
// understand is left alone: a warning nobody can attribute to a path is still a
// warning, but it is not a path, and inventing one would put a lie in the report.
func parseTarSkips(log string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(log, "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "tar: ")
		for _, marker := range tarSkipMarkers {
			idx := strings.Index(line, marker)
			if idx <= 0 {
				continue
			}
			p := strings.TrimSuffix(line[:idx], ": Warning")
			if !strings.HasPrefix(p, "/") || seen[p] {
				break
			}
			seen[p] = true
			out = append(out, p)
			break
		}
	}
	sort.Strings(out)
	return out
}

// recreateSkippedDirs puts back the directories the archive could not open.
//
// A directory's whole content in the manifest is its mode - the walk records no
// checksum for one - so a directory tar refused to open can be reproduced exactly
// by making it and setting that mode. Files are a different matter: their content
// is genuinely lost, they stay in the skipped list, and verification will report
// them missing, which is the truth and should stay visible.
func (c *ClusterCrystallizer) recreateSkippedDirs(ctx context.Context, targetPod string, skipped []string, entries map[string]FileEntry, dest string, strip int) error {
	var cmds []string
	for _, p := range skipped {
		e, ok := entries[p]
		if !ok || e.SHA256 != "dir" {
			continue
		}
		target := stripComponents(p, strip)
		if target == "" {
			continue
		}
		full := strings.TrimSuffix(dest, "/") + "/" + target
		cmds = append(cmds, fmt.Sprintf("mkdir -p '%s' && chmod %s '%s'", full, e.Mode, full))
	}
	if len(cmds) == 0 {
		return nil
	}
	if _, err := c.run(ctx, targetPod, crystalContainer, strings.Join(cmds, "; ")); err != nil {
		return fmt.Errorf("crystallize: recreate directories the box would not open: %w", err)
	}
	return nil
}

// stripComponents applies tar's --strip-components to one path.
func stripComponents(p string, strip int) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if strip >= len(parts) {
		return ""
	}
	return strings.Join(parts[strip:], "/")
}

func crystalName(vmName string) string { return "crystal-" + vmName }

// ensurePVC creates the permanent disk and reports whether THIS call created it.
//
// The answer matters on the failure path: a promotion that dies before it seeds
// anything must give the disk back, and a disk that already existed belongs to an
// earlier, live artifact of the same name. Deleting that one would destroy the
// very thing the promotion promised keeps living.
func (c *ClusterCrystallizer) ensurePVC(ctx context.Context, vmName string) (bool, error) {
	gb := c.DiskGB
	if gb <= 0 {
		gb = crystalDefaultGB
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crystalName(vmName),
			Namespace: c.Namespace,
			Labels:    map[string]string{labelCrystal: vmName},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: ptr.To(c.StorageClass),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", gb))},
			},
		},
	}
	_, err := c.clientset.CoreV1().PersistentVolumeClaims(c.Namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("crystallize: create permanent disk: %w", err)
	}
	return true, nil
}

// releaseProvisioned gives back what a promotion reserved but never used.
//
// A promotion that never reached the seed stage carries no data: the disk it
// asked for was empty, and the workload it declared never ran. Leaving both
// behind is not caution, it is a leak -- on a storage pool that is already full,
// the abandoned disk keeps holding the space that would let the NEXT attempt
// succeed, and the usual cause of dying at this stage is exactly a full pool.
// Only a disk this attempt created is released; one that predates the attempt
// belongs to a live artifact and is never touched.
func (c *ClusterCrystallizer) releaseProvisioned(ctx context.Context, vmName string, pvcCreated bool) {
	if !pvcCreated {
		return
	}
	name := crystalName(vmName)
	if err := c.clientset.AppsV1().Deployments(c.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return
	}
	_ = c.clientset.CoreV1().PersistentVolumeClaims(c.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

// crystalCommand is what the promoted container runs.
//
// It waits for the seed marker before doing anything: the Deployment is created
// BEFORE the userland arrives, because the transfer needs a live pod to write
// into. Starting the application against a half-copied disk is the one way this
// mechanism could produce a corrupt artifact, and the marker is what makes that
// impossible rather than unlikely.
//
// The marker belongs to ONE attempt, and that is not a detail. The disk outlives
// an attempt: a second run against the same artifact finds the previous run's
// marker already there, starts the application against the old userland before a
// byte of the new one arrives, and — since a half-promoted artifact usually cannot
// start at all — spends the rest of the attempt in a restart loop. Every exec the
// seed stage needs then lands in a container that is dying, so the retry cannot
// finish, and the failure looks like a slow transfer instead of a marker from an
// hour ago. A per-attempt name makes a stale marker unmatchable by construction.
//
// Every step guards its own precondition instead of trusting a trailing `|| true`,
// because `.` on a missing file is FATAL in dash: the shell exits 2 before the
// `||` is ever evaluated. Paired with a blanket `2>/dev/null` that produced the
// worst diagnostic this mechanism can produce — a container that dies instantly,
// with an empty log and an exit code that names nothing.
//
// The copy is `cp -af` because the pod drops every capability: root here has no
// CAP_DAC_OVERRIDE, so a destination the image ships read-only (npm's cache writes
// its content files 0444) cannot be opened for writing at all. Without -f those
// paths silently fall out of the copy and the manifest comparison reports a lost
// volume for files whose content never actually differed.
//
// The env file is forced to 0600 on the way in. ADR-019 requires the promoted
// artifact to hold injected credentials at 0600 root, and verification enforces
// exactly that, so a file that arrived group-readable would be reported as a lost
// environment even though every value in it matched.
func crystalCommand(d ServiceDescriptor, workDir, marker string) string {
	var b strings.Builder
	b.WriteString("while [ ! -f " + marker + " ]; do sleep 1; done\n")
	b.WriteString("cp -af " + crystalRootDelta + "/. / 2>" + crystalCopyLog + " || echo \"crystal: часть файлов не скопировалась, подробности в " + crystalCopyLog + "\" >&2\n")
	b.WriteString("set -a\n")
	for _, p := range []string{ClusterBoxEnvPath, BoxEnvPath} {
		b.WriteString("[ -f /" + p + " ] && chmod 600 /" + p + "\n")
		b.WriteString("[ -r /" + p + " ] && . /" + p + "\n")
	}
	b.WriteString("set +a\n")
	b.WriteString("cd " + shellQuote(workDir) + " || { echo \"crystal: нет рабочего каталога \"" + shellQuote(workDir) + " >&2; exit 1; }\n")
	b.WriteString("exec /bin/sh -c " + shellQuote(d.Command) + "\n")
	return b.String()
}

func (c *ClusterCrystallizer) ensureDeployment(ctx context.Context, vmName, image string, d ServiceDescriptor, workDir, marker string) error {
	name := crystalName(vmName)
	labels := map[string]string{labelCrystal: vmName}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					HostUsers:                    ptr.To(false),
					AutomountServiceAccountToken: ptr.To(false),
					EnableServiceLinks:           ptr.To(false),
					ServiceAccountName:           "box-runner",
					SecurityContext: &corev1.PodSecurityContext{
						SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						FSGroup:        ptr.To(int64(0)),
					},
					Volumes: []corev1.Volume{{
						Name: "crystal",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: name},
						},
					}},
					Containers: []corev1.Container{{
						Name:            crystalContainer,
						Image:           image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Command:         []string{"/bin/sh", "-c", crystalCommand(d, workDir, marker)},
						SecurityContext: &corev1.SecurityContext{
							RunAsUser:                ptr.To(int64(0)),
							RunAsGroup:               ptr.To(int64(0)),
							AllowPrivilegeEscalation: ptr.To(false),
							Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"),
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1"),
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "crystal", MountPath: crystalMountPath},
							{Name: "crystal", MountPath: workDir, SubPath: "work"},
						},
					}},
				},
			},
		},
	}
	if c.PullSecret != "" {
		dep.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: c.PullSecret}}
	}
	_, err := c.clientset.AppsV1().Deployments(c.Namespace).Create(ctx, dep, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = c.clientset.AppsV1().Deployments(c.Namespace).Update(ctx, dep, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("crystallize: create the permanent workload: %w", err)
	}
	return nil
}

// waitForCrystalPod returns the pod THIS attempt created, never merely a pod that
// carries the artifact's label.
//
// A second promotion of the same box updates the Deployment, and the Recreate
// strategy then deletes the previous pod. A wait that accepted any labelled
// running pod would hand back the doomed one: the seed and verify stages exec
// into a pod that k8s is already tearing down, and the run dies with "pods ...
// not found" — an error that names the symptom and hides the race entirely.
// The per-attempt marker is already unique per run, so the pod's own command is
// the identity check, and it needs no ReplicaSet bookkeeping to be exact.
func (c *ClusterCrystallizer) waitForCrystalPod(ctx context.Context, vmName, marker string) (string, error) {
	deadline := c.now().Add(c.readyTimeout())
	for {
		pods, err := c.clientset.CoreV1().Pods(c.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelCrystal + "=" + vmName,
		})
		if err != nil {
			return "", fmt.Errorf("crystallize: list permanent pods: %w", err)
		}
		for _, p := range pods.Items {
			if !podRunsAttempt(p, marker) {
				continue
			}
			if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil && crystalContainerRunning(p) {
				return p.Name, nil
			}
		}
		if c.now().After(deadline) {
			return "", fmt.Errorf("crystallize: the permanent workload did not start within %s%s", c.readyTimeout(), c.whyNotStarted(ctx, vmName, marker))
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(crystalReadyPoll):
		}
	}
}

// whyNotStarted turns a bare timeout into a diagnosis.
//
// "the permanent workload did not start within 5m" says nothing a caller can act
// on, and the cause is usually not the promotion at all: a full Longhorn pool
// answers "no available disk for replica", and the pod then sits in
// ContainerCreating until something frees space. The reason k8s already recorded
// is right there in the pod's waiting state and in the events on its object, so
// the timeout carries it instead of hiding it. Best effort by construction: a
// diagnosis that cannot be read must not replace the timeout it decorates.
func (c *ClusterCrystallizer) whyNotStarted(ctx context.Context, vmName, marker string) string {
	pods, err := c.clientset.CoreV1().Pods(c.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelCrystal + "=" + vmName,
	})
	if err != nil {
		return ""
	}
	for _, p := range pods.Items {
		if !podRunsAttempt(p, marker) {
			continue
		}
		for _, cs := range p.Status.ContainerStatuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Message != "" {
				return fmt.Sprintf(" (%s: %s)", cs.State.Waiting.Reason, cs.State.Waiting.Message)
			}
		}
		events, err := c.clientset.CoreV1().Events(c.Namespace).List(ctx, metav1.ListOptions{
			FieldSelector: "involvedObject.name=" + p.Name,
		})
		if err != nil {
			return ""
		}
		for i := len(events.Items) - 1; i >= 0; i-- {
			e := events.Items[i]
			if e.Type == corev1.EventTypeWarning && e.Message != "" {
				return fmt.Sprintf(" (%s: %s)", e.Reason, e.Message)
			}
		}
		if len(p.Status.ContainerStatuses) == 0 {
			return fmt.Sprintf(" (pod %s is %s)", p.Name, p.Status.Phase)
		}
	}
	return ""
}

// podRunsAttempt reports whether a pod was created from the template this attempt
// wrote, judged by the per-attempt seed marker baked into its command.
func podRunsAttempt(p corev1.Pod, marker string) bool {
	for _, ct := range p.Spec.Containers {
		if ct.Name != crystalContainer {
			continue
		}
		for _, arg := range ct.Command {
			if strings.Contains(arg, marker) {
				return true
			}
		}
	}
	return false
}

// crystalContainerRunning reports whether the container the seed stage will exec
// into is actually running.
//
// A pod's phase is Running while its only container sits in CrashLoopBackOff, so
// the phase alone would hand the seed stage a target that is between two deaths:
// every exec into it either fails or is killed halfway, and the transfer reads as
// mysteriously slow rather than as a workload that never started.
func crystalContainerRunning(p corev1.Pod) bool {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == crystalContainer {
			return cs.State.Running != nil
		}
	}
	return false
}

func (c *ClusterCrystallizer) ensureService(ctx context.Context, vmName string, port int) error {
	name := crystalName(vmName)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.Namespace, Labels: map[string]string{labelCrystal: vmName}},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{labelCrystal: vmName},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromInt32(int32(port))}},
		},
	}
	_, err := c.clientset.CoreV1().Services(c.Namespace).Create(ctx, svc, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		existing, getErr := c.clientset.CoreV1().Services(c.Namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return fmt.Errorf("crystallize: read the published service: %w", getErr)
		}
		existing.Spec.Selector = svc.Spec.Selector
		existing.Spec.Ports = svc.Spec.Ports
		_, err = c.clientset.CoreV1().Services(c.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("crystallize: publish the service: %w", err)
	}
	return nil
}

// ensureIngress puts the artifact on its address. TLS is attached only when the
// hostname is under the platform wildcard: pointing an Ingress at a certificate
// that does not cover it would serve a name mismatch, which is worse than plain
// HTTP because it looks secure and is not.
func (c *ClusterCrystallizer) ensureIngress(ctx context.Context, vmName, domain string) error {
	name := crystalName(vmName)
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.Namespace,
			Labels:    map[string]string{labelCrystal: vmName},
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/proxy-read-timeout": "3600",
				"nginx.ingress.kubernetes.io/proxy-body-size":    "64m",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To(c.IngressClass),
			Rules: []networkingv1.IngressRule{{
				Host: domain,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: ptr.To(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: name,
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}
	if c.HostnameBase != "" && strings.HasSuffix(domain, "."+strings.TrimPrefix(c.HostnameBase, ".")) {
		ing.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{domain}, SecretName: c.TLSSecret}}
	}
	_, err := c.clientset.NetworkingV1().Ingresses(c.Namespace).Create(ctx, ing, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_, err = c.clientset.NetworkingV1().Ingresses(c.Namespace).Update(ctx, ing, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("crystallize: publish the address: %w", err)
	}
	return nil
}

// compareEnvMaps compares two env maps by sha256 PER KEY, never by value: a
// verification report is written to logs and pasted into tickets, so a report
// carrying DATABASE_URL would be a credential leak caused by the safety check.
func compareEnvMaps(boxEnv, targetEnv map[string]string, mode string) EnvComparison {
	out := EnvComparison{BoxDigest: map[string]string{}, VMDigest: map[string]string{}, Mode: mode}
	for k := range boxEnv {
		out.Keys = append(out.Keys, k)
	}
	sort.Strings(out.Keys)
	for _, k := range out.Keys {
		bd := digest(boxEnv[k])
		out.BoxDigest[k] = bd
		v, ok := targetEnv[k]
		if !ok {
			out.Mismatched = append(out.Mismatched, k+" (absent on the artifact)")
			continue
		}
		out.VMDigest[k] = digest(v)
		if out.VMDigest[k] != bd {
			out.Mismatched = append(out.Mismatched, k+" (digest differs)")
		}
	}
	if len(out.Keys) == 0 {
		out.Equal = true
		return out
	}
	out.Equal = len(out.Mismatched) == 0 && mode == "0600"
	return out
}

// probeInside asks the artifact itself, from inside its own network namespace,
// with the public Host header. It is the check that cannot be satisfied by DNS or
// by the ingress: something in THAT pod answered.
func (c *ClusterCrystallizer) probeInside(ctx context.Context, pod string, port int, host, path string) HTTPProbeResult {
	target := fmt.Sprintf("%s127.0.0.1:%d%s", schemeHTTP, port, path)
	res := HTTPProbeResult{URL: target, Host: host}
	cmd := fmt.Sprintf(
		`(command -v curl >/dev/null && curl -s -o /dev/null -w '%%{http_code}' -H 'Host: %s' %s) || `+
			`(command -v wget >/dev/null && wget -q -S -O /dev/null --header='Host: %s' %s 2>&1 | awk '/HTTP\//{print $2}' | tail -1)`,
		host, target, host, target)
	out, err := c.run(ctx, pod, crystalContainer, cmd)
	if err != nil {
		res.Body = err.Error()
		return res
	}
	code, convErr := strconv.Atoi(strings.TrimSpace(out))
	if convErr != nil {
		res.Body = strings.TrimSpace(out)
		return res
	}
	res.Status = code
	res.OK = code >= 200 && code < 400
	return res
}

// awaitHTTPS probes the public address until it answers or the budget runs out.
//
// A single shot measures the edge's programming delay rather than the artifact:
// the ingress and its certificate land seconds after the object is created, so the
// first request gets the controller's default certificate and the report says the
// address was LOST on an artifact that answers 200 half a minute later. The budget
// is how long a customer would wait for their own new address.
func awaitHTTPS(domain, path string, budget, interval time.Duration) HTTPProbeResult {
	deadline := time.Now().Add(budget)
	for {
		res := probeHTTPS(domain, path)
		if res.OK || !time.Now().Add(interval).Before(deadline) {
			return res
		}
		time.Sleep(interval)
	}
}

// probeHTTPS is the end-to-end request a customer would make: the public name,
// over TLS, through the same ingress everyone else reaches.
func probeHTTPS(domain, path string) HTTPProbeResult {
	url := schemeHTTPS + domain + path
	res := HTTPProbeResult{URL: url, Host: domain}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		res.Body = err.Error()
		return res
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		res.Body = err.Error()
		return res
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	res.Status = resp.StatusCode
	res.Body = strings.TrimSpace(string(body))
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 400
	return res
}

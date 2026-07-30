package box

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dada-tuda/console/backend/internal/metrics"
)

// Crystallization: materializing a box's userland onto a real VM.
//
// This file implements ADR-019's mechanism, in its order, with its exclusion list
// and — the part that decides whether the product's promise is true — its
// verification. The rule the ADR states and this file obeys: the proof is a file
// manifest, a listening-socket set and an end-to-end probe. NOT "rsync exited 0".
// An rsync that copied nothing exits 0.
//
// The one thing this file cannot do in a container with no hypervisor is boot a
// Beget VM from a slug. So VMTarget stands in for the VM: a SEPARATE root
// directory, seeded with its own kernel image, its own modules directory, its own
// machine-id, hostname, hosts, resolv.conf and fstab, entered through its own
// namespace with its own PID 1. Everything after "the VM exists" is the real
// mechanism against real files. Which parts are the stand-in and which are the
// mechanism is printed in the report rather than left to the reader.

// ADRUserlandExclusions is the fixed set of paths that belong to the machine and
// not to the application, quoted from ADR-019 §"Механизм материализации" step 2.
//
// It is a package-level variable that the verification report PRINTS, because the
// ADR requires exactly that: a list nobody sees drifts away from reality
// silently. Adding an entry here is a deliberate edit reviewed against the ADR.
var ADRUserlandExclusions = []string{
	"/proc", "/sys", "/dev", "/run", "/tmp", "/boot", "/lib/modules",
	"/etc/fstab", "/etc/machine-id", "/etc/hostname", "/etc/resolv.conf", "/etc/hosts",
}

// vmOwnKernelArtifacts are the VM-side files whose survival proves the ADR's
// central structural claim: the VM keeps its own kernel, init and bootloader, and
// only the userland is materialized onto it. A crystallization that overwrote
// these would have produced a container pretending to be a VM.
var vmOwnKernelArtifacts = []string{
	"/boot/vmlinuz-6.8.0-64-generic",
	"/boot/initrd.img-6.8.0-64-generic",
	"/boot/grub/grub.cfg",
	"/lib/modules/6.8.0-64-generic/modules.dep",
	"/etc/machine-id",
	"/etc/fstab",
}

// CrystallizeOptions is what the caller asks for.
type CrystallizeOptions struct {
	// VMName names the stand-in VM root under <Root>/vms/<VMName>/root.
	VMName string
	// Domain is the address the crystallized artifact answers on. Recorded and
	// probed; never used to pick a certificate here (that is phase 8.6).
	Domain string
	// OSSlug is the standard OS image the VM was booted from. It MUST equal the
	// warm image's distribution and version — ADR-019 §3 makes that part of the
	// mechanism, so a mismatch fails the crystallization rather than warning.
	OSSlug string
	// ProbePath is the HTTP path the end-to-end probe requests.
	ProbePath string
}

// FileEntry is one file in a manifest: exactly the tuple ADR-019 §7 names.
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
}

// ManifestComparison is the file-level proof. Counts plus the actual disagreeing
// paths: a count alone tells an operator that something is wrong and nothing about
// what, which is how a verification step becomes decoration.
type ManifestComparison struct {
	BoxFiles    int      `json:"box_files"`
	VMFiles     int      `json:"vm_files"`
	Equal       bool     `json:"equal"`
	MissingOnVM []string `json:"missing_on_vm"`
	Mismatched  []string `json:"mismatched"`
	SampleEqual []string `json:"sample_equal"`
	TotalBytes  int64    `json:"total_bytes"`
}

// SocketComparison is the listening-socket proof.
//
// DeclaredPorts is where the set comes from and it is stated rather than implied:
// the ports the box's own service descriptors publish. LocalRuntime shares the
// host's network namespace, so a set scraped from /proc/net/tcp would describe the
// host and not the box; each port here is probed by a real connect from inside the
// respective root.
type SocketComparison struct {
	DeclaredPorts         []int `json:"declared_ports"`
	ListeningBeforeFreeze []int `json:"listening_before_freeze"`
	ListeningAfterCutover []int `json:"listening_after_cutover"`
	Equal                 bool  `json:"equal"`
}

// HTTPProbeResult is the end-to-end probe against the crystallized artifact.
type HTTPProbeResult struct {
	URL    string `json:"url"`
	Host   string `json:"host"`
	Status int    `json:"status"`
	Body   string `json:"body"`
	OK     bool   `json:"ok"`
}

// EnvComparison compares the env carried to the VM by sha256 PER KEY.
//
// Never by value, and that is a hard rule rather than a preference: a verification
// report is written to logs, mailed, and pasted into tickets, so a report that
// contains DATABASE_URL is a credential leak caused by the safety check.
type EnvComparison struct {
	Keys       []string          `json:"keys"`
	BoxDigest  map[string]string `json:"box_digest"`
	VMDigest   map[string]string `json:"vm_digest"`
	Mismatched []string          `json:"mismatched"`
	Mode       string            `json:"mode"`
	Equal      bool              `json:"equal"`
}

// VolumeRestore records one named volume's restoration by mount path.
type VolumeRestore struct {
	Name      string `json:"name"`
	MountPath string `json:"mount_path"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
	Restored  bool   `json:"restored"`
}

// UnitRender is the systemd unit the entrypoint and ports became.
type UnitRender struct {
	Service string `json:"service"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// CrystallizationReport is the full verification report.
type CrystallizationReport struct {
	BoxID       string `json:"box_id"`
	InstanceRef string `json:"instance_ref"`
	VMName      string `json:"vm_name"`
	VMRoot      string `json:"vm_root"`
	OSSlug      string `json:"os_slug"`
	Domain      string `json:"domain"`
	Stage       string `json:"stage"`
	DurationMS  int64  `json:"duration_ms"`

	ADRExclusions     []string `json:"adr_exclusions"`
	AdapterExclusions []string `json:"adapter_exclusions"`
	VolumeExclusions  []string `json:"volume_exclusions"`

	RsyncCommand string `json:"rsync_command"`

	Manifest        ManifestComparison `json:"manifest"`
	VMOwnArtifacts  map[string]bool    `json:"vm_own_artifacts_intact"`
	BoxSentinels    map[string]bool    `json:"box_machine_sentinels_leaked"`
	Env             EnvComparison      `json:"env"`
	Volumes         []VolumeRestore    `json:"volumes"`
	Units           []UnitRender       `json:"units"`
	Sockets         SocketComparison   `json:"sockets"`
	Probe           HTTPProbeResult    `json:"probe"`
	StoppedServices []string           `json:"stopped_services"`

	Carry CarryManifest `json:"carry"`
	// Honest scope of the stand-in, carried in the report itself so no reader has
	// to infer it from where the code lives.
	StandIn []string `json:"stand_in"`
}

// LocalCrystallizer materializes a box onto a stand-in VM root, on this host.
//
// It satisfies Crystallizer, so the control plane holds only the interface; the
// richer CrystallizeWithReport exists because the interface returns the carry
// manifest and the verification report is bigger than that by design.
type LocalCrystallizer struct {
	Runtime *LocalRuntime
	Clock   Clock
	// Options the API fills in per request.
	Options CrystallizeOptions
}

var _ Crystallizer = (*LocalCrystallizer)(nil)

// Crystallize satisfies the Crystallizer seam.
func (c *LocalCrystallizer) Crystallize(ctx context.Context, inst *Instance, domain string) (CarryManifest, error) {
	opts := c.Options
	opts.Domain = domain
	rep, err := c.CrystallizeWithReport(ctx, inst, opts)
	if rep == nil {
		return nil, err
	}
	return rep.Carry, err
}

// CrystallizeWithReport runs the whole mechanism and returns the verification
// report whether it passed or failed. A failed crystallization with no report is
// an outage nobody can diagnose, so the report is returned alongside the error.
func (c *LocalCrystallizer) CrystallizeWithReport(ctx context.Context, inst *Instance, opts CrystallizeOptions) (*CrystallizationReport, error) {
	rt := c.Runtime
	clock := c.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	if opts.OSSlug == "" {
		opts.OSSlug = WarmImageOSSlug
	}
	if opts.ProbePath == "" {
		opts.ProbePath = "/"
	}
	if opts.VMName == "" {
		opts.VMName = inst.InstanceRef + "-vm"
	}
	started := clock.Now()

	rep := &CrystallizationReport{
		BoxID:             inst.ID,
		InstanceRef:       inst.InstanceRef,
		VMName:            opts.VMName,
		VMRoot:            rt.VMRoot(opts.VMName),
		OSSlug:            opts.OSSlug,
		Domain:            opts.Domain,
		Stage:             "capture",
		ADRExclusions:     ADRUserlandExclusions,
		AdapterExclusions: adapterExclusions(),
		Carry:             CarryManifest{},
		VMOwnArtifacts:    map[string]bool{},
		BoxSentinels:      map[string]bool{},
		StandIn: []string{
			"The VM is a separate root directory on this host, not a Beget instance: " +
				"there is no hypervisor and no Beget token in this environment.",
			"systemd is not present, so the rendered unit is written and its ExecStart is " +
				"executed by the same supervisor the box used. On a real VM systemd runs it.",
			"The VM root shares the host network namespace with the box, so the port set is " +
				"compared by real connects from inside each root rather than by netns separation.",
		},
	}
	for _, v := range rt.Volumes {
		rep.VolumeExclusions = append(rep.VolumeExclusions, v.MountPath)
	}

	fail := func(stage string, err error) (*CrystallizationReport, error) {
		rep.Stage = stage
		rep.DurationMS = clock.Now().Sub(started).Milliseconds()
		metrics.RecordBoxCrystallization("failed", stage, clock.Now().Sub(started))
		return rep, err
	}

	// --- 0. the base of the box image must match the VM slug ------------------
	//
	// ADR-019 §3: this is part of the mechanism, not advice. One distribution's
	// userland on another's kernel builds a chimera that assembles and then breaks
	// in production, so a mismatch stops here.
	boxSlug, err := readOSSlug(filepath.Join(rt.RootFS(inst.InstanceRef), "etc/os-release"))
	if err != nil {
		return fail("capture", fmt.Errorf("crystallize: read box os-release: %w", err))
	}
	if boxSlug != opts.OSSlug {
		return fail("capture", fmt.Errorf(
			"crystallize: box image base %q does not match the VM slug %q; materializing one distribution's userland onto another's kernel is refused (ADR-019 §3)",
			boxSlug, opts.OSSlug))
	}

	// --- 1. capture: declared ports and their live state, then freeze ----------
	descs, err := rt.Services(inst.InstanceRef)
	if err != nil {
		return fail("capture", err)
	}
	declared := declaredPorts(descs)
	rep.Sockets.DeclaredPorts = declared
	before, err := rt.ListeningPorts(ctx, inst, declared)
	if err != nil {
		return fail("capture", err)
	}
	rep.Sockets.ListeningBeforeFreeze = before

	boxEnv, err := rt.EnvSnapshot(inst.InstanceRef)
	if err != nil {
		return fail("capture", err)
	}

	stopped, err := rt.StopServices(ctx, inst)
	if err != nil {
		return fail("capture", err)
	}
	rep.StoppedServices = stopped
	// The declared ports MUST become free, and a port that does not is a hard
	// failure rather than a warning.
	//
	// This is the difference between a verification and a decoration. The socket-set
	// comparison and the HTTP probe both ask "does something answer on this port
	// after the cutover" — and if the box's own process, or any other process, is
	// still holding it, both questions get a yes that says nothing about the VM. The
	// check that makes the later evidence evidence is that nothing else could have
	// produced it.
	if busy := waitPortsFree(declared, 10*time.Second); len(busy) > 0 {
		return fail("capture", fmt.Errorf(
			"crystallize: port(s) %v were not released after the freeze, so nothing answering them afterwards would be evidence about the VM", busy))
	}

	// --- 2. provision the VM from a standard slug -----------------------------
	rep.Stage = "provision"
	vmRoot := rep.VMRoot
	if err := seedVMRoot(vmRoot, opts.VMName, opts.OSSlug); err != nil {
		return fail("provision", err)
	}

	// --- 3. materialize the userland onto the VM's / ---------------------------
	rep.Stage = "seed"
	boxRoot := rt.RootFS(inst.InstanceRef)
	args := []string{"-aHAX", "--numeric-ids"}
	for _, ex := range rep.ADRExclusions {
		args = append(args, "--exclude="+ex)
	}
	for _, ex := range rep.AdapterExclusions {
		args = append(args, "--exclude="+ex)
	}
	for _, ex := range rep.VolumeExclusions {
		args = append(args, "--exclude="+ex)
	}
	args = append(args, boxRoot+"/", vmRoot+"/")
	rep.RsyncCommand = "rsync " + strings.Join(args, " ")
	// No --delete, and that is the ADR: the userland is APPLIED ONTO the VM's
	// root, it does not mirror it. --delete would remove the VM's own kernel,
	// bootloader and modules — the very things that make it a VM.
	if out, err := exec.CommandContext(ctx, "rsync", args...).CombinedOutput(); err != nil {
		return fail("seed", fmt.Errorf("crystallize: rsync userland: %w: %s", err, out))
	}

	// --- 4. restore named volumes by mount path -------------------------------
	for _, v := range rt.Volumes {
		src := filepath.Join(rt.Root, "volumes", inst.InstanceRef, v.Name)
		dst := filepath.Join(vmRoot, strings.TrimPrefix(v.MountPath, "/"))
		vr := VolumeRestore{Name: v.Name, MountPath: v.MountPath}
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return fail("seed", err)
		}
		if out, err := exec.CommandContext(ctx, "rsync", "-aHAX", "--numeric-ids", src+"/", dst+"/").CombinedOutput(); err != nil {
			return fail("seed", fmt.Errorf("crystallize: restore volume %s: %w: %s", v.Name, err, out))
		}
		vr.Files, vr.Bytes, err = countTree(dst)
		if err != nil {
			return fail("seed", err)
		}
		srcFiles, _, err := countTree(src)
		if err != nil {
			return fail("seed", err)
		}
		vr.Restored = vr.Files == srcFiles
		rep.Volumes = append(rep.Volumes, vr)
		if vr.Restored {
			rep.Carry["volume"] = CarryPreserved
		} else {
			rep.Carry["volume"] = CarryLost
		}
	}
	if len(rt.Volumes) == 0 {
		rep.Carry["volume"] = CarryPreserved
	}

	// --- 5. env, out of band, 0600, root --------------------------------------
	//
	// ADR-019 step 5. Written directly to the VM's filesystem, never rendered into
	// a manifest that reaches git: the existing VM track's dbwatcher does render
	// secrets into git, and that is the violation this path does not repeat.
	//
	// Written through the SAME renderer the box used (box.WriteEnvFile), which is
	// why the file compares byte-identical in the manifest. Two renderers would
	// produce a diff for identical content, and a verification failure with no cause
	// is how an operator learns to stop reading the report.
	envPath := filepath.Join(vmRoot, BoxEnvPath)
	if err := WriteEnvFile(envPath, boxEnv); err != nil {
		return fail("seed", err)
	}
	rep.Env = compareEnv(boxEnv, envPath)
	if rep.Env.Equal {
		rep.Carry["env"] = CarryPreserved
	} else {
		rep.Carry["env"] = CarryLost
	}
	// An attachment is carried exactly insofar as its injected credential is: the
	// database itself never lived in the box.
	if hasAttachmentKeys(boxEnv) {
		if rep.Env.Equal {
			rep.Carry["attachment"] = CarryPreserved
		} else {
			rep.Carry["attachment"] = CarryLost
		}
	}

	// --- 6. entrypoint and ports become a systemd unit ------------------------
	for _, d := range descs {
		unit := renderSystemdUnit(d, opts.Domain)
		path := filepath.Join(vmRoot, "etc/systemd/system", d.Name+".service")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fail("seed", err)
		}
		if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
			return fail("seed", err)
		}
		rep.Units = append(rep.Units, UnitRender{
			Service: d.Name + ".service",
			Path:    "/" + filepath.ToSlash(filepath.Join("etc/systemd/system", d.Name+".service")),
			Content: unit,
		})
	}

	// --- 7. verify -------------------------------------------------------------
	rep.Stage = "verify"

	excl := newExclusionSet(rep.ADRExclusions, rep.AdapterExclusions, rep.VolumeExclusions)
	boxManifest, err := walkManifest(boxRoot, excl)
	if err != nil {
		return fail("verify", err)
	}
	vmManifest, err := walkManifest(vmRoot, excl)
	if err != nil {
		return fail("verify", err)
	}
	rep.Manifest = compareManifests(boxManifest, vmManifest)

	// The VM kept its own kernel, init and bootloader.
	for _, p := range vmOwnKernelArtifacts {
		_, err := os.Lstat(filepath.Join(vmRoot, strings.TrimPrefix(p, "/")))
		rep.VMOwnArtifacts[p] = err == nil
	}
	// And none of the box's machine-owned sentinels crossed over. This is what
	// turns "--exclude was passed" into "the exclusion demonstrably held".
	for _, p := range boxMachineSentinels(rt.Volumes) {
		_, err := os.Lstat(filepath.Join(vmRoot, strings.TrimPrefix(p, "/")))
		rep.BoxSentinels[p] = err == nil
	}

	// Restart the carried processes once, on the VM, with the same command and the
	// same environment — the only state ADR-019 admits it cannot carry live.
	session, err := rt.OpenRoot(ctx, vmRoot, opts.VMName, nil)
	if err != nil {
		return fail("cutover", err)
	}
	defer session.Close()
	for _, d := range descs {
		if err := session.StartService(ctx, d); err != nil {
			return fail("cutover", err)
		}
	}
	after, err := waitForPorts(ctx, session, declared, 10*time.Second)
	if err != nil {
		return fail("verify", err)
	}
	rep.Sockets.ListeningAfterCutover = after
	rep.Sockets.Equal = sameInts(before, after)
	if rep.Sockets.Equal {
		rep.Carry["port"] = CarryPreserved
		rep.Carry["process"] = CarryRecreated
	} else {
		rep.Carry["port"] = CarryLost
		rep.Carry["process"] = CarryLost
	}

	// End-to-end HTTP probe against the crystallized artifact.
	if len(after) > 0 {
		rep.Probe = probeHTTP(after[0], opts.Domain, opts.ProbePath)
	} else {
		rep.Probe = HTTPProbeResult{Host: opts.Domain, OK: false}
	}
	if rep.Probe.OK {
		rep.Carry["address"] = CarryRecreated
	} else {
		rep.Carry["address"] = CarryLost
	}

	// Every disposition of "lost" increments the only critical box alert. It is a
	// metric and not merely a test assertion because one loss teaches distrust.
	for kind, disp := range rep.Carry {
		if disp == CarryLost {
			metrics.RecordBoxCrystallizeStateLoss(kind)
		}
	}

	verified := rep.Manifest.Equal && rep.Sockets.Equal && rep.Probe.OK && rep.Env.Equal &&
		allTrue(rep.VMOwnArtifacts) && allFalse(rep.BoxSentinels) && allVolumesRestored(rep.Volumes)
	rep.DurationMS = clock.Now().Sub(started).Milliseconds()
	if !verified {
		metrics.RecordBoxCrystallization("failed", "verify", clock.Now().Sub(started))
		return rep, fmt.Errorf("crystallize: verification failed (manifest_equal=%t sockets_equal=%t probe_ok=%t env_equal=%t vm_kernel_intact=%t no_sentinels_leaked=%t volumes_restored=%t)",
			rep.Manifest.Equal, rep.Sockets.Equal, rep.Probe.OK, rep.Env.Equal,
			allTrue(rep.VMOwnArtifacts), allFalse(rep.BoxSentinels), allVolumesRestored(rep.Volumes))
	}
	rep.Stage = "none"
	metrics.RecordBoxCrystallization("success", "none", clock.Now().Sub(started))
	return rep, nil
}

// --- the VM stand-in ---------------------------------------------------------

// seedVMRoot creates the root of a VM booted from a standard OS slug.
//
// Everything written here is machine-owned: a kernel, an initrd, a bootloader
// config, a modules tree, a machine-id, a hostname, hosts, resolv.conf and an
// fstab. That is precisely the set ADR-019 excludes from the transfer, so these
// files are both the VM's identity and the assertion the transfer must not touch.
func seedVMRoot(root, name, slug string) error {
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	dirs := []string{
		"boot/grub", "lib/modules/6.8.0-64-generic", "etc", "etc/systemd/system",
		"var", "var/log", "srv", "root", "run", "proc", "sys", "dev", "tmp",
		"usr", "bin", "sbin", "lib64", "opt",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return err
		}
	}
	resolv, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		resolv = []byte("nameserver 127.0.0.53\n")
	}
	files := map[string]string{
		"boot/vmlinuz-6.8.0-64-generic":            "# the VM's OWN kernel, from slug " + slug + "\n",
		"boot/initrd.img-6.8.0-64-generic":         "# the VM's OWN initramfs\n",
		"boot/grub/grub.cfg":                       "# the VM's OWN bootloader\nlinux /boot/vmlinuz-6.8.0-64-generic\n",
		"lib/modules/6.8.0-64-generic/modules.dep": "# the VM's OWN kernel modules\n",
		"etc/machine-id":                           "7f9c1b2d3e4f5a6b7c8d9e0f1a2b3c4d\n",
		"etc/hostname":                             name + "\n",
		"etc/hosts":                                "127.0.0.1\tlocalhost " + name + "\n",
		"etc/fstab":                                "LABEL=cloudimg-rootfs / ext4 defaults 0 1\n",
		"etc/os-release": "NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nID=ubuntu\n" +
			"PRETTY_NAME=\"Ubuntu 24.04 LTS\"\nDADA_BOX_OS_SLUG=" + slug + "\n",
		"sbin/init": "#!/bin/sh\n# the VM's OWN init (systemd on a real instance)\n",
	}
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(root, "etc/resolv.conf"), resolv, 0o644)
}

// boxMachineSentinels lists the box-side files that must never appear on the VM.
//
// Volume sentinels are deliberately NOT in this list: a volume's content is
// SUPPOSED to arrive, just through the volume restore (step 4) and not through the
// userland rsync (step 3). The mountpoint's exclusion is proven a different way —
// from outside the box's namespace the mountpoint is an empty directory, so the
// rsync had nothing there to carry, and the restore's own file count is what shows
// the content arrived.
func boxMachineSentinels(vols []Volume) []string {
	return []string{
		"/boot/vmlinuz-box-sentinel",
		"/lib/modules/.box-sentinel",
		"/proc/.box-sentinel",
		"/sys/.box-sentinel",
		"/dev/.box-sentinel",
		"/run/.box-sentinel",
		"/tmp/.box-sentinel",
	}
}

// adapterExclusions are the LocalRuntime-specific mountpoints excluded on top of
// ADR-019's fixed list, kept SEPARATE from it in the report.
//
// They exist because on this adapter the toolchain is bind-mounted from the host
// read-only, so inside the box these are mountpoints and from outside they are
// empty directories — not the box's userland. In the production adapter the
// container image owns /usr and this list is empty. Folding the two lists together
// would make an adapter detail look like an architectural decision.
func adapterExclusions() []string {
	out := make([]string, 0, len(sharedSystemDirs))
	for _, d := range sharedSystemDirs {
		out = append(out, "/"+d)
	}
	return out
}

// --- systemd unit ------------------------------------------------------------

// renderSystemdUnit turns one service descriptor into a systemd unit.
//
// ExecStart from the command, WorkingDirectory from the box's working directory,
// EnvironmentFile pointing at the 0600 env file, Restart=always, and the published
// ports redeclared as-is in a comment — ADR-019 step 6. There is no Docker, no
// compose and no Portainer agent in the result: a crystallized VM is not a
// container host, which is why the existing bootstrap.sh.tmpl path does not apply
// to it.
func renderSystemdUnit(d ServiceDescriptor, domain string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Rendered by Dada Box crystallization from the box's own service descriptor\n")
	fmt.Fprintf(&b, "# /etc/dada/services/%s.json. No Docker, no compose, no Portainer agent:\n", d.Name)
	fmt.Fprintf(&b, "# a crystallized VM is not a container host (ADR-019).\n")
	b.WriteString("[Unit]\n")
	fmt.Fprintf(&b, "Description=Dada Box crystallized service %s\n", d.Name)
	b.WriteString("After=network-online.target\nWants=network-online.target\n\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	fmt.Fprintf(&b, "WorkingDirectory=%s\n", d.WorkingDir)
	fmt.Fprintf(&b, "EnvironmentFile=/%s\n", BoxEnvPath)
	fmt.Fprintf(&b, "ExecStart=/bin/sh -c %s\n", shellQuote(d.Command))
	b.WriteString("Restart=always\nRestartSec=2\n")
	for _, p := range d.Ports {
		fmt.Fprintf(&b, "# published port (redeclared as-is): %d\n", p)
	}
	if domain != "" {
		fmt.Fprintf(&b, "# address: %s\n", domain)
	}
	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

// --- manifests ---------------------------------------------------------------

type exclusionSet struct{ paths []string }

func newExclusionSet(lists ...[]string) *exclusionSet {
	s := &exclusionSet{}
	for _, l := range lists {
		for _, p := range l {
			s.paths = append(s.paths, strings.TrimSuffix(p, "/"))
		}
	}
	return s
}

// excluded reports whether an absolute in-tree path is excluded. Anchored at the
// transfer root and prefix-matching on directory boundaries, which is exactly how
// rsync treats a pattern with a leading slash — so the manifest is taken over the
// same set of paths rsync transferred, not a similar-looking one.
func (s *exclusionSet) excluded(p string) bool {
	for _, ex := range s.paths {
		if p == ex || strings.HasPrefix(p, ex+"/") {
			return true
		}
	}
	return false
}

// walkManifest builds the (path, size, mode, sha256) manifest of a tree's
// userland set.
//
// Symlinks are hashed by their TARGET STRING rather than followed: following them
// would hash whatever the link points at on the host, so two trees with different
// links into identical content would compare equal. That is the kind of false
// pass a verification step exists to prevent.
func walkManifest(root string, excl *exclusionSet) (map[string]FileEntry, error) {
	out := map[string]FileEntry{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) || os.IsPermission(err) {
				return nil
			}
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		abs := "/" + filepath.ToSlash(rel)
		if excl.excluded(abs) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		entry := FileEntry{Path: abs, Mode: fmt.Sprintf("%04o", info.Mode().Perm())}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256([]byte("symlink:" + target))
			entry.SHA256 = "symlink:" + hex.EncodeToString(sum[:])
			entry.Size = int64(len(target))
		case info.IsDir():
			entry.SHA256 = "dir"
		case info.Mode().IsRegular():
			sum, err := sha256File(p)
			if err != nil {
				return err
			}
			entry.SHA256 = sum
			entry.Size = info.Size()
		default:
			entry.SHA256 = "special:" + info.Mode().String()
		}
		out[abs] = entry
		return nil
	})
	return out, err
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// compareManifests compares the box's userland manifest against the VM's.
//
// Direction matters and is deliberate: every path the box had must exist on the VM
// with the same tuple. Paths present only on the VM are NOT failures — they are
// the VM's own kernel, bootloader and machine identity, which the ADR requires to
// survive, and they are asserted separately in VMOwnArtifacts.
func compareManifests(boxM, vmM map[string]FileEntry) ManifestComparison {
	cmp := ManifestComparison{BoxFiles: len(boxM), VMFiles: len(vmM)}
	paths := make([]string, 0, len(boxM))
	for p := range boxM {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		b := boxM[p]
		cmp.TotalBytes += b.Size
		v, ok := vmM[p]
		if !ok {
			cmp.MissingOnVM = append(cmp.MissingOnVM, p)
			continue
		}
		if b.Size != v.Size || b.Mode != v.Mode || b.SHA256 != v.SHA256 {
			cmp.Mismatched = append(cmp.Mismatched,
				fmt.Sprintf("%s box=(%d,%s,%s) vm=(%d,%s,%s)", p, b.Size, b.Mode, short(b.SHA256), v.Size, v.Mode, short(v.SHA256)))
		}
	}
	cmp.Equal = len(cmp.MissingOnVM) == 0 && len(cmp.Mismatched) == 0 && cmp.BoxFiles > 0
	// A handful of matched entries is printed so a reader can see the comparison
	// is over real content, not over an empty set that trivially agrees.
	for _, p := range []string{"/etc/dada/services", "/srv/app", "/etc/os-release", "/root"} {
		if e, ok := boxM[p]; ok {
			cmp.SampleEqual = append(cmp.SampleEqual, fmt.Sprintf("%s (%d bytes, %s, %s)", p, e.Size, e.Mode, short(e.SHA256)))
		}
	}
	return cmp
}

func short(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

func countTree(root string) (files int, bytes int64, err error) {
	err = filepath.Walk(root, func(p string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if info.Mode().IsRegular() {
			files++
			bytes += info.Size()
		}
		return nil
	})
	return files, bytes, err
}

// --- env ---------------------------------------------------------------------

// compareEnv compares the carried env per key by sha256 and records the file mode.
func compareEnv(boxEnv map[string]string, vmPath string) EnvComparison {
	out := EnvComparison{BoxDigest: map[string]string{}, VMDigest: map[string]string{}}
	vmEnv := map[string]string{}
	if raw, err := os.ReadFile(vmPath); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if k, v, ok := strings.Cut(line, "="); ok {
				vmEnv[strings.TrimSpace(k)] = shellUnquote(strings.TrimSpace(v))
			}
		}
	}
	if fi, err := os.Stat(vmPath); err == nil {
		out.Mode = fmt.Sprintf("%04o", fi.Mode().Perm())
	}
	for k := range boxEnv {
		out.Keys = append(out.Keys, k)
	}
	sort.Strings(out.Keys)
	for _, k := range out.Keys {
		bd := digest(boxEnv[k])
		out.BoxDigest[k] = bd
		vd, ok := vmEnv[k]
		if !ok {
			out.Mismatched = append(out.Mismatched, k+" (absent on vm)")
			continue
		}
		out.VMDigest[k] = digest(vd)
		if out.VMDigest[k] != bd {
			out.Mismatched = append(out.Mismatched, k+" (digest differs)")
		}
	}
	out.Equal = len(out.Mismatched) == 0 && out.Mode == "0600" && len(out.Keys) > 0
	return out
}

func digest(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])[:16]
}

func hasAttachmentKeys(env map[string]string) bool {
	for k := range env {
		if strings.HasSuffix(k, "DATABASE_URL") || strings.HasPrefix(k, "AWS_") || strings.HasSuffix(k, "S3_ENDPOINT") {
			return true
		}
	}
	return false
}

// --- ports and probe ---------------------------------------------------------

func declaredPorts(descs []ServiceDescriptor) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, d := range descs {
		for _, p := range d.Ports {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Ints(out)
	return out
}

// waitPortsFree waits for every port to stop accepting and returns the ones that
// never did.
func waitPortsFree(ports []int, within time.Duration) []int {
	deadline := time.Now().Add(within)
	for {
		var busy []int
		for _, p := range ports {
			c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 200*time.Millisecond)
			if err == nil {
				_ = c.Close()
				busy = append(busy, p)
			}
		}
		if len(busy) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return busy
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func waitForPorts(ctx context.Context, s *RootSession, ports []int, within time.Duration) ([]int, error) {
	deadline := time.Now().Add(within)
	var last []int
	for {
		got, err := s.ListeningPorts(ctx, ports)
		if err != nil {
			return nil, err
		}
		last = got
		if len(got) == len(ports) {
			return got, nil
		}
		if time.Now().After(deadline) {
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// probeHTTP performs the end-to-end probe against the crystallized artifact,
// sending the crystallized domain as the Host header so the request is the one a
// real client would make rather than a loopback shortcut with a different name.
func probeHTTP(port int, domain, path string) HTTPProbeResult {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	res := HTTPProbeResult{URL: url, Host: domain}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return res
	}
	if domain != "" {
		req.Host = domain
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		res.Body = err.Error()
		return res
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	res.Status = resp.StatusCode
	res.Body = strings.TrimSpace(string(body))
	res.OK = resp.StatusCode == http.StatusOK
	return res
}

// --- small helpers -----------------------------------------------------------

func readOSSlug(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "DADA_BOX_OS_SLUG="); ok {
			return strings.Trim(v, `"`), nil
		}
	}
	return "", fmt.Errorf("no DADA_BOX_OS_SLUG in %s", path)
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]int(nil), a...)
	y := append([]int(nil), b...)
	sort.Ints(x)
	sort.Ints(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return len(x) > 0
}

func allTrue(m map[string]bool) bool {
	if len(m) == 0 {
		return false
	}
	for _, v := range m {
		if !v {
			return false
		}
	}
	return true
}

func allFalse(m map[string]bool) bool {
	for _, v := range m {
		if v {
			return false
		}
	}
	return true
}

func allVolumesRestored(vs []VolumeRestore) bool {
	for _, v := range vs {
		if !v.Restored {
			return false
		}
	}
	return true
}

// Text renders the report the way an operator reads it. The exclusion lists are
// printed in full because ADR-019 requires the list to be part of the report:
// a list nobody sees diverges from reality without anyone noticing.
func (r *CrystallizationReport) Text() string {
	var b strings.Builder
	line := strings.Repeat("-", 78)
	fmt.Fprintf(&b, "CRYSTALLIZATION VERIFICATION REPORT\n%s\n", line)
	fmt.Fprintf(&b, "box            %s (%s)\n", r.BoxID, r.InstanceRef)
	fmt.Fprintf(&b, "vm             %s  root=%s\n", r.VMName, r.VMRoot)
	fmt.Fprintf(&b, "os slug        %s (box image base and VM slug must match — ADR-019 §3)\n", r.OSSlug)
	fmt.Fprintf(&b, "domain         %s\n", r.Domain)
	fmt.Fprintf(&b, "stage          %s   duration %dms\n", r.Stage, r.DurationMS)
	fmt.Fprintf(&b, "%s\nADR-019 FIXED EXCLUSIONS (machine, not application)\n", line)
	for _, e := range r.ADRExclusions {
		fmt.Fprintf(&b, "  %s\n", e)
	}
	fmt.Fprintf(&b, "ADAPTER EXCLUSIONS (LocalRuntime: host toolchain bind-mounted read-only)\n")
	for _, e := range r.AdapterExclusions {
		fmt.Fprintf(&b, "  %s\n", e)
	}
	fmt.Fprintf(&b, "VOLUME MOUNTPOINT EXCLUSIONS (restored separately, ADR-019 step 4)\n")
	for _, e := range r.VolumeExclusions {
		fmt.Fprintf(&b, "  %s\n", e)
	}
	fmt.Fprintf(&b, "%s\nTRANSFER\n  %s\n", line, r.RsyncCommand)
	fmt.Fprintf(&b, "  (no --delete: the userland is applied ONTO the VM's root, it does not mirror it)\n")
	fmt.Fprintf(&b, "%s\nFILE MANIFEST (path, size, mode, sha256)\n", line)
	fmt.Fprintf(&b, "  box userland entries   %d  (%d bytes)\n", r.Manifest.BoxFiles, r.Manifest.TotalBytes)
	fmt.Fprintf(&b, "  vm userland entries    %d\n", r.Manifest.VMFiles)
	fmt.Fprintf(&b, "  missing on vm          %d\n", len(r.Manifest.MissingOnVM))
	for _, p := range firstN(r.Manifest.MissingOnVM, 20) {
		fmt.Fprintf(&b, "      - %s\n", p)
	}
	fmt.Fprintf(&b, "  mismatched tuples      %d\n", len(r.Manifest.Mismatched))
	for _, p := range firstN(r.Manifest.Mismatched, 20) {
		fmt.Fprintf(&b, "      ! %s\n", p)
	}
	for _, p := range r.Manifest.SampleEqual {
		fmt.Fprintf(&b, "      = %s\n", p)
	}
	fmt.Fprintf(&b, "  MANIFEST EQUAL         %t\n", r.Manifest.Equal)
	fmt.Fprintf(&b, "%s\nTHE VM KEPT ITS OWN KERNEL, INIT AND BOOTLOADER\n", line)
	for _, k := range sortedKeys(r.VMOwnArtifacts) {
		fmt.Fprintf(&b, "  %-46s intact=%t\n", k, r.VMOwnArtifacts[k])
	}
	fmt.Fprintf(&b, "NO BOX MACHINE-OWNED SENTINEL CROSSED OVER\n")
	for _, k := range sortedKeys(r.BoxSentinels) {
		fmt.Fprintf(&b, "  %-46s leaked=%t\n", k, r.BoxSentinels[k])
	}
	fmt.Fprintf(&b, "%s\nENV (sha256 per key, never values)\n", line)
	fmt.Fprintf(&b, "  file mode              %s\n", r.Env.Mode)
	for _, k := range r.Env.Keys {
		fmt.Fprintf(&b, "  %-24s box=%s vm=%s\n", k, r.Env.BoxDigest[k], r.Env.VMDigest[k])
	}
	fmt.Fprintf(&b, "  ENV EQUAL              %t  mismatched=%v\n", r.Env.Equal, r.Env.Mismatched)
	fmt.Fprintf(&b, "%s\nVOLUMES RESTORED BY MOUNT PATH\n", line)
	for _, v := range r.Volumes {
		fmt.Fprintf(&b, "  %-10s -> %-12s files=%d bytes=%d restored=%t\n", v.Name, v.MountPath, v.Files, v.Bytes, v.Restored)
	}
	fmt.Fprintf(&b, "%s\nENTRYPOINT AND PORTS AS SYSTEMD UNITS\n", line)
	for _, u := range r.Units {
		fmt.Fprintf(&b, "  %s  (%s)\n", u.Service, u.Path)
		for _, l := range strings.Split(strings.TrimRight(u.Content, "\n"), "\n") {
			fmt.Fprintf(&b, "    | %s\n", l)
		}
	}
	fmt.Fprintf(&b, "%s\nLISTENING SOCKET SET\n", line)
	fmt.Fprintf(&b, "  declared by the box    %v\n", r.Sockets.DeclaredPorts)
	fmt.Fprintf(&b, "  listening before freeze %v\n", r.Sockets.ListeningBeforeFreeze)
	fmt.Fprintf(&b, "  listening after cutover %v\n", r.Sockets.ListeningAfterCutover)
	fmt.Fprintf(&b, "  SET EQUAL              %t\n", r.Sockets.Equal)
	fmt.Fprintf(&b, "  stopped at freeze      %v (restarted once on the VM)\n", r.StoppedServices)
	fmt.Fprintf(&b, "%s\nEND-TO-END HTTP PROBE AGAINST THE CRYSTALLIZED ARTIFACT\n", line)
	fmt.Fprintf(&b, "  GET %s   Host: %s\n", r.Probe.URL, r.Probe.Host)
	fmt.Fprintf(&b, "  status %d   ok=%t\n", r.Probe.Status, r.Probe.OK)
	fmt.Fprintf(&b, "  body   %s\n", r.Probe.Body)
	fmt.Fprintf(&b, "%s\nCARRY MANIFEST (preserved|recreated|lost)\n", line)
	for _, k := range sortedCarry(r.Carry) {
		fmt.Fprintf(&b, "  %-12s %s\n", k, r.Carry[k])
	}
	fmt.Fprintf(&b, "%s\nWHAT IS A STAND-IN IN THIS ENVIRONMENT\n", line)
	for _, s := range r.StandIn {
		fmt.Fprintf(&b, "  * %s\n", s)
	}
	return b.String()
}

func firstN(in []string, n int) []string {
	if len(in) <= n {
		return in
	}
	return in[:n]
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedCarry(m CarryManifest) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

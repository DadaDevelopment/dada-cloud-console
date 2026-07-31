// Command box-init is PID 1 inside a box Pod.
//
// WHY IT EXISTS. On LocalRuntime the box's namespaces are assembled by a shell
// script and the toolchain arrives as read-only binds from the host
// (internal/box/localruntime.go). In the cluster none of that is needed or even
// possible: the container IS the mount, PID and network namespace, and the
// toolchain is baked into the warm image. What is still needed is the part that
// script did AFTER the chroot — stamp the box's identity, put the box's own door
// up, and stay alive as the process whose death tears the body down. That is this
// binary, and it is deliberately the whole of it.
//
// WHAT IT GUARANTEES, in the order the pod's probes depend on:
//
//   - /etc/dada/root-marker carries BOX_ID before the broker is started. The
//     control plane's identity probe reads exactly this file and refuses to treat
//     a body as the box until it answers with the box's own ref, so writing it
//     late would make a green probe mean "some root answered".
//   - /run/dada-broker exists, 0700, with an empty tokens file. The broker refuses
//     to start without one (cmd/box-broker), which is correct — but the file is
//     control-plane state, and a body that cannot come up until the control plane
//     has written to it could never be pre-warmed. An empty file accepts nobody,
//     which is the right starting posture for a parked box.
//   - The broker is restarted if it exits. The pod's startupProbe and
//     readinessProbe are a TCP connect to the broker's port, so a broker that died
//     without a supervisor would take the box's readiness with it while the
//     container kept running — a body that is alive, billed, and unreachable.
//
// It also reaps orphans, because PID 1 in a pod inherits every process whose
// parent exits inside the box, and an unreaped zombie eventually exhausts the
// box's pid budget. Nothing else here is a supervisor: a box is the tenant's, and
// this process does not manage their work.
//
// The broker's output goes to this process's own stdout and stderr rather than to
// a file in the box, so `kubectl logs` on the pod shows the door's behaviour. In
// the box's own filesystem it would be both invisible to operators and, being
// under /run, gone the moment the body is.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const (
	brokerDir    = "/run/dada-broker"
	tokensPath   = brokerDir + "/tokens"
	addrPath     = brokerDir + "/addr"
	markerPath   = "/etc/dada/root-marker"
	brokerBinary = "/usr/local/bin/box-broker"
)

// brokerRestartDelay keeps a broker that fails instantly from spinning the
// container's CPU. It is short because the box is unreachable for exactly this
// long, and readiness is what the tenant is waiting on.
const brokerRestartDelay = time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "box-init: %v\n", err)
		os.Exit(1)
	}
}

// run stamps the box, starts its door and stays as PID 1 until the pod is asked
// to stop.
//
// An empty BOX_ID is refused rather than defaulted: an unidentified body would
// answer the control plane's identity probe with an empty marker, and a probe that
// cannot tell two boxes apart is worse than one that fails.
func run() error {
	boxID := os.Getenv("BOX_ID")
	if boxID == "" {
		return errors.New("BOX_ID is required: the box's identity marker is what the control plane's probe reads")
	}
	if err := stampIdentity(boxID); err != nil {
		return err
	}
	if err := prepareBrokerDir(); err != nil {
		return err
	}

	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGCHLD)

	broker, err := startBroker(boxID)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "box-init: box %s up, broker pid %d\n", boxID, broker.Process.Pid)

	for sig := range sigs {
		switch sig {
		case syscall.SIGTERM, syscall.SIGINT:
			stopBroker(broker)
			return nil
		case syscall.SIGCHLD:
			if !reap(broker.Process.Pid) {
				continue
			}
			time.Sleep(brokerRestartDelay)
			broker, err = startBroker(boxID)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stdout, "box-init: broker restarted, pid %d\n", broker.Process.Pid)
		}
	}
	return nil
}

// stampIdentity writes the marker the control plane's probe reads.
func stampIdentity(boxID string) error {
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(markerPath), err)
	}
	if err := os.WriteFile(markerPath, []byte(boxID+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", markerPath, err)
	}
	return nil
}

// prepareBrokerDir creates the door's directory and the empty digest file it
// refuses to start without.
//
// 0700 and 0600: the directory holds credential digests for every live session of
// this box. The box runs the tenant's own code as root, so the mode is not a
// boundary against them — it is a boundary against anything they run that is not
// them.
func prepareBrokerDir() error {
	if err := os.MkdirAll(brokerDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", brokerDir, err)
	}
	f, err := os.OpenFile(tokensPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", tokensPath, err)
	}
	return f.Close()
}

// startBroker launches the box's own door and hands it the paths it publishes to.
//
// BOX_BROKER_ADDR is taken from the pod's environment rather than defaulted here,
// because the port in it is also the port the pod's probes connect to and the port
// its exposure routes to. A default in this binary would be a fourth place that
// number lives, and the first three disagree loudly while this one would not.
func startBroker(boxID string) (*exec.Cmd, error) {
	cmd := exec.Command(brokerBinary)
	cmd.Env = append(os.Environ(),
		"BOX_BROKER_TOKENS="+tokensPath,
		"BOX_BROKER_ADDR_FILE="+addrPath,
		"BOX_NAME="+boxID,
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", brokerBinary, err)
	}
	return cmd, nil
}

// stopBroker asks the door to close. It does not wait: the pod's
// terminationGracePeriodSeconds is the deadline that actually applies, and a wait
// here would only compete with it.
func stopBroker(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
}

// reap collects every finished child and reports whether the broker was among
// them.
//
// Wait4 with -1 rather than cmd.Wait: PID 1 is handed every orphan in the box, and
// a supervisor that only waits on its own child leaves the rest as zombies until
// the box runs out of pids. The broker is recognised by pid, which is what makes
// its exit distinguishable from a tenant process finishing.
func reap(brokerPID int) bool {
	brokerDied := false
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if err != nil || pid <= 0 {
			return brokerDied
		}
		if pid == brokerPID {
			brokerDied = true
		}
	}
}

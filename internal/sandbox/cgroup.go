package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

const cgroupV2Root = "/sys/fs/cgroup"

const launcherLeaf = "runboxd.self"

type cgroupState struct {
	mount  string
	reason error
}

var (
	cgroupOnce  sync.Once
	cgroupCache cgroupState
)

func delegatedCgroupMount(override string) (string, error) {
	cgroupOnce.Do(func() { cgroupCache = setupDelegatedCgroup(override) })
	return cgroupCache.mount, cgroupCache.reason
}

func cgroupsActive() bool {
	mount, _ := delegatedCgroupMount("")
	return mount != ""
}

func setupDelegatedCgroup(override string) cgroupState {
	mount := override
	if mount == "" {
		detected, err := detectOwnCgroup()
		if err != nil {
			return cgroupState{reason: err}
		}
		mount = detected
	}
	if !cgroupControllersAvailable(mount) {
		return cgroupState{reason: fmt.Errorf("cgroup %q does not expose memory+pids controllers", mount)}
	}
	if err := seatSelfInLeaf(mount); err != nil {
		return cgroupState{reason: fmt.Errorf("cannot seat launcher under %q: %w", mount, err)}
	}
	if err := enableSubtreeControl(mount); err != nil {
		return cgroupState{reason: fmt.Errorf("cannot enable subtree_control on %q: %w", mount, err)}
	}
	return cgroupState{mount: mount}
}

func detectOwnCgroup() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("reading /proc/self/cgroup: %w", err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if rel, ok := strings.CutPrefix(line, "0::"); ok {
			return filepath.Join(cgroupV2Root, rel), nil
		}
	}
	return "", fmt.Errorf("no cgroup v2 (0::) entry in /proc/self/cgroup")
}

func cgroupControllersAvailable(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "cgroup.controllers"))
	if err != nil {
		return false
	}
	controllers := strings.Fields(string(data))
	return slices.Contains(controllers, "memory") && slices.Contains(controllers, "pids")
}

func seatSelfInLeaf(mount string) error {
	leaf := filepath.Join(mount, launcherLeaf)
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		return fmt.Errorf("creating leaf cgroup: %w", err)
	}
	pid := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(filepath.Join(leaf, "cgroup.procs"), []byte(pid), 0o644); err != nil {
		return fmt.Errorf("writing pid to leaf cgroup.procs: %w", err)
	}
	return nil
}

func enableSubtreeControl(mount string) error {
	controllers := []byte("+memory +pids +cpu")
	if err := os.WriteFile(filepath.Join(mount, "cgroup.subtree_control"), controllers, 0o644); err != nil {
		return fmt.Errorf("enabling subtree_control: %w", err)
	}
	return nil
}

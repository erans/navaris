//go:build firecracker

package firecracker

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// cpuPeriod is the CFS scheduling period used for all FC sandboxes.
// 100ms is the kernel default; quota is computed as LimitCPU * cpuPeriod.
const cpuPeriod int64 = 100_000

// cgroup filesystem magic numbers (defined by the Linux kernel).
// Used to verify CgroupRoot actually points at a cgroup mount before
// declaring setup successful — a misconfigured root would otherwise let
// us happily write plain files into a regular directory and report
// success while CPU enforcement does nothing.
const (
	cgroup2SuperMagic = 0x63677270 // CGROUP2_SUPER_MAGIC
	cgroupV1CPUMagic  = 0x27e0eb   // CGROUP_SUPER_MAGIC (any v1 controller mount)
)

// cgroupCPUDir returns the absolute filesystem path to the CPU cgroup
// directory for vmID. Caller writes cpu.max (v2) or cpu.cfs_quota_us +
// cpu.cfs_period_us (v1) inside this directory.
//
// Mapping from CgroupRoot to per-version layout:
//   - v2: paths sit directly under the unified hierarchy. CgroupRoot is
//     used verbatim as a subtree path (e.g. /sys/fs/cgroup/navaris-fc).
//   - v1: each controller has its own mount (/sys/fs/cgroup/cpu/...).
//     We rebase CgroupRoot from the unified prefix /sys/fs/cgroup/ to the
//     cpu controller mount /sys/fs/cgroup/cpu/, preserving any nested
//     subtrees beyond the prefix. So /sys/fs/cgroup/team/navaris-fc maps
//     to /sys/fs/cgroup/cpu/team/navaris-fc, not a flattened version.
func (p *Provider) cgroupCPUDir(vmID string) string {
	if p.config.EnableJailer {
		// Jailer creates firecracker/<vm-id> by default; we follow the same convention.
		if p.cgroupVersion == "1" {
			return filepath.Join("/sys/fs/cgroup/cpu/firecracker", vmID)
		}
		return filepath.Join("/sys/fs/cgroup/firecracker", vmID)
	}
	if p.cgroupVersion == "1" {
		// Re-root CgroupRoot under /sys/fs/cgroup/cpu/, preserving any nested
		// subtree the operator configured under /sys/fs/cgroup/. Handle the
		// exact "/sys/fs/cgroup" case (no trailing slash) separately so it
		// doesn't get joined verbatim.
		const unifiedPrefix = "/sys/fs/cgroup/"
		var subtree string
		if p.config.CgroupRoot == "/sys/fs/cgroup" {
			subtree = ""
		} else {
			subtree = strings.TrimPrefix(p.config.CgroupRoot, unifiedPrefix)
		}
		return filepath.Join("/sys/fs/cgroup/cpu", subtree, vmID)
	}
	return filepath.Join(p.config.CgroupRoot, vmID)
}

// writeCPUMax writes the CFS quota+period to the cgroup directory dir,
// branching on cgroupVersion.
func (p *Provider) writeCPUMax(dir string, quota, period int64) error {
	if p.cgroupVersion == "1" {
		if err := os.WriteFile(filepath.Join(dir, "cpu.cfs_quota_us"),
			[]byte(strconv.FormatInt(quota, 10)), 0644); err != nil {
			return fmt.Errorf("write cpu.cfs_quota_us: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cpu.cfs_period_us"),
			[]byte(strconv.FormatInt(period, 10)), 0644); err != nil {
			return fmt.Errorf("write cpu.cfs_period_us: %w", err)
		}
		return nil
	}
	// cgroup v2: single cpu.max file with "<quota> <period>".
	line := fmt.Sprintf("%d %d", quota, period)
	if err := os.WriteFile(filepath.Join(dir, "cpu.max"), []byte(line), 0644); err != nil {
		return fmt.Errorf("write cpu.max: %w", err)
	}
	return nil
}

// verifyCgroupFS returns nil if dir (or its nearest existing ancestor) is
// on a cgroup filesystem matching p.cgroupVersion. Returns an error
// otherwise — most commonly because the operator pointed
// --firecracker-cgroup-root at a regular directory (e.g. /tmp/...).
//
// Walking up to the nearest existing ancestor matters because a typical
// CgroupRoot (/sys/fs/cgroup/navaris-fc) may not exist on first run; we
// check the parent (/sys/fs/cgroup/) which DOES exist and IS a cgroup
// mount. Without this walk-up, MkdirAll would happily create
// /tmp/navaris-fc and silently disable enforcement.
//
// Tests that don't run on a real cgroup mount set p.cgroupSkipFSCheck to
// bypass; production code paths never set it.
func (p *Provider) verifyCgroupFS(dir string) error {
	if p.cgroupSkipFSCheck {
		return nil
	}
	// Walk up to the nearest existing path. Stop at "/" — if we got that far
	// without finding the directory, MkdirAll downstream will fail too.
	probe := dir
	for {
		if _, err := os.Stat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return fmt.Errorf("%s does not exist and has no existing ancestor", dir)
		}
		probe = parent
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(probe, &stat); err != nil {
		return fmt.Errorf("statfs %s: %w", probe, err)
	}
	want := int64(cgroup2SuperMagic)
	wantName := "cgroup v2"
	if p.cgroupVersion == "1" {
		want = cgroupV1CPUMagic
		wantName = "cgroup v1 cpu controller"
	}
	if int64(stat.Type) != want {
		return fmt.Errorf("%s is not on a %s mount (statfs type 0x%x); check --firecracker-cgroup-root",
			probe, wantName, stat.Type)
	}
	return nil
}

// setupCgroup creates a per-VM cgroup, enables the cpu controller (v2),
// places the FC PID into it, and writes the initial CPU bandwidth quota.
// Returns nil and is a no-op in jailer mode (the jailer handles cgroup
// creation and the initial limit via JailerCfg.CgroupArgs).
//
// Idempotent: tolerates "already exists" / "already enabled" errors so a
// second call (or an inherited cgroup from a previous daemon invocation)
// does not fail the sandbox start.
//
// Validates that the parent CgroupRoot is actually on a cgroup filesystem
// before doing anything (see verifyCgroupFS). Without that check, a
// misconfigured root would silently disable enforcement.
func (p *Provider) setupCgroup(pid int, vmID string, limitCPU int64) error {
	if p.config.EnableJailer {
		return nil // jailer handles cgroup creation
	}

	// Validate the parent root is on a cgroup mount BEFORE MkdirAll. The
	// validation walks up to the nearest existing ancestor, so a typical
	// CgroupRoot like /sys/fs/cgroup/navaris-fc passes (parent
	// /sys/fs/cgroup/ exists and is cgroupfs) while /tmp/navaris-fc fails
	// (parent /tmp is tmpfs/ext4, not cgroupfs). Without this check
	// MkdirAll would silently create a regular directory tree and
	// setupCgroup would happily write plain files into it, leaving CPU
	// enforcement disabled while the daemon thinks it's active.
	parentRoot := p.config.CgroupRoot
	if p.cgroupVersion == "1" {
		const unifiedPrefix = "/sys/fs/cgroup/"
		var subtree string
		if p.config.CgroupRoot == "/sys/fs/cgroup" {
			subtree = ""
		} else {
			subtree = strings.TrimPrefix(p.config.CgroupRoot, unifiedPrefix)
		}
		parentRoot = filepath.Join("/sys/fs/cgroup/cpu", subtree)
	}
	if err := p.verifyCgroupFS(parentRoot); err != nil {
		return err
	}

	dir := p.cgroupCPUDir(vmID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir cgroup dir %s: %w", dir, err)
	}

	// On cgroup v2, child cgroups can only use the cpu controller if the
	// parent enables it via subtree_control. Idempotent — ignore "no such
	// device" / "already enabled" errors which the kernel returns when the
	// controller is already enabled.
	if p.cgroupVersion == "2" {
		parent := filepath.Dir(dir)
		_ = os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"),
			[]byte("+cpu"), 0644)
	}

	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"),
		[]byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("write cgroup.procs: %w", err)
	}

	quota := limitCPU * cpuPeriod
	if err := p.writeCPUMax(dir, quota, cpuPeriod); err != nil {
		return err
	}
	return nil
}

// removeCgroup deletes the per-VM cgroup directory. Idempotent: missing
// directories are not an error. Jailer mode is a no-op (jailer cleans up
// its own cgroup tree on FC exit).
func (p *Provider) removeCgroup(vmID string) error {
	if p.config.EnableJailer {
		return nil
	}
	dir := p.cgroupCPUDir(vmID)
	if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove cgroup dir %s: %w", dir, err)
	}
	return nil
}

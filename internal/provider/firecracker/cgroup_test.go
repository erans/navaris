//go:build firecracker

package firecracker

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCgroupCPUDir_NonJailer_v2(t *testing.T) {
	p := &Provider{
		config:        Config{CgroupRoot: "/sys/fs/cgroup/navaris-fc", EnableJailer: false},
		cgroupVersion: "2",
	}
	got := p.cgroupCPUDir("vm-x")
	want := "/sys/fs/cgroup/navaris-fc/vm-x"
	if got != want {
		t.Errorf("cgroupCPUDir = %q, want %q", got, want)
	}
}

func TestCgroupCPUDir_NonJailer_v1(t *testing.T) {
	p := &Provider{
		config:        Config{CgroupRoot: "/sys/fs/cgroup/navaris-fc", EnableJailer: false},
		cgroupVersion: "1",
	}
	got := p.cgroupCPUDir("vm-x")
	want := "/sys/fs/cgroup/cpu/navaris-fc/vm-x"
	if got != want {
		t.Errorf("cgroupCPUDir = %q, want %q", got, want)
	}
}

func TestCgroupCPUDir_Jailer_v2(t *testing.T) {
	p := &Provider{
		config:        Config{EnableJailer: true},
		cgroupVersion: "2",
	}
	got := p.cgroupCPUDir("vm-x")
	want := "/sys/fs/cgroup/firecracker/vm-x"
	if got != want {
		t.Errorf("cgroupCPUDir = %q, want %q", got, want)
	}
}

func TestCgroupCPUDir_Jailer_v1(t *testing.T) {
	p := &Provider{
		config:        Config{EnableJailer: true},
		cgroupVersion: "1",
	}
	got := p.cgroupCPUDir("vm-x")
	want := "/sys/fs/cgroup/cpu/firecracker/vm-x"
	if got != want {
		t.Errorf("cgroupCPUDir = %q, want %q", got, want)
	}
}

func TestWriteCPUMax_v2(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{cgroupVersion: "2"}
	if err := p.writeCPUMax(dir, 200000, 100000); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "cpu.max"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "200000 100000" {
		t.Errorf("cpu.max = %q, want %q", string(got), "200000 100000")
	}
}

func TestWriteCPUMax_v1(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{cgroupVersion: "1"}
	if err := p.writeCPUMax(dir, 200000, 100000); err != nil {
		t.Fatal(err)
	}
	quota, err := os.ReadFile(filepath.Join(dir, "cpu.cfs_quota_us"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(quota)) != "200000" {
		t.Errorf("cpu.cfs_quota_us = %q", string(quota))
	}
	period, err := os.ReadFile(filepath.Join(dir, "cpu.cfs_period_us"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(period)) != "100000" {
		t.Errorf("cpu.cfs_period_us = %q", string(period))
	}
}

func TestCgroupCPUDir_NonJailer_v1_NestedRoot(t *testing.T) {
	// Operators may put navaris under a team/group subtree to integrate
	// with their host's cgroup layout. The v1 path must preserve nested
	// segments under /sys/fs/cgroup/, not flatten via filepath.Base.
	p := &Provider{
		config:        Config{CgroupRoot: "/sys/fs/cgroup/team/navaris-fc", EnableJailer: false},
		cgroupVersion: "1",
	}
	got := p.cgroupCPUDir("vm-x")
	want := "/sys/fs/cgroup/cpu/team/navaris-fc/vm-x"
	if got != want {
		t.Errorf("cgroupCPUDir = %q, want %q", got, want)
	}
}

// TestCgroupCPUDir_NonJailer_v1_UnifiedRoot guards the edge case where
// CgroupRoot is exactly "/sys/fs/cgroup" (no trailing slash). Without
// the special case, strings.TrimPrefix would leave it unchanged and we'd
// build the bogus path /sys/fs/cgroup/cpu/sys/fs/cgroup/<vm>.
func TestCgroupCPUDir_NonJailer_v1_UnifiedRoot(t *testing.T) {
	p := &Provider{
		config:        Config{CgroupRoot: "/sys/fs/cgroup", EnableJailer: false},
		cgroupVersion: "1",
	}
	got := p.cgroupCPUDir("vm-x")
	want := "/sys/fs/cgroup/cpu/vm-x"
	if got != want {
		t.Errorf("cgroupCPUDir = %q, want %q", got, want)
	}
}

func TestSetupCgroup_NonJailer_WritesQuota(t *testing.T) {
	root := t.TempDir()
	p := &Provider{
		config:            Config{CgroupRoot: root, EnableJailer: false},
		cgroupVersion:     "2",
		cgroupSkipFSCheck: true,
	}
	pid := os.Getpid()
	if err := p.setupCgroup(pid, "vm-test", 2); err != nil {
		t.Fatalf("setupCgroup: %v", err)
	}

	dir := filepath.Join(root, "vm-test")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cgroup dir missing: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "cpu.max"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "200000 100000" {
		t.Errorf("cpu.max = %q, want %q", string(got), "200000 100000")
	}

	procs, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(procs)) != strconv.Itoa(pid) {
		t.Errorf("cgroup.procs = %q, want %d", string(procs), pid)
	}
}

// TestSetupCgroup_NonCgroupRoot_Errors guards the most likely production
// misconfiguration: --firecracker-cgroup-root pointed at a regular dir
// (e.g. /tmp/nav-cg). Without verifyCgroupFS, setupCgroup would mkdir,
// drop a "cpu.max" plain file, and report success — leaving CPU
// enforcement entirely off while the daemon believes it's active.
func TestSetupCgroup_NonCgroupRoot_Errors(t *testing.T) {
	root := t.TempDir() // tmpfs, NOT cgroupfs
	p := &Provider{
		config:        Config{CgroupRoot: root, EnableJailer: false},
		cgroupVersion: "2",
		// cgroupSkipFSCheck NOT set — we want the validation to fire.
	}
	err := p.setupCgroup(os.Getpid(), "vm-misconfigured", 2)
	if err == nil {
		t.Fatal("expected error from non-cgroup root, got nil")
	}
	if !strings.Contains(err.Error(), "cgroup") {
		t.Errorf("error %q should mention cgroup", err.Error())
	}
}

// TestSetupCgroup_NonExistentRoot_Errors guards the case where the
// operator points --firecracker-cgroup-root at a path that doesn't even
// exist yet (e.g. typo in /tmp/navaris-fc). MkdirAll would happily
// create the path under /tmp/, so the validation must walk up to the
// nearest EXISTING ancestor and check IT is a cgroup mount.
func TestSetupCgroup_NonExistentRoot_Errors(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	p := &Provider{
		config:        Config{CgroupRoot: root, EnableJailer: false},
		cgroupVersion: "2",
	}
	err := p.setupCgroup(os.Getpid(), "vm-typoed", 2)
	if err == nil {
		t.Fatal("expected error from non-existent non-cgroup root, got nil")
	}
}

func TestSetupCgroup_Jailer_NoOp(t *testing.T) {
	p := &Provider{
		config:        Config{EnableJailer: true},
		cgroupVersion: "2",
	}
	if err := p.setupCgroup(os.Getpid(), "vm-test", 2); err != nil {
		t.Errorf("setupCgroup with jailer should be no-op, got: %v", err)
	}
}

func TestRemoveCgroup_NonJailer_RemovesDir(t *testing.T) {
	root := t.TempDir()
	p := &Provider{
		config:        Config{CgroupRoot: root, EnableJailer: false},
		cgroupVersion: "2",
	}
	dir := filepath.Join(root, "vm-rm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := p.removeCgroup("vm-rm"); err != nil {
		t.Fatalf("removeCgroup: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("dir still exists: err=%v", err)
	}
}

func TestRemoveCgroup_Missing_NoError(t *testing.T) {
	root := t.TempDir()
	p := &Provider{
		config:        Config{CgroupRoot: root, EnableJailer: false},
		cgroupVersion: "2",
	}
	if err := p.removeCgroup("vm-never-existed"); err != nil {
		t.Errorf("removeCgroup on missing dir should not error, got: %v", err)
	}
}

func TestRemoveCgroup_Jailer_NoOp(t *testing.T) {
	p := &Provider{
		config:        Config{EnableJailer: true},
		cgroupVersion: "2",
	}
	if err := p.removeCgroup("vm-jailer"); err != nil {
		t.Errorf("removeCgroup with jailer should be no-op, got: %v", err)
	}
}

// TestEffectiveCPULimit_Backfill guards against legacy vminfo.json records
// (written before this spec) where LimitCPU is unset (zero). Without the
// backfill, the cgroup quota would be computed as 0 * period = 0, which
// hard-throttles the VM to no CPU at all at boot — a 100% regression for
// any sandbox that survived a daemon upgrade.
func TestEffectiveCPULimit_Backfill(t *testing.T) {
	p := &Provider{}
	cases := []struct {
		name string
		info *VMInfo
		want int64
	}{
		{"LimitCPU set", &VMInfo{LimitCPU: 4, CeilingCPU: 8, VcpuCount: 8}, 4},
		{"only CeilingCPU set (legacy fallback)", &VMInfo{CeilingCPU: 4, VcpuCount: 4}, 4},
		{"only VcpuCount set (deeper legacy)", &VMInfo{VcpuCount: 2}, 2},
		{"all zero (safety floor)", &VMInfo{}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := p.effectiveCPULimit(tc.info)
			if got != tc.want {
				t.Errorf("effectiveCPULimit = %d, want %d", got, tc.want)
			}
		})
	}
}

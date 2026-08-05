//go:build firecracker

package firecracker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
)

func seedResizeVM(t *testing.T, p *Provider, info *VMInfo) {
	t.Helper()
	if p.vms == nil {
		p.vms = make(map[string]*VMInfo)
	}
	if p.fileMu == nil {
		p.fileMu = make(map[string]*vmFileLock)
	}
	if err := os.MkdirAll(p.vmDir(info.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := info.Write(p.vmInfoPath(info.ID)); err != nil {
		t.Fatal(err)
	}
	p.vms[info.ID] = info
	p.fileMu[info.ID] = &vmFileLock{}
}

func TestUpdateResources_CPU_AppliedViaCgroup(t *testing.T) {
	tmp := t.TempDir()
	p := &Provider{
		config: Config{
			CgroupRoot:   tmp,
			ChrootBase:   tmp,
			EnableJailer: false,
		},
		cgroupVersion:     "2",
		cgroupSkipFSCheck: true, // tempdir is tmpfs, not cgroupfs
	}
	seedResizeVM(t, p, &VMInfo{
		ID:           "vm-cpu",
		PID:          os.Getpid(),
		LimitCPU:     1,
		CeilingCPU:   4,
		CgroupActive: true,
	})
	// Pre-create the cgroup directory like setupCgroup would have at boot
	// (we're skipping setupCgroup here and testing the resize path
	// in isolation).
	cgDir := filepath.Join(tmp, "vm-cpu")
	if err := os.MkdirAll(cgDir, 0755); err != nil {
		t.Fatal(err)
	}
	// vmInfoPath() uses ChrootBase/<vmID>/vminfo.json — same dir.

	cpu := 2
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-cpu"},
		domain.UpdateResourcesRequest{CPULimit: &cpu})
	if err != nil {
		t.Fatalf("UpdateResources: %v", err)
	}

	// Verify cpu.max was written.
	got, err := os.ReadFile(filepath.Join(cgDir, "cpu.max"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "200000 100000" {
		t.Errorf("cpu.max = %q, want %q", string(got), "200000 100000")
	}

	// Verify VMInfo.LimitCPU was updated.
	if p.vms["vm-cpu"].LimitCPU != 2 {
		t.Errorf("LimitCPU = %d, want 2", p.vms["vm-cpu"].LimitCPU)
	}
}

func TestUpdateResources_CPU_ExceedsCeiling(t *testing.T) {
	tmp := t.TempDir()
	p := &Provider{
		config:            Config{CgroupRoot: tmp, ChrootBase: tmp, EnableJailer: false},
		cgroupVersion:     "2",
		cgroupSkipFSCheck: true,
	}
	seedResizeVM(t, p, &VMInfo{
		ID:           "vm-c",
		PID:          os.Getpid(),
		LimitCPU:     1,
		CeilingCPU:   2,
		CgroupActive: true,
	})
	cpu := 4 // > ceiling of 2
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-c"},
		domain.UpdateResourcesRequest{CPULimit: &cpu})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) {
		t.Fatalf("expected ProviderResizeError, got %T: %v", err, err)
	}
	if prErr.Reason != domain.ResizeReasonExceedsCeiling {
		t.Errorf("reason = %q, want %q", prErr.Reason, domain.ResizeReasonExceedsCeiling)
	}
}

func TestUpdateResources_CPU_NoCgroup_Unavailable(t *testing.T) {
	tmp := t.TempDir()
	p := &Provider{
		config:            Config{ChrootBase: tmp},
		cgroupVersion:     "2",
		cgroupSkipFSCheck: true,
	}
	seedResizeVM(t, p, &VMInfo{
		ID:           "vm-nc",
		LimitCPU:     1,
		CeilingCPU:   4,
		CgroupActive: false, // setup failed at boot
	})
	cpu := 2
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-nc"},
		domain.UpdateResourcesRequest{CPULimit: &cpu})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) {
		t.Fatalf("expected ProviderResizeError, got %T", err)
	}
	if prErr.Reason != domain.ResizeReasonCgroupUnavailable {
		t.Errorf("reason = %q, want %q", prErr.Reason, domain.ResizeReasonCgroupUnavailable)
	}
}

func TestUpdateResources_FC_MemoryAboveCeiling(t *testing.T) {
	tmp := t.TempDir()
	p := &Provider{
		config: Config{ChrootBase: tmp, VcpuHeadroomMult: 2.0, MemHeadroomMult: 2.0},
	}
	seedResizeVM(t, p, &VMInfo{
		ID: "vm-1", LimitMemMib: 256, CeilingMemMib: 512, MemSizeMib: 512,
	})
	mem := 1024 // above ceiling 512
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-1"},
		domain.UpdateResourcesRequest{MemoryLimitMB: &mem})
	var prErr *domain.ProviderResizeError
	if !errors.As(err, &prErr) || prErr.Reason != domain.ResizeReasonExceedsCeiling {
		t.Fatalf("err = %v, want ProviderResizeError(exceeds_ceiling)", err)
	}
}

func TestUpdateResources_FC_VMNotFound(t *testing.T) {
	p := &Provider{
		config: Config{ChrootBase: t.TempDir()},
		vms:    map[string]*VMInfo{},
		fileMu: map[string]*vmFileLock{},
	}
	mem := 256
	err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "missing"},
		domain.UpdateResourcesRequest{MemoryLimitMB: &mem})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateResources_PreservesPublishedPortState(t *testing.T) {
	oldAdd, oldRemove := addDNATFn, removeDNATFn
	defer func() { addDNATFn, removeDNATFn = oldAdd, oldRemove }()

	var added, removed []int
	addDNATFn = func(port int, _ string, _ int) error {
		added = append(added, port)
		return nil
	}
	removeDNATFn = func(port int, _ string, _ int) {
		removed = append(removed, port)
	}

	tmp := t.TempDir()
	p := &Provider{
		config:        Config{CgroupRoot: tmp, ChrootBase: tmp},
		cgroupVersion: "2", cgroupSkipFSCheck: true,
		subnets: network.NewAllocator(), portAlloc: network.NewPortAllocator(),
	}
	seed := &VMInfo{
		ID: "vm-port-resize", PID: os.Getpid(), TapDevice: "tap-test",
		LimitCPU: 1, CeilingCPU: 4, CgroupActive: true, Ports: map[int]int{},
	}
	seedResizeVM(t, p, seed)

	ep, err := p.PublishPort(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: seed.ID}, 8080,
		domain.PublishPortOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !p.portAlloc.InUse(ep.PublishedPort) {
		t.Fatalf("allocated port %d is not owned", ep.PublishedPort)
	}

	cpu := 2
	if err := p.UpdateResources(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: seed.ID},
		domain.UpdateResourcesRequest{CPULimit: &cpu}); err != nil {
		t.Fatal(err)
	}

	disk, err := ReadVMInfo(p.vmInfoPath(seed.ID))
	if err != nil {
		t.Fatal(err)
	}
	if got := disk.Ports[ep.PublishedPort]; got != 8080 {
		t.Fatalf("disk port mapping = %d; want 8080", got)
	}
	if got := p.vms[seed.ID].Ports[ep.PublishedPort]; got != 8080 {
		t.Fatalf("cache port mapping = %d; want 8080", got)
	}

	if err := p.UnpublishPort(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: seed.ID},
		ep.PublishedPort); err != nil {
		t.Fatal(err)
	}
	if p.portAlloc.InUse(ep.PublishedPort) {
		t.Fatalf("port %d still owned after UnpublishPort", ep.PublishedPort)
	}
	if len(added) != 1 || len(removed) != 1 || added[0] != removed[0] {
		t.Fatalf("DNAT add/remove mismatch: added=%v removed=%v", added, removed)
	}
}

type cpuApplyGate struct {
	quota   int64
	release chan struct{}
}

func TestUpdateResources_WaitsForPortMutationBeforeLiveApply(t *testing.T) {
	oldAdd, oldRemove := addDNATFn, removeDNATFn
	defer func() { addDNATFn, removeDNATFn = oldAdd, oldRemove }()

	addStarted := make(chan struct{})
	releaseAdd := make(chan struct{})
	addDNATFn = func(int, string, int) error {
		close(addStarted)
		<-releaseAdd
		return nil
	}
	removeDNATFn = func(int, string, int) {}

	tmp := t.TempDir()
	p := &Provider{
		config:        Config{CgroupRoot: tmp, ChrootBase: tmp},
		cgroupVersion: "2", cgroupSkipFSCheck: true,
		subnets: network.NewAllocator(), portAlloc: network.NewPortAllocator(),
	}
	seed := &VMInfo{ID: "vm-port-lock", PID: os.Getpid(), TapDevice: "tap-test",
		LimitCPU: 1, CeilingCPU: 4, CgroupActive: true, Ports: map[int]int{}}
	seedResizeVM(t, p, seed)

	publishDone := make(chan error, 1)
	go func() {
		_, err := p.PublishPort(context.Background(),
			domain.BackendRef{Backend: "firecracker", Ref: seed.ID}, 8080,
			domain.PublishPortOptions{})
		publishDone <- err
	}()
	<-addStarted // PublishPort holds fl.mu here.

	cpu := 2
	resizeDone := make(chan error, 1)
	go func() {
		resizeDone <- p.UpdateResources(context.Background(),
			domain.BackendRef{Backend: "firecracker", Ref: seed.ID},
			domain.UpdateResourcesRequest{CPULimit: &cpu})
	}()

	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(tmp, seed.ID, "cpu.max")); !os.IsNotExist(err) {
		t.Fatalf("resize applied before acquiring per-VM lock; cpu.max err=%v", err)
	}
	close(releaseAdd)
	if err := <-publishDone; err != nil {
		t.Fatal(err)
	}
	if err := <-resizeDone; err != nil {
		t.Fatal(err)
	}
}

func TestUpdateResources_SerializesLiveApplyAndCommit(t *testing.T) {
	oldWrite := resizeWriteCPUMax
	defer func() { resizeWriteCPUMax = oldWrite }()

	entered := make(chan cpuApplyGate, 2)
	resizeWriteCPUMax = func(_ *Provider, _ string, quota, _ int64) error {
		gate := cpuApplyGate{quota: quota, release: make(chan struct{})}
		entered <- gate
		<-gate.release
		return nil
	}

	tmp := t.TempDir()
	p := &Provider{config: Config{CgroupRoot: tmp, ChrootBase: tmp},
		cgroupVersion: "2", cgroupSkipFSCheck: true}
	seed := &VMInfo{ID: "vm-resize-order", PID: os.Getpid(), LimitCPU: 1,
		CeilingCPU: 4, CgroupActive: true}
	seedResizeVM(t, p, seed)

	cpuA, cpuB := 2, 3
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	go func() {
		doneA <- p.UpdateResources(context.Background(),
			domain.BackendRef{Backend: "firecracker", Ref: seed.ID},
			domain.UpdateResourcesRequest{CPULimit: &cpuA})
	}()
	gateA := <-entered

	go func() {
		doneB <- p.UpdateResources(context.Background(),
			domain.BackendRef{Backend: "firecracker", Ref: seed.ID},
			domain.UpdateResourcesRequest{CPULimit: &cpuB})
	}()
	select {
	case gateB := <-entered:
		close(gateB.release)
		close(gateA.release)
		t.Fatal("second resize entered live apply before first committed")
	case <-time.After(50 * time.Millisecond):
	}

	close(gateA.release)
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}
	gateB := <-entered
	close(gateB.release)
	if err := <-doneB; err != nil {
		t.Fatal(err)
	}

	disk, err := ReadVMInfo(p.vmInfoPath(seed.ID))
	if err != nil {
		t.Fatal(err)
	}
	if disk.LimitCPU != 3 || p.vms[seed.ID].LimitCPU != 3 {
		t.Fatalf("final limits disk=%d cache=%d; want 3",
			disk.LimitCPU, p.vms[seed.ID].LimitCPU)
	}
}

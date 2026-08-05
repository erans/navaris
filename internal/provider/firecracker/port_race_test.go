//go:build firecracker

package firecracker

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
)

// TestPublishPort_Concurrent_NoLostUpdate: N concurrent PublishPort calls
// on one VM must produce exactly N distinct port mappings in vminfo.json.
// Without lockFor, the unlocked read-modify-write loses updates (last
// writer wins, so entries vanish). The iptables path is stubbed via the
// package-level addDNATFn/removeDNATFn vars (introduced in Step 3) so the
// test needs no iptables or root.
func TestPublishPort_Concurrent_NoLostUpdate(t *testing.T) {
	addDNATFn = func(int, string, int) error { return nil }
	removeDNATFn = func(int, string, int) {}
	defer func() {
		addDNATFn = network.AddDNAT
		removeDNATFn = network.RemoveDNAT
	}()

	dir := t.TempDir()
	p := &Provider{
		config:    Config{ChrootBase: dir, EnableJailer: false},
		subnets:   network.NewAllocator(),
		portAlloc: network.NewPortAllocator(),
		vms:       map[string]*VMInfo{},
		fileMu:    map[string]*vmFileLock{},
	}
	seed := &VMInfo{ID: "vm-race", PID: os.Getpid(), TapDevice: "fc-race", SubnetIdx: 0, Ports: map[int]int{}}
	if err := os.MkdirAll(p.vmDir("vm-race"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := seed.Write(p.vmInfoPath("vm-race")); err != nil {
		t.Fatal(err)
	}
	p.vmMu.Lock()
	p.vms["vm-race"] = seed
	p.fileMu["vm-race"] = &vmFileLock{}
	p.vmMu.Unlock()

	const N = 25
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			if _, err := p.PublishPort(context.Background(),
				domain.BackendRef{Backend: "firecracker", Ref: "vm-race"},
				4000+i, domain.PublishPortOptions{}); err != nil {
				t.Errorf("PublishPort %d: %v", i, err)
			}
		}()
	}
	wg.Wait()

	info, err := ReadVMInfo(p.vmInfoPath("vm-race"))
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Ports) != N {
		t.Fatalf("info.Ports has %d entries; want %d (lost-update race)", len(info.Ports), N)
	}
}

// TestPublishPort_AfterStopSentinel_FailsFast: once StopSandbox has flipped
// the stopped sentinel, a subsequent PublishPort on that VM must fail fast
// with ErrVMStopped (not block, not succeed). The sentinel is set directly
// here to simulate the instant StopSandbox begins; this deterministically
// tests that PublishPort consults lockFor's fast path. No real StopSandbox
// is invoked, so there is no signal/graceful-wait risk.
func TestPublishPort_AfterStopSentinel_FailsFast(t *testing.T) {
	addDNATFn = func(int, string, int) error { return nil }
	defer func() { addDNATFn = network.AddDNAT }()

	dir := t.TempDir()
	p := &Provider{
		config:    Config{ChrootBase: dir, EnableJailer: false},
		subnets:   network.NewAllocator(),
		portAlloc: network.NewPortAllocator(),
		vms:       map[string]*VMInfo{},
		fileMu:    map[string]*vmFileLock{},
	}
	seed := &VMInfo{ID: "vm-s", PID: os.Getpid(), TapDevice: "fc-s", SubnetIdx: 0, Ports: map[int]int{}}
	if err := os.MkdirAll(p.vmDir("vm-s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := seed.Write(p.vmInfoPath("vm-s")); err != nil {
		t.Fatal(err)
	}
	p.vmMu.Lock()
	p.vms["vm-s"] = seed
	// Simulate StopSandbox having just flipped the sentinel.
	fl := &vmFileLock{}
	fl.stopped.Store(true)
	p.fileMu["vm-s"] = fl
	p.vmMu.Unlock()

	_, err := p.PublishPort(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-s"},
		41000, domain.PublishPortOptions{})
	if !errors.Is(err, domain.ErrVMStopped) {
		t.Fatalf("PublishPort err = %v; want ErrVMStopped", err)
	}
}

// TestStopSandbox_CleansUpFileMuEntry: with a dead VM (PID=0 so the graceful
// block is skipped; no TapDevice/Ports so network+port cleanup is skipped),
// StopSandbox must delete the p.vms and p.fileMu entries for the VM. No
// signals are sent (PID is not > 0), so the test is safe.
func TestStopSandbox_CleansUpFileMuEntry(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{
		config: Config{ChrootBase: dir, EnableJailer: false},
		vms:    map[string]*VMInfo{},
		fileMu: map[string]*vmFileLock{},
	}
	seed := &VMInfo{ID: "vm-d", PID: 0, SubnetIdx: 0, Ports: map[int]int{}}
	if err := os.MkdirAll(p.vmDir("vm-d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := seed.Write(p.vmInfoPath("vm-d")); err != nil {
		t.Fatal(err)
	}
	p.vmMu.Lock()
	p.vms["vm-d"] = seed
	p.fileMu["vm-d"] = &vmFileLock{}
	p.vmMu.Unlock()

	if err := p.StopSandbox(context.Background(),
		domain.BackendRef{Backend: "firecracker", Ref: "vm-d"}, false); err != nil {
		t.Fatal(err)
	}

	p.vmMu.Lock()
	_, vmsHas := p.vms["vm-d"]
	_, fmHas := p.fileMu["vm-d"]
	p.vmMu.Unlock()
	if vmsHas {
		t.Error("p.vms[vm-d] not deleted by StopSandbox")
	}
	if fmHas {
		t.Error("p.fileMu[vm-d] not deleted by StopSandbox")
	}

	got, err := ReadVMInfo(p.vmInfoPath("vm-d"))
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != 0 {
		t.Errorf("vminfo PID = %d; want 0 (ClearRuntime)", got.PID)
	}
}

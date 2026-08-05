//go:build firecracker

package firecracker

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
)

func TestStartSandbox_AcquiresVMLockBeforeReading(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{
		config: Config{ChrootBase: dir, EnableJailer: false},
		fileMu: map[string]*vmFileLock{},
	}
	vmID := "vm-start-lock"
	seed := &VMInfo{ID: vmID, Ports: map[int]int{}}
	if err := os.MkdirAll(p.vmDir(vmID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := seed.Write(p.vmInfoPath(vmID)); err != nil {
		t.Fatal(err)
	}

	fl := &vmFileLock{}
	fl.mu.Lock()
	locked := true
	defer func() {
		if locked {
			fl.mu.Unlock()
		}
	}()
	p.vmMu.Lock()
	p.fileMu[vmID] = fl
	p.vmMu.Unlock()

	events := make(chan string, 2)
	readErr := errors.New("start read sentinel")
	oldRead := startReadVMInfo
	startReadVMInfo = func(string) (*VMInfo, error) {
		events <- "read"
		return nil, readErr
	}
	defer func() { startReadVMInfo = oldRead }()
	p.lockForAfterFastCheckHook = func() { events <- "lock" }

	done := make(chan error, 1)
	go func() {
		done <- p.StartSandbox(context.Background(), domain.BackendRef{Backend: backendName, Ref: vmID})
	}()

	first := recvResizeTest(t, "first StartSandbox ordering event", events)
	if first != "lock" {
		err := recvResizeTest(t, "StartSandbox completion", done)
		if !errors.Is(err, readErr) {
			t.Fatalf("StartSandbox error = %v; want read sentinel after first event %q", err, first)
		}
		t.Fatalf("StartSandbox read vminfo before acquiring VM lock; first event = %q", first)
	}

	fl.mu.Unlock()
	locked = false
	err := recvResizeTest(t, "StartSandbox completion", done)
	if !errors.Is(err, readErr) {
		t.Fatalf("StartSandbox error = %v; want read sentinel", err)
	}
}

func TestStopSandbox_FinalWriteFailureRetainsStateForRetry(t *testing.T) {
	dir := t.TempDir()
	p := &Provider{
		config: Config{ChrootBase: dir, EnableJailer: false},
		vms:    map[string]*VMInfo{},
		fileMu: map[string]*vmFileLock{},
	}
	vmID := "vm-stop-retry"
	seed := &VMInfo{ID: vmID, PID: 0, SubnetIdx: 0, Ports: map[int]int{}}
	if err := os.MkdirAll(p.vmDir(vmID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := seed.Write(p.vmInfoPath(vmID)); err != nil {
		t.Fatal(err)
	}
	fl := &vmFileLock{}
	p.vmMu.Lock()
	p.vms[vmID] = seed
	p.fileMu[vmID] = fl
	p.vmMu.Unlock()

	writeErr := errors.New("final write failed")
	oldOpenDir := vminfoOpenDir
	vminfoOpenDir = func(string) (*os.File, error) { return nil, writeErr }
	defer func() { vminfoOpenDir = oldOpenDir }()

	err := p.StopSandbox(context.Background(), domain.BackendRef{Backend: backendName, Ref: vmID}, true)
	if !errors.Is(err, writeErr) {
		t.Fatalf("StopSandbox error = %v; want final write error", err)
	}
	p.vmMu.Lock()
	cached := p.vms[vmID]
	gotFL := p.fileMu[vmID]
	p.vmMu.Unlock()
	if cached == nil || gotFL != fl || !fl.stopped.Load() {
		t.Fatalf("failed final commit discarded retry state: cached=%v gotFL=%p wantFL=%p stopped=%v",
			cached != nil, gotFL, fl, fl.stopped.Load())
	}

	vminfoOpenDir = oldOpenDir
	if err := p.StopSandbox(context.Background(), domain.BackendRef{Backend: backendName, Ref: vmID}, true); err != nil {
		t.Fatalf("StopSandbox retry: %v", err)
	}
	p.vmMu.Lock()
	_, vmsHas := p.vms[vmID]
	_, fmHas := p.fileMu[vmID]
	p.vmMu.Unlock()
	if vmsHas || fmHas {
		t.Fatalf("StopSandbox retry left entries: vms=%v fileMu=%v", vmsHas, fmHas)
	}
}

func TestCleanupStartFailure_KillFailureUsesFallbackAndAggregates(t *testing.T) {
	p := &Provider{config: Config{EnableJailer: true}}
	vmID := "vm-cleanup-kill"
	killErr := errors.New("kill failed")
	fallbackErr := errors.New("fallback failed")

	oldKill := startKillProcess
	defer func() { startKillProcess = oldKill }()
	startKillProcess = func(pid int) error {
		if pid != 1234 {
			t.Fatalf("startKillProcess pid = %d; want 1234", pid)
		}
		return killErr
	}

	fallbackCalls := 0
	err := p.cleanupStartFailure(vmID, 1234, func() error {
		fallbackCalls++
		return fallbackErr
	}, "", 0)
	if fallbackCalls != 1 {
		t.Fatalf("fallback calls = %d; want 1", fallbackCalls)
	}
	if !errors.Is(err, killErr) || !errors.Is(err, fallbackErr) {
		t.Fatalf("cleanup error = %v; want kill and fallback errors", err)
	}

	startKillProcess = func(pid int) error {
		if pid != 5678 {
			t.Fatalf("startKillProcess pid = %d; want 5678", pid)
		}
		return syscall.ESRCH
	}
	fallbackCalls = 0
	err = p.cleanupStartFailure(vmID, 5678, func() error {
		fallbackCalls++
		return nil
	}, "", 0)
	if err != nil {
		t.Fatalf("cleanup ESRCH error = %v; want nil", err)
	}
	if fallbackCalls != 0 {
		t.Fatalf("fallback calls for ESRCH = %d; want 0", fallbackCalls)
	}
}

func TestCleanupStartFailure_MissingPIDUsesMachineFallback(t *testing.T) {
	p := &Provider{config: Config{EnableJailer: true}}
	fallbackErr := errors.New("fallback failed")

	oldKill := startKillProcess
	defer func() { startKillProcess = oldKill }()
	startKillProcess = func(pid int) error {
		t.Fatalf("startKillProcess called for missing PID %d", pid)
		return nil
	}

	fallbackCalls := 0
	err := p.cleanupStartFailure("vm-cleanup-missing-pid", 0, func() error {
		fallbackCalls++
		return fallbackErr
	}, "", 0)
	if fallbackCalls != 1 {
		t.Fatalf("fallback calls = %d; want 1", fallbackCalls)
	}
	if !errors.Is(err, fallbackErr) {
		t.Fatalf("cleanup error = %v; want fallback error", err)
	}
}

func TestCommitStartedVM_WriteFailureTerminatesAndSkipsCache(t *testing.T) {
	terminationErr := errors.New("termination failed")
	cases := []struct {
		operation string
		killErr   error
	}{
		{operation: "start"},
		{operation: "snapshot", killErr: terminationErr},
	}

	for _, tc := range cases {
		t.Run(tc.operation, func(t *testing.T) {
			dir := t.TempDir()
			p := &Provider{
				config: Config{ChrootBase: dir, EnableJailer: true},
				vms:    map[string]*VMInfo{},
			}
			vmID := "vm-commit-" + tc.operation
			if err := os.MkdirAll(p.vmDir(vmID), 0o755); err != nil {
				t.Fatal(err)
			}

			writeErr := errors.New("write failed " + tc.operation)
			oldOpenDir := vminfoOpenDir
			vminfoOpenDir = func(string) (*os.File, error) { return nil, writeErr }
			defer func() { vminfoOpenDir = oldOpenDir }()

			oldKill := startKillProcess
			defer func() { startKillProcess = oldKill }()
			killCalls := 0
			startKillProcess = func(pid int) error {
				killCalls++
				if pid != 4321 {
					t.Fatalf("startKillProcess pid = %d; want 4321", pid)
				}
				return tc.killErr
			}

			fallbackCalls := 0
			info := &VMInfo{ID: vmID, PID: 4321, Ports: map[int]int{}}
			err := p.commitStartedVM(tc.operation, vmID, p.vmInfoPath(vmID), info, 4321, func() error {
				fallbackCalls++
				return nil
			}, "", 0)
			if !errors.Is(err, writeErr) {
				t.Fatalf("commitStartedVM error = %v; want write error", err)
			}
			if !strings.Contains(err.Error(), "firecracker persist "+tc.operation+" runtime "+vmID) {
				t.Fatalf("commitStartedVM error = %q; want operation label %q", err, tc.operation)
			}
			if killCalls != 1 {
				t.Fatalf("startKillProcess calls = %d; want 1", killCalls)
			}
			if tc.killErr != nil {
				if !errors.Is(err, tc.killErr) {
					t.Fatalf("commitStartedVM error = %v; want termination error", err)
				}
				if fallbackCalls != 1 {
					t.Fatalf("fallback calls = %d; want 1", fallbackCalls)
				}
			} else if fallbackCalls != 0 {
				t.Fatalf("fallback calls = %d; want 0", fallbackCalls)
			}

			p.vmMu.Lock()
			_, cached := p.vms[vmID]
			p.vmMu.Unlock()
			if cached {
				t.Fatalf("p.vms[%s] registered after write failure", vmID)
			}
		})
	}
}

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

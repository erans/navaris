//go:build firecracker

package firecracker

import (
	"context"
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

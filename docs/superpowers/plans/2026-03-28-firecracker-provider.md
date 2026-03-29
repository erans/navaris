# Firecracker Provider (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Firecracker-based provider that implements `domain.Provider` with VM lifecycle, exec/sessions via a custom vsock guest agent, tap networking, and jailer isolation.

**Architecture:** New `internal/provider/firecracker/` package with sub-packages for vsock protocol, networking, and jailer config. A separate `cmd/navaris-agent/` binary runs inside VMs as a vsock-based exec agent. Provider selection in `cmd/navarisd/main.go` extended with `--firecracker-bin` flag.

**Tech Stack:** `firecracker-go-sdk`, `golang.org/x/sys/unix` (vsock), standard library for networking/iptables exec.

**Spec:** `docs/superpowers/specs/2026-03-28-firecracker-provider-design.md`

---

## File Structure

### New files (create)

| File | Responsibility |
|------|---------------|
| `internal/provider/firecracker/firecracker.go` | Provider struct, `New()`, `Health()`, config, crash recovery |
| `internal/provider/firecracker/sandbox.go` | `CreateSandbox`, `StartSandbox`, `StopSandbox`, `DestroySandbox`, `GetSandboxState` |
| `internal/provider/firecracker/exec.go` | `Exec`, `ExecDetached`, `AttachSession` — delegates to vsock client |
| `internal/provider/firecracker/stubs.go` | Phase 2 stubs returning `ErrNotImplemented` |
| `internal/provider/firecracker/vminfo.go` | `vmInfo` struct, JSON read/write, filesystem scanning |
| `internal/provider/firecracker/vminfo_test.go` | vmInfo persistence tests |
| `internal/provider/firecracker/vsock/protocol.go` | `Message` struct, encode/decode helpers |
| `internal/provider/firecracker/vsock/protocol_test.go` | Round-trip encoding tests |
| `internal/provider/firecracker/vsock/client.go` | Host-side vsock client: dial, exec, session multiplexing |
| `internal/provider/firecracker/vsock/client_test.go` | Client tests with mock vsock (Unix socket stand-in) |
| `internal/provider/firecracker/network/allocator.go` | `/30` subnet allocator from `172.26.0.0/16` |
| `internal/provider/firecracker/network/allocator_test.go` | Allocation, wrap-around, release tests |
| `internal/provider/firecracker/network/tap.go` | Tap device create/delete, iptables masquerade |
| `internal/provider/firecracker/network/tap_test.go` | Tap config generation tests (no root needed) |
| `internal/provider/firecracker/jailer/jailer.go` | Jailer config struct, UID allocator |
| `internal/provider/firecracker/jailer/jailer_test.go` | Config generation, UID allocation tests |
| `cmd/navaris-agent/main.go` | Guest agent entry point |
| `cmd/navaris-agent/agent/server.go` | vsock listener, request dispatcher |
| `cmd/navaris-agent/agent/exec.go` | Command execution (pipe-based and PTY) |
| `cmd/navaris-agent/agent/session.go` | Interactive shell sessions |
| `cmd/navaris-agent/agent/server_test.go` | Agent dispatch tests |
| `cmd/navaris-agent/agent/exec_test.go` | Exec handler tests |
| `cmd/navarisd/provider_firecracker.go` | `//go:build firecracker` — `newFirecrackerProvider()` |
| `cmd/navarisd/provider_firecracker_stub.go` | `//go:build !firecracker` — stub returning error |

### Modified files

| File | Change |
|------|--------|
| `internal/service/sandbox.go:21-53` | Add `defaultBackend string` field to `SandboxService`, accept in constructor |
| `internal/service/sandbox.go:75,119` | Replace hardcoded `"incus"` with `s.defaultBackend` |
| `internal/service/sandbox_test.go` | Update `newServiceEnv` to pass backend name |
| `cmd/navarisd/main.go:24-51,78-90` | Add Firecracker flags, extend provider selection switch |
| `go.mod` | Add `firecracker-go-sdk`, `golang.org/x/sys` |

---

### Task 1: Fix service layer Backend hardcoding

**Files:**
- Modify: `internal/service/sandbox.go:21-30,32-53,75,119`
- Modify: `internal/service/sandbox_test.go`
- Modify: `internal/service/image_test.go` (uses `newServiceEnv`)
- Modify: `cmd/navarisd/main.go:92-97`
- Modify: `cmd/navarisd/main_test.go`
- Modify: `internal/api/helpers_test.go`
- Modify: `internal/api/middleware_test.go`
- Modify: `pkg/client/client_test.go`

This is the prerequisite from the spec: the service layer hardcodes `Backend: "incus"`.

- [ ] **Step 1: Add `defaultBackend` to SandboxService**

In `internal/service/sandbox.go`, add `defaultBackend string` field to `SandboxService` struct and update `NewSandboxService` to accept it:

```go
type SandboxService struct {
	sandboxes      domain.SandboxStore
	snapshots      domain.SnapshotStore
	ops            domain.OperationStore
	ports          domain.PortBindingStore
	sessions       domain.SessionStore
	provider       domain.Provider
	events         domain.EventBus
	workers        *worker.Dispatcher
	defaultBackend string
}

func NewSandboxService(
	sandboxes domain.SandboxStore,
	snapshots domain.SnapshotStore,
	ops domain.OperationStore,
	ports domain.PortBindingStore,
	sessions domain.SessionStore,
	provider domain.Provider,
	events domain.EventBus,
	workers *worker.Dispatcher,
	defaultBackend string,
) *SandboxService {
	svc := &SandboxService{
		sandboxes:      sandboxes,
		snapshots:      snapshots,
		ops:            ops,
		ports:          ports,
		sessions:       sessions,
		provider:       provider,
		events:         events,
		workers:        workers,
		defaultBackend: defaultBackend,
	}
	svc.registerHandlers()
	return svc
}
```

- [ ] **Step 2: Replace hardcoded "incus" with `s.defaultBackend`**

In `Create()` (~line 75), change:
```go
Backend: "incus",
```
to:
```go
Backend: s.defaultBackend,
```

In `CreateFromSnapshot()` (~line 119), same change.

- [ ] **Step 3: Update all callers**

In `cmd/navarisd/main.go`, update the `NewSandboxService` call (~line 94) to pass the backend name. Determine it from the selected provider:

```go
// After provider selection, before service creation:
backendName := "mock"
if cfg.incusSocket != "" {
    backendName = "incus"
}

sbxSvc := service.NewSandboxService(
    store.SandboxStore(), store.SnapshotStore(), store.OperationStore(), store.PortBindingStore(),
    store.SessionStore(), prov, bus, disp, backendName,
)
```

- [ ] **Step 4: Update test helpers**

In `internal/service/sandbox_test.go`, update `newServiceEnv` to pass `"mock"`:

```go
sbxSvc := service.NewSandboxService(
    s.SandboxStore(), s.SnapshotStore(), s.OperationStore(), s.PortBindingStore(),
    s.SessionStore(), mock, bus, disp, "mock",
)
```

Update any other test files that call `NewSandboxService` (check `image_test.go`, `reconcile_test.go`, `cmd/navarisd/main_test.go`, `internal/api/helpers_test.go`, `internal/api/middleware_test.go`, `pkg/client/client_test.go`) — all need the new `"mock"` parameter appended.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/service/ -v -count=1`
Expected: All tests PASS

Run: `go test ./cmd/navarisd/ -v -count=1`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/service/sandbox.go internal/service/sandbox_test.go internal/service/image_test.go cmd/navarisd/main.go
git commit -m "refactor: make SandboxService backend name configurable

Replace hardcoded 'incus' backend with a defaultBackend field
passed during construction. Prerequisite for multi-provider support."
```

---

### Task 2: vsock protocol — shared message types

**Files:**
- Create: `internal/provider/firecracker/vsock/protocol.go`
- Create: `internal/provider/firecracker/vsock/protocol_test.go`

No build tag on these files — the protocol types are used by both the host-side client and the guest agent, and the protocol package has no Firecracker SDK dependency.

- [ ] **Step 1: Write protocol tests**

Create `internal/provider/firecracker/vsock/protocol_test.go`:

```go
package vsock

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	msg := Message{
		Version: ProtocolVersion,
		Type:    TypeExec,
		ID:      "abc-123",
		Payload: []byte(`{"command":["/bin/ls"]}`),
	}

	var buf bytes.Buffer
	if err := Encode(&buf, &msg); err != nil {
		t.Fatal(err)
	}

	got, err := Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != msg.Version {
		t.Errorf("version: got %d, want %d", got.Version, msg.Version)
	}
	if got.Type != msg.Type {
		t.Errorf("type: got %q, want %q", got.Type, msg.Type)
	}
	if got.ID != msg.ID {
		t.Errorf("id: got %q, want %q", got.ID, msg.ID)
	}
	if !bytes.Equal(got.Payload, msg.Payload) {
		t.Errorf("payload mismatch")
	}
}

func TestDecodeEmptyBuffer(t *testing.T) {
	var buf bytes.Buffer
	_, err := Decode(&buf)
	if err == nil {
		t.Error("expected error on empty buffer")
	}
}

func TestEncodeDecodeAllTypes(t *testing.T) {
	types := []string{
		TypePing, TypePong, TypeExec, TypeSession,
		TypeStdout, TypeStderr, TypeStdin,
		TypeExit, TypeResize, TypeSignal,
	}
	for _, typ := range types {
		msg := Message{Version: ProtocolVersion, Type: typ, ID: "test"}
		var buf bytes.Buffer
		if err := Encode(&buf, &msg); err != nil {
			t.Errorf("encode %s: %v", typ, err)
			continue
		}
		got, err := Decode(&buf)
		if err != nil {
			t.Errorf("decode %s: %v", typ, err)
			continue
		}
		if got.Type != typ {
			t.Errorf("type: got %q, want %q", got.Type, typ)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/firecracker/vsock/ -v -count=1`
Expected: FAIL (package doesn't exist yet)

- [ ] **Step 3: Implement protocol.go**

Create `internal/provider/firecracker/vsock/protocol.go`:

```go
package vsock

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const ProtocolVersion uint8 = 1

// Message types.
const (
	TypePing    = "ping"
	TypePong    = "pong"
	TypeExec    = "exec"
	TypeSession = "session"
	TypeStdout  = "stdout"
	TypeStderr  = "stderr"
	TypeStdin   = "stdin"
	TypeExit    = "exit"
	TypeResize  = "resize"
	TypeSignal  = "signal"
)

// Message is the wire format for all vsock communication between
// the host-side client and the guest agent.
type Message struct {
	Version uint8           `json:"v"`
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Encode writes a length-prefixed JSON message to w.
func Encode(w io.Writer, msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("vsock encode: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(data))); err != nil {
		return fmt.Errorf("vsock encode length: %w", err)
	}
	_, err = w.Write(data)
	return err
}

// Decode reads a length-prefixed JSON message from r.
func Decode(r io.Reader) (*Message, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("vsock decode length: %w", err)
	}
	if length > 4*1024*1024 { // 4MB sanity limit
		return nil, fmt.Errorf("vsock message too large: %d bytes", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("vsock decode body: %w", err)
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("vsock decode json: %w", err)
	}
	return &msg, nil
}

// ExecPayload is the payload for TypeExec messages.
type ExecPayload struct {
	Command []string          `json:"command"`
	Env     map[string]string `json:"env,omitempty"`
	WorkDir string            `json:"workdir,omitempty"`
}

// SessionPayload is the payload for TypeSession messages.
type SessionPayload struct {
	Shell string `json:"shell"`
}

// ExitPayload is the payload for TypeExit messages.
type ExitPayload struct {
	Code int `json:"code"`
}

// ResizePayload is the payload for TypeResize messages.
type ResizePayload struct {
	Width  int `json:"w"`
	Height int `json:"h"`
}

// SignalPayload is the payload for TypeSignal messages.
type SignalPayload struct {
	Signal string `json:"signal"`
}

// DataPayload is the payload for TypeStdout, TypeStderr, TypeStdin messages.
type DataPayload struct {
	Data []byte `json:"data"`
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/provider/firecracker/vsock/ -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/vsock/
git commit -m "feat(firecracker): add vsock protocol message types

Length-prefixed JSON wire format for host-agent communication.
Includes encode/decode helpers and typed payloads for exec,
session, resize, and signal operations."
```

---

### Task 3: Network — subnet allocator

**Files:**
- Create: `internal/provider/firecracker/network/allocator.go`
- Create: `internal/provider/firecracker/network/allocator_test.go`

- [ ] **Step 1: Write allocator tests**

Create `internal/provider/firecracker/network/allocator_test.go`:

```go
package network

import (
	"testing"
)

func TestAllocatorFirstSubnet(t *testing.T) {
	a := NewAllocator()
	idx := a.Allocate()
	ip := a.HostIP(idx)
	guest := a.GuestIP(idx)
	mask := a.Mask()

	if ip.String() != "172.26.0.1" {
		t.Errorf("host IP: got %s, want 172.26.0.1", ip)
	}
	if guest.String() != "172.26.0.2" {
		t.Errorf("guest IP: got %s, want 172.26.0.2", guest)
	}
	if mask.String() != "255.255.255.252" {
		t.Errorf("mask: got %s, want 255.255.255.252", mask)
	}
}

func TestAllocatorSequential(t *testing.T) {
	a := NewAllocator()
	idx0 := a.Allocate()
	idx1 := a.Allocate()
	idx2 := a.Allocate()

	if a.GuestIP(idx0).String() != "172.26.0.2" {
		t.Error("wrong guest IP for idx 0")
	}
	if a.GuestIP(idx1).String() != "172.26.0.6" {
		t.Error("wrong guest IP for idx 1")
	}
	if a.GuestIP(idx2).String() != "172.26.0.10" {
		t.Error("wrong guest IP for idx 2")
	}
}

func TestAllocatorRelease(t *testing.T) {
	a := NewAllocator()
	idx := a.Allocate()
	a.Release(idx)
	// After release, same index should not be reallocated immediately
	// (counter moves forward), but it's no longer in-use.
	if a.InUse(idx) {
		t.Error("expected idx released")
	}
}

func TestAllocatorInitPast(t *testing.T) {
	a := NewAllocator()
	a.InitPast(100)
	idx := a.Allocate()
	if idx <= 100 {
		t.Errorf("expected idx > 100, got %d", idx)
	}
}

func TestAllocatorBootArg(t *testing.T) {
	a := NewAllocator()
	idx := a.Allocate()
	arg := a.KernelBootArg(idx)
	expected := "ip=172.26.0.2::172.26.0.1:255.255.255.252::eth0:off"
	if arg != expected {
		t.Errorf("boot arg:\ngot  %s\nwant %s", arg, expected)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/firecracker/network/ -v -count=1`
Expected: FAIL

- [ ] **Step 3: Implement allocator.go**

Create `internal/provider/firecracker/network/allocator.go`:

```go
package network

import (
	"fmt"
	"net"
	"sync"
)

// Allocator manages /30 subnet allocation from 172.26.0.0/16.
// Each subnet provides one host IP (.1) and one guest IP (.2).
type Allocator struct {
	mu      sync.Mutex
	next    int
	inUse   map[int]bool
}

// NewAllocator returns a subnet allocator starting at index 0.
func NewAllocator() *Allocator {
	return &Allocator{inUse: make(map[int]bool)}
}

// InitPast sets the counter past the given index. Used on startup
// to skip subnets already in use by orphaned VMs.
func (a *Allocator) InitPast(idx int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if idx >= a.next {
		a.next = idx + 1
	}
}

// Allocate returns the next available subnet index.
func (a *Allocator) Allocate() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	idx := a.next
	a.next++
	a.inUse[idx] = true
	return idx
}

// Release marks a subnet index as no longer in use.
func (a *Allocator) Release(idx int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.inUse, idx)
}

// InUse returns whether a subnet index is currently allocated.
func (a *Allocator) InUse(idx int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inUse[idx]
}

// base returns the base address for a /30 block at the given index.
// Index 0 → 172.26.0.0, index 1 → 172.26.0.4, etc.
func base(idx int) net.IP {
	offset := idx * 4
	return net.IPv4(172, 26, byte(offset>>8), byte(offset&0xFF))
}

// HostIP returns the host-side IP (.1) for the given subnet index.
func (a *Allocator) HostIP(idx int) net.IP {
	ip := base(idx)
	ip[15]++ // .1
	return ip
}

// GuestIP returns the guest-side IP (.2) for the given subnet index.
func (a *Allocator) GuestIP(idx int) net.IP {
	ip := base(idx)
	ip[15] += 2 // .2
	return ip
}

// Mask returns the /30 subnet mask.
func (a *Allocator) Mask() net.IPMask {
	return net.CIDRMask(30, 32)
}

// KernelBootArg returns the kernel ip= parameter for the given subnet.
func (a *Allocator) KernelBootArg(idx int) string {
	return fmt.Sprintf("ip=%s::%s:%s::eth0:off",
		a.GuestIP(idx), a.HostIP(idx),
		net.IP(a.Mask()))
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/provider/firecracker/network/ -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/network/
git commit -m "feat(firecracker): add /30 subnet allocator for VM networking

Thread-safe allocator for 172.26.0.0/16 range. Each VM gets a /30
block with host (.1) and guest (.2) IPs. Supports crash recovery
via InitPast() and kernel boot arg generation."
```

---

### Task 4: Network — tap device and iptables management

**Files:**
- Create: `internal/provider/firecracker/network/tap.go`
- Create: `internal/provider/firecracker/network/tap_test.go`

- [ ] **Step 1: Write tap config tests**

Create `internal/provider/firecracker/network/tap_test.go`:

```go
package network

import (
	"testing"
)

func TestTapName(t *testing.T) {
	name := TapName("nvrs-fc-a1b2c3d4")
	if name != "fc-a1b2c3d4" {
		t.Errorf("got %q, want %q", name, "fc-a1b2c3d4")
	}
	// Verify IFNAMSIZ compliance (max 15 chars).
	if len(name) > 15 {
		t.Errorf("tap name %q exceeds IFNAMSIZ (15), len=%d", name, len(name))
	}
}

func TestTapNameFromShortID(t *testing.T) {
	name := TapName("nvrs-fc-ab")
	if name != "fc-ab" {
		t.Errorf("got %q, want %q", name, "fc-ab")
	}
}

func TestMasqueradeArgs(t *testing.T) {
	args := MasqueradeArgs("172.26.0.2", "eth0")
	expected := []string{
		"-t", "nat", "-A", "POSTROUTING",
		"-s", "172.26.0.2/32",
		"-o", "eth0",
		"-j", "MASQUERADE",
	}
	if len(args) != len(expected) {
		t.Fatalf("got %d args, want %d", len(args), len(expected))
	}
	for i, a := range args {
		if a != expected[i] {
			t.Errorf("arg[%d]: got %q, want %q", i, a, expected[i])
		}
	}
}

func TestDeleteMasqueradeArgs(t *testing.T) {
	args := DeleteMasqueradeArgs("172.26.0.2", "eth0")
	if args[2] != "-D" {
		t.Errorf("expected -D, got %q", args[2])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/firecracker/network/ -v -count=1 -run TestTap`
Expected: FAIL

- [ ] **Step 3: Implement tap.go**

Create `internal/provider/firecracker/network/tap.go`:

```go
package network

import (
	"fmt"
	"os/exec"
	"strings"
)

// TapName derives the tap device name from a VM ID.
// Result is "fc-" + last 8 chars of vmID, max 11 chars (within IFNAMSIZ=15).
func TapName(vmID string) string {
	suffix := vmID
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	return "fc-" + suffix
}

// CreateTap creates a tap device and configures it with the given host IP.
func CreateTap(name string, hostIP string, mask string) error {
	cmds := [][]string{
		{"ip", "tuntap", "add", "dev", name, "mode", "tap"},
		{"ip", "addr", "add", hostIP + "/30", "dev", name},
		{"ip", "link", "set", name, "up"},
	}
	for _, args := range cmds {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("tap %s: %s: %w: %s", name, strings.Join(args, " "), err, out)
		}
	}
	return nil
}

// DeleteTap removes a tap device.
func DeleteTap(name string) error {
	out, err := exec.Command("ip", "link", "del", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("delete tap %s: %w: %s", name, err, out)
	}
	return nil
}

// MasqueradeArgs returns iptables args to add a masquerade rule for a guest IP.
func MasqueradeArgs(guestIP string, hostIface string) []string {
	return []string{
		"-t", "nat", "-A", "POSTROUTING",
		"-s", guestIP + "/32",
		"-o", hostIface,
		"-j", "MASQUERADE",
	}
}

// DeleteMasqueradeArgs returns iptables args to remove a masquerade rule.
func DeleteMasqueradeArgs(guestIP string, hostIface string) []string {
	return []string{
		"-t", "nat", "-D", "POSTROUTING",
		"-s", guestIP + "/32",
		"-o", hostIface,
		"-j", "MASQUERADE",
	}
}

// AddMasquerade adds an iptables masquerade rule for a guest IP.
func AddMasquerade(guestIP string, hostIface string) error {
	args := MasqueradeArgs(guestIP, hostIface)
	out, err := exec.Command("iptables", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("add masquerade for %s: %w: %s", guestIP, err, out)
	}
	return nil
}

// RemoveMasquerade removes an iptables masquerade rule for a guest IP.
func RemoveMasquerade(guestIP string, hostIface string) error {
	args := DeleteMasqueradeArgs(guestIP, hostIface)
	out, err := exec.Command("iptables", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("remove masquerade for %s: %w: %s", guestIP, err, out)
	}
	return nil
}

// DetectDefaultInterface returns the host's default route interface.
func DetectDefaultInterface() (string, error) {
	out, err := exec.Command("ip", "route", "get", "1.1.1.1").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("detect default interface: %w: %s", err, out)
	}
	// Parse "1.1.1.1 via X.X.X.X dev eth0 ..."
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return "", fmt.Errorf("detect default interface: 'dev' not found in: %s", out)
}

// CheckIPForward returns an error if ip_forward is not enabled.
func CheckIPForward() error {
	out, err := exec.Command("sysctl", "-n", "net.ipv4.ip_forward").CombinedOutput()
	if err != nil {
		return fmt.Errorf("check ip_forward: %w: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "1" {
		return fmt.Errorf("net.ipv4.ip_forward is not enabled; run: sysctl -w net.ipv4.ip_forward=1")
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/provider/firecracker/network/ -v -count=1`
Expected: PASS (tests only exercise pure functions, not system commands)

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/network/tap.go internal/provider/firecracker/network/tap_test.go
git commit -m "feat(firecracker): add tap device and iptables management

Tap create/delete via ip commands, per-VM masquerade rules via
iptables, default interface detection, and ip_forward validation."
```

---

### Task 5: Jailer configuration

**Files:**
- Create: `internal/provider/firecracker/jailer/jailer.go`
- Create: `internal/provider/firecracker/jailer/jailer_test.go`

- [ ] **Step 1: Write jailer tests**

Create `internal/provider/firecracker/jailer/jailer_test.go`:

```go
package jailer

import (
	"testing"
)

func TestUIDAllocator(t *testing.T) {
	a := NewUIDAllocator(10000)
	uid0 := a.Allocate()
	uid1 := a.Allocate()
	if uid0 != 10000 {
		t.Errorf("first UID: got %d, want 10000", uid0)
	}
	if uid1 != 10001 {
		t.Errorf("second UID: got %d, want 10001", uid1)
	}
}

func TestUIDAllocatorInitPast(t *testing.T) {
	a := NewUIDAllocator(10000)
	a.InitPast(10050)
	uid := a.Allocate()
	if uid <= 10050 {
		t.Errorf("expected UID > 10050, got %d", uid)
	}
}

func TestChrootPath(t *testing.T) {
	path := ChrootPath("/srv/firecracker", "nvrs-fc-a1b2c3d4")
	expected := "/srv/firecracker/firecracker/nvrs-fc-a1b2c3d4"
	if path != expected {
		t.Errorf("got %q, want %q", path, expected)
	}
}

func TestVMInfoPath(t *testing.T) {
	path := VMInfoPath("/srv/firecracker", "nvrs-fc-a1b2c3d4")
	expected := "/srv/firecracker/firecracker/nvrs-fc-a1b2c3d4/vminfo.json"
	if path != expected {
		t.Errorf("got %q, want %q", path, expected)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/firecracker/jailer/ -v -count=1`
Expected: FAIL

- [ ] **Step 3: Implement jailer.go**

Create `internal/provider/firecracker/jailer/jailer.go`:

```go
package jailer

import (
	"fmt"
	"path/filepath"
	"sync"
)

// UIDAllocator provides unique UID/GID values for jailer isolation.
type UIDAllocator struct {
	mu   sync.Mutex
	next int
}

// NewUIDAllocator returns a UID allocator starting at baseUID.
func NewUIDAllocator(baseUID int) *UIDAllocator {
	return &UIDAllocator{next: baseUID}
}

// InitPast advances the allocator past the given UID. Used on startup
// to skip UIDs already in use by orphaned VMs.
func (a *UIDAllocator) InitPast(uid int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if uid >= a.next {
		a.next = uid + 1
	}
}

// Allocate returns the next unique UID.
func (a *UIDAllocator) Allocate() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	uid := a.next
	a.next++
	return uid
}

// ChrootPath returns the jailer chroot directory for a VM.
// Layout: <base>/firecracker/<vmID>/
// The actual chroot root is <base>/firecracker/<vmID>/root/ (managed by jailer).
// vminfo.json lives at <base>/firecracker/<vmID>/vminfo.json (outside chroot).
func ChrootPath(base, vmID string) string {
	return filepath.Join(base, "firecracker", vmID)
}

// VMInfoPath returns the path to vminfo.json for a VM.
func VMInfoPath(base, vmID string) string {
	return filepath.Join(ChrootPath(base, vmID), "vminfo.json")
}

// VMDirGlob returns a glob pattern matching all VM directories under base.
func VMDirGlob(base string) string {
	return filepath.Join(base, "firecracker", "nvrs-fc-*")
}

// Config holds the parameters needed to launch a jailed Firecracker VM.
type Config struct {
	FirecrackerBin string
	JailerBin      string
	VMID           string
	UID            int
	GID            int
	ChrootBase     string
	KernelPath     string
}

// ChrootDir returns the full chroot base directory for this VM.
func (c *Config) ChrootDir() string {
	return ChrootPath(c.ChrootBase, c.VMID)
}

// String returns a human-readable summary for logging.
func (c *Config) String() string {
	return fmt.Sprintf("jailer{vm=%s uid=%d chroot=%s}", c.VMID, c.UID, c.ChrootDir())
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/provider/firecracker/jailer/ -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/jailer/
git commit -m "feat(firecracker): add jailer config and UID allocator

UID allocator for per-VM isolation, chroot path helpers, and jailer
config struct. Supports crash recovery via InitPast()."
```

---

### Task 6: VM info persistence

**Files:**
- Create: `internal/provider/firecracker/vminfo.go`
- Create: `internal/provider/firecracker/vminfo_test.go`

No build tag — `vminfo.go` uses only the standard library.

- [ ] **Step 1: Write vminfo tests**

Create `internal/provider/firecracker/vminfo_test.go`:

```go
package firecracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVMInfoWriteRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vminfo.json")

	info := &VMInfo{
		ID:        "nvrs-fc-abc12345",
		PID:       12345,
		CID:       100,
		TapDevice: "fc-abc12345",
		SubnetIdx: 3,
		UID:       10003,
	}

	if err := info.Write(path); err != nil {
		t.Fatal(err)
	}

	got, err := ReadVMInfo(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != info.ID || got.PID != info.PID || got.CID != info.CID {
		t.Errorf("mismatch: got %+v", got)
	}
	if got.TapDevice != info.TapDevice || got.SubnetIdx != info.SubnetIdx {
		t.Errorf("mismatch: got %+v", got)
	}
}

func TestVMInfoClearRuntime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vminfo.json")

	info := &VMInfo{
		ID:        "nvrs-fc-abc12345",
		PID:       12345,
		CID:       100,
		TapDevice: "fc-abc12345",
		SubnetIdx: 3,
		UID:       10003,
	}
	info.Write(path)

	info.ClearRuntime()
	info.Write(path)

	got, _ := ReadVMInfo(path)
	if got.PID != 0 || got.TapDevice != "" || got.SubnetIdx != 0 {
		t.Errorf("expected runtime fields cleared, got %+v", got)
	}
	if got.CID != 100 || got.UID != 10003 {
		t.Errorf("expected persistent fields preserved, got %+v", got)
	}
}

func TestScanVMDirs(t *testing.T) {
	base := t.TempDir()
	fcDir := filepath.Join(base, "firecracker")
	os.MkdirAll(fcDir, 0o755)

	// Create two VM dirs with vminfo.json
	for _, vm := range []struct{ id string; cid uint32; uid int }{
		{"nvrs-fc-aaaaaaaa", 100, 10000},
		{"nvrs-fc-bbbbbbbb", 105, 10005},
	} {
		dir := filepath.Join(fcDir, vm.id)
		os.MkdirAll(dir, 0o755)
		info := &VMInfo{ID: vm.id, CID: vm.cid, UID: vm.uid}
		info.Write(filepath.Join(dir, "vminfo.json"))
	}

	infos, err := ScanVMDirs(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 VMs, got %d", len(infos))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/provider/firecracker/ -v -count=1 -run TestVMInfo`
Expected: FAIL

- [ ] **Step 3: Implement vminfo.go**

Create `internal/provider/firecracker/vminfo.go`:

```go
package firecracker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// VMInfo is the on-disk metadata for a Firecracker VM.
// Persisted as vminfo.json outside the jailer chroot so the guest cannot read it.
type VMInfo struct {
	ID          string `json:"id"`
	PID         int    `json:"pid,omitempty"`         // 0 when stopped
	CID         uint32 `json:"cid"`
	TapDevice   string `json:"tap_device,omitempty"`   // empty when stopped
	SubnetIdx   int    `json:"subnet_idx,omitempty"`   // 0 when stopped
	UID         int    `json:"uid"`
	NetworkMode string `json:"network_mode,omitempty"` // "published" or "isolated"
	Stopping    bool   `json:"stopping,omitempty"`
}

// Write persists VMInfo as JSON to the given path.
func (v *VMInfo) Write(path string) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vminfo: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write vminfo %s: %w", path, err)
	}
	return nil
}

// ClearRuntime zeroes out fields that are only valid while the VM is running.
// CID and UID are preserved for restart.
func (v *VMInfo) ClearRuntime() {
	v.PID = 0
	v.TapDevice = ""
	v.SubnetIdx = 0
	v.Stopping = false
}

// ReadVMInfo loads VMInfo from a JSON file.
func ReadVMInfo(path string) (*VMInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vminfo %s: %w", path, err)
	}
	var info VMInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("unmarshal vminfo %s: %w", path, err)
	}
	return &info, nil
}

// ScanVMDirs finds all VM directories under <base>/firecracker/nvrs-fc-*/
// and returns their VMInfo. Directories without vminfo.json are skipped.
func ScanVMDirs(base string) ([]*VMInfo, error) {
	pattern := filepath.Join(base, "firecracker", "nvrs-fc-*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("scan VM dirs: %w", err)
	}
	var infos []*VMInfo
	for _, dir := range matches {
		path := filepath.Join(dir, "vminfo.json")
		info, err := ReadVMInfo(path)
		if err != nil {
			continue // skip dirs without valid vminfo
		}
		infos = append(infos, info)
	}
	return infos, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/provider/firecracker/ -v -count=1 -run TestVMInfo`
Expected: PASS

Also: `go test ./internal/provider/firecracker/ -v -count=1 -run TestScan`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/firecracker/vminfo.go internal/provider/firecracker/vminfo_test.go
git commit -m "feat(firecracker): add VMInfo persistence and directory scanning

JSON-based VM metadata persisted outside jailer chroot. Supports
ClearRuntime() for stop/start cycles and ScanVMDirs() for crash
recovery on startup."
```

---

### Task 7: Guest agent — server and exec

**Files:**
- Create: `cmd/navaris-agent/main.go`
- Create: `cmd/navaris-agent/agent/server.go`
- Create: `cmd/navaris-agent/agent/exec.go`
- Create: `cmd/navaris-agent/agent/exec_test.go`

- [ ] **Step 1: Write exec handler tests**

Create `cmd/navaris-agent/agent/exec_test.go`:

```go
package agent

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

func TestHandleExec(t *testing.T) {
	payload, _ := json.Marshal(vsock.ExecPayload{
		Command: []string{"echo", "hello"},
	})
	req := &vsock.Message{
		Version: vsock.ProtocolVersion,
		Type:    vsock.TypeExec,
		ID:      "test-1",
		Payload: payload,
	}

	var responses []*vsock.Message
	send := func(msg *vsock.Message) error {
		responses = append(responses, msg)
		return nil
	}

	HandleExec(req, send)

	// Should have at least one stdout and one exit message.
	var gotStdout, gotExit bool
	for _, r := range responses {
		if r.ID != "test-1" {
			t.Errorf("wrong ID: %s", r.ID)
		}
		switch r.Type {
		case vsock.TypeStdout:
			var data vsock.DataPayload
			json.Unmarshal(r.Payload, &data)
			if !bytes.Contains(data.Data, []byte("hello")) {
				t.Errorf("stdout missing 'hello': %s", data.Data)
			}
			gotStdout = true
		case vsock.TypeExit:
			var exit vsock.ExitPayload
			json.Unmarshal(r.Payload, &exit)
			if exit.Code != 0 {
				t.Errorf("exit code: got %d, want 0", exit.Code)
			}
			gotExit = true
		}
	}
	if !gotStdout {
		t.Error("no stdout message")
	}
	if !gotExit {
		t.Error("no exit message")
	}
}

func TestHandleExecFailure(t *testing.T) {
	payload, _ := json.Marshal(vsock.ExecPayload{
		Command: []string{"/nonexistent-binary-xyz"},
	})
	req := &vsock.Message{
		Version: vsock.ProtocolVersion,
		Type:    vsock.TypeExec,
		ID:      "test-2",
		Payload: payload,
	}

	var responses []*vsock.Message
	send := func(msg *vsock.Message) error {
		responses = append(responses, msg)
		return nil
	}

	HandleExec(req, send)

	// Should get an exit with non-zero code or an error stderr.
	var gotExit bool
	for _, r := range responses {
		if r.Type == vsock.TypeExit {
			var exit vsock.ExitPayload
			json.Unmarshal(r.Payload, &exit)
			if exit.Code == 0 {
				t.Error("expected non-zero exit for missing binary")
			}
			gotExit = true
		}
	}
	if !gotExit {
		t.Error("no exit message")
	}
}
```

- [ ] **Step 2: Implement exec.go**

Create `cmd/navaris-agent/agent/exec.go`:

```go
package agent

import (
	"encoding/json"
	"io"
	"os/exec"

	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

// SendFunc sends a message back to the host.
type SendFunc func(msg *vsock.Message) error

// HandleExec runs a command and streams output back via send.
func HandleExec(req *vsock.Message, send SendFunc) {
	var payload vsock.ExecPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		sendExit(send, req.ID, -1)
		return
	}

	if len(payload.Command) == 0 {
		sendExit(send, req.ID, -1)
		return
	}

	cmd := exec.Command(payload.Command[0], payload.Command[1:]...)
	if payload.WorkDir != "" {
		cmd.Dir = payload.WorkDir
	}
	for k, v := range payload.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		sendExit(send, req.ID, -1)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		sendExit(send, req.ID, -1)
		return
	}

	if err := cmd.Start(); err != nil {
		sendExit(send, req.ID, 127)
		return
	}

	// Stream stdout and stderr.
	done := make(chan struct{}, 2)
	go streamOutput(send, req.ID, vsock.TypeStdout, stdout, done)
	go streamOutput(send, req.ID, vsock.TypeStderr, stderr, done)
	<-done
	<-done

	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	sendExit(send, req.ID, exitCode)
}

func streamOutput(send SendFunc, id string, msgType string, r io.Reader, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			data, _ := json.Marshal(vsock.DataPayload{Data: buf[:n]})
			send(&vsock.Message{
				Version: vsock.ProtocolVersion,
				Type:    msgType,
				ID:      id,
				Payload: data,
			})
		}
		if err != nil {
			return
		}
	}
}

func sendExit(send SendFunc, id string, code int) {
	data, _ := json.Marshal(vsock.ExitPayload{Code: code})
	send(&vsock.Message{
		Version: vsock.ProtocolVersion,
		Type:    vsock.TypeExit,
		ID:      id,
		Payload: data,
	})
}
```

- [ ] **Step 3: Implement server.go**

Create `cmd/navaris-agent/agent/server.go`:

```go
package agent

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

const VsockPort = 1024

// Server handles vsock connections from the host.
type Server struct {
	listener net.Listener
}

// NewServer creates a server listening on the given listener.
func NewServer(ln net.Listener) *Server {
	return &Server{listener: ln}
}

// Serve accepts connections and handles messages.
func (s *Server) Serve() error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	var mu sync.Mutex
	send := func(msg *vsock.Message) error {
		mu.Lock()
		defer mu.Unlock()
		return vsock.Encode(conn, msg)
	}

	for {
		msg, err := vsock.Decode(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("decode error: %v", err)
			}
			return
		}

		switch msg.Type {
		case vsock.TypePing:
			send(&vsock.Message{
				Version: vsock.ProtocolVersion,
				Type:    vsock.TypePong,
				ID:      msg.ID,
			})
		case vsock.TypeExec:
			go HandleExec(msg, send)
		case vsock.TypeSession:
			go HandleSession(msg, send, conn)
		default:
			log.Printf("unknown message type: %s", msg.Type)
		}
	}
}
```

- [ ] **Step 4: Create agent main.go stub**

Create `cmd/navaris-agent/main.go`:

```go
package main

import (
	"log"
	"net"
	"os"
	"strconv"

	"github.com/navaris/navaris/cmd/navaris-agent/agent"
	"golang.org/x/sys/unix"
)

func main() {
	port := agent.VsockPort
	if p := os.Getenv("VSOCK_PORT"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			port = v
		}
	}

	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		log.Fatalf("vsock socket: %v", err)
	}

	sa := &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_ANY,
		Port: uint32(port),
	}
	if err := unix.Bind(fd, sa); err != nil {
		log.Fatalf("vsock bind port %d: %v", port, err)
	}
	if err := unix.Listen(fd, 128); err != nil {
		log.Fatalf("vsock listen: %v", err)
	}

	f := os.NewFile(uintptr(fd), "vsock")
	ln, err := net.FileListener(f)
	f.Close()
	if err != nil {
		log.Fatalf("vsock listener: %v", err)
	}

	log.Printf("navaris-agent listening on vsock port %d", port)
	srv := agent.NewServer(ln)
	log.Fatal(srv.Serve())
}
```

- [ ] **Step 5: Create session.go stub** (full implementation in Task 8)

Create `cmd/navaris-agent/agent/session.go`:

```go
package agent

import (
	"encoding/json"
	"net"

	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

// HandleSession starts an interactive PTY session.
// Full implementation in Task 8.
func HandleSession(req *vsock.Message, send SendFunc, conn net.Conn) {
	var payload vsock.SessionPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		sendExit(send, req.ID, -1)
		return
	}
	// TODO: PTY allocation and shell spawn (Task 8)
	sendExit(send, req.ID, -1)
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./cmd/navaris-agent/agent/ -v -count=1`
Expected: PASS

- [ ] **Step 7: Verify agent compiles**

Run: `go build ./cmd/navaris-agent/`
Expected: Compiles (may need `GOOS=linux` if not on Linux)

- [ ] **Step 8: Commit**

```bash
git add cmd/navaris-agent/
git commit -m "feat(firecracker): add guest agent with vsock server and exec handler

Statically compiled agent for Firecracker VMs. Listens on vsock
port 1024, handles ping/pong health checks and command execution
with streamed stdout/stderr. Session support stubbed for Task 8."
```

---

### Task 8: Guest agent — PTY sessions

**Files:**
- Modify: `cmd/navaris-agent/agent/session.go`
- Create: `cmd/navaris-agent/agent/session_test.go`

- [ ] **Step 1: Write session tests**

Create `cmd/navaris-agent/agent/session_test.go`:

```go
package agent

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

func TestHandleSessionEcho(t *testing.T) {
	payload, _ := json.Marshal(vsock.SessionPayload{Shell: "/bin/sh"})
	req := &vsock.Message{
		Version: vsock.ProtocolVersion,
		Type:    vsock.TypeSession,
		ID:      "sess-1",
		Payload: payload,
	}

	var mu sync.Mutex
	var responses []*vsock.Message
	send := func(msg *vsock.Message) error {
		mu.Lock()
		defer mu.Unlock()
		responses = append(responses, msg)
		return nil
	}

	// Start session in background — needs a real net.Conn for stdin.
	// For unit testing, we test the PTY allocation and exec logic directly.
	pty, err := allocPTY("/bin/sh")
	if err != nil {
		t.Skipf("cannot allocate PTY (need terminal): %v", err)
	}
	defer pty.Close()

	// Write a command and read output.
	pty.Write([]byte("echo hello-from-pty\n"))
	time.Sleep(100 * time.Millisecond)

	buf := make([]byte, 4096)
	n, _ := pty.Read(buf)
	output := string(buf[:n])
	if len(output) == 0 {
		t.Error("expected output from PTY")
	}
	_ = req
	_ = send
}
```

- [ ] **Step 2: Implement session.go with PTY support**

Replace `cmd/navaris-agent/agent/session.go`:

```go
package agent

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

// ptyFile wraps a PTY master fd with the child process.
type ptyFile struct {
	master *os.File
	cmd    *exec.Cmd
}

func (p *ptyFile) Read(b []byte) (int, error)  { return p.master.Read(b) }
func (p *ptyFile) Write(b []byte) (int, error) { return p.master.Write(b) }
func (p *ptyFile) Close() error {
	p.master.Close()
	return p.cmd.Wait()
}

func (p *ptyFile) Resize(w, h int) error {
	ws := struct {
		Row uint16
		Col uint16
		X   uint16
		Y   uint16
	}{Row: uint16(h), Col: uint16(w)}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		p.master.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

// allocPTY opens a PTY and starts a shell.
func allocPTY(shell string) (*ptyFile, error) {
	master, slave, err := openPTY()
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(shell)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}

	if err := cmd.Start(); err != nil {
		master.Close()
		slave.Close()
		return nil, err
	}
	slave.Close() // Only master is needed after start.
	return &ptyFile{master: master, cmd: cmd}, nil
}

func openPTY() (master, slave *os.File, err error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}

	// Grant and unlock.
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, 0); e != 0 {
		// Fallback: get pts number.
	}
	var n int
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		m.Close()
		return nil, nil, e
	}
	var unlock int
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		m.Close()
		return nil, nil, e
	}

	sname := "/dev/pts/" + itoa(n)
	s, err := os.OpenFile(sname, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf) - 1
	for n > 0 {
		buf[i] = byte('0' + n%10)
		i--
		n /= 10
	}
	return string(buf[i+1:])
}

// HandleSession starts an interactive PTY session.
func HandleSession(req *vsock.Message, send SendFunc, conn net.Conn) {
	var payload vsock.SessionPayload
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		sendExit(send, req.ID, -1)
		return
	}

	shell := payload.Shell
	if shell == "" {
		shell = "/bin/sh"
	}

	pty, err := allocPTY(shell)
	if err != nil {
		log.Printf("session %s: alloc PTY: %v", req.ID, err)
		sendExit(send, req.ID, -1)
		return
	}
	defer pty.Close()

	// Read PTY output → send stdout messages to host.
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				data, _ := json.Marshal(vsock.DataPayload{Data: buf[:n]})
				send(&vsock.Message{
					Version: vsock.ProtocolVersion,
					Type:    vsock.TypeStdout,
					ID:      req.ID,
					Payload: data,
				})
			}
			if err != nil {
				return
			}
		}
	}()

	// Read stdin/resize/signal messages from host connection.
	// Note: this reads from the shared connection, so it must filter by ID.
	// In practice, the host sends stdin messages for this session's ID.
	for {
		msg, err := vsock.Decode(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("session %s: decode: %v", req.ID, err)
			}
			break
		}
		if msg.ID != req.ID {
			continue // Not for this session.
		}
		switch msg.Type {
		case vsock.TypeStdin:
			var data vsock.DataPayload
			json.Unmarshal(msg.Payload, &data)
			pty.Write(data.Data)
		case vsock.TypeResize:
			var resize vsock.ResizePayload
			json.Unmarshal(msg.Payload, &resize)
			pty.Resize(resize.Width, resize.Height)
		case vsock.TypeSignal:
			// Close on HUP or explicit close.
			goto done
		}
	}
done:
	<-done
	sendExit(send, req.ID, 0)
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./cmd/navaris-agent/agent/ -v -count=1`
Expected: PASS (session test may skip if no PTY available in CI)

- [ ] **Step 4: Commit**

```bash
git add cmd/navaris-agent/agent/session.go cmd/navaris-agent/agent/session_test.go
git commit -m "feat(firecracker): add PTY-based interactive sessions to guest agent

Allocates PTY, spawns shell, bidirectional stdin/stdout streaming
with resize support over the vsock protocol."
```

---

### Task 9: vsock client (host side)

**Files:**
- Create: `internal/provider/firecracker/vsock/client.go`
- Create: `internal/provider/firecracker/vsock/client_test.go`

- [ ] **Step 1: Write client tests**

Create `internal/provider/firecracker/vsock/client_test.go`:

```go
package vsock

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

// mockAgent simulates the guest agent over a Unix socket pair.
func mockAgent(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()
	for {
		msg, err := Decode(conn)
		if err != nil {
			return
		}
		switch msg.Type {
		case TypePing:
			Encode(conn, &Message{Version: ProtocolVersion, Type: TypePong, ID: msg.ID})
		case TypeExec:
			data, _ := json.Marshal(DataPayload{Data: []byte("output\n")})
			Encode(conn, &Message{Version: ProtocolVersion, Type: TypeStdout, ID: msg.ID, Payload: data})
			exitData, _ := json.Marshal(ExitPayload{Code: 0})
			Encode(conn, &Message{Version: ProtocolVersion, Type: TypeExit, ID: msg.ID, Payload: exitData})
		}
	}
}

func TestClientPing(t *testing.T) {
	server, client := net.Pipe()
	go mockAgent(t, server)

	c := NewClientFromConn(client)
	defer c.Close()

	if err := c.Ping(time.Second); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestClientExec(t *testing.T) {
	server, client := net.Pipe()
	go mockAgent(t, server)

	c := NewClientFromConn(client)
	defer c.Close()

	handle, err := c.Exec(ExecPayload{Command: []string{"echo", "hello"}})
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 1024)
	n, _ := handle.Stdout.Read(buf)
	if n == 0 {
		t.Error("no stdout data")
	}

	code, err := handle.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Errorf("exit code: got %d, want 0", code)
	}
}
```

- [ ] **Step 2: Implement client.go**

Create `internal/provider/firecracker/vsock/client.go`:

```go
package vsock

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Client is the host-side vsock client that communicates with the guest agent.
type Client struct {
	conn     net.Conn
	mu       sync.Mutex // protects writes
	handlers map[string]chan *Message
	handlerMu sync.RWMutex
	done     chan struct{}
}

// NewClientFromConn wraps an existing connection (for testing with net.Pipe).
func NewClientFromConn(conn net.Conn) *Client {
	c := &Client{
		conn:     conn,
		handlers: make(map[string]chan *Message),
		done:     make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Close shuts down the client.
func (c *Client) Close() error {
	close(c.done)
	return c.conn.Close()
}

func (c *Client) send(msg *Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Encode(c.conn, msg)
}

func (c *Client) register(id string) chan *Message {
	ch := make(chan *Message, 64)
	c.handlerMu.Lock()
	c.handlers[id] = ch
	c.handlerMu.Unlock()
	return ch
}

func (c *Client) unregister(id string) {
	c.handlerMu.Lock()
	delete(c.handlers, id)
	c.handlerMu.Unlock()
}

func (c *Client) readLoop() {
	for {
		msg, err := Decode(c.conn)
		if err != nil {
			return
		}
		c.handlerMu.RLock()
		ch, ok := c.handlers[msg.ID]
		c.handlerMu.RUnlock()
		if ok {
			select {
			case ch <- msg:
			default: // drop if full
			}
		}
	}
}

// Ping sends a health check and waits for pong.
func (c *Client) Ping(timeout time.Duration) error {
	id := uuid.NewString()[:8]
	ch := c.register(id)
	defer c.unregister(id)

	if err := c.send(&Message{Version: ProtocolVersion, Type: TypePing, ID: id}); err != nil {
		return fmt.Errorf("ping send: %w", err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case msg := <-ch:
		if msg.Type != TypePong {
			return fmt.Errorf("expected pong, got %s", msg.Type)
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("ping timeout after %s", timeout)
	}
}

// ExecHandle provides access to exec results from the host side.
type ExecHandle struct {
	id     string
	Stdout io.ReadCloser
	Stderr io.ReadCloser
	waitCh chan int
	errCh  chan error
}

// ID returns the correlation ID for this exec request.
func (h *ExecHandle) ID() string { return h.id }

// Wait blocks until the command exits and returns the exit code.
func (h *ExecHandle) Wait() (int, error) {
	select {
	case code := <-h.waitCh:
		return code, nil
	case err := <-h.errCh:
		return -1, err
	}
}

// Exec runs a command on the guest and returns an ExecHandle.
func (c *Client) Exec(payload ExecPayload) (*ExecHandle, error) {
	id := uuid.NewString()[:8]
	ch := c.register(id)

	data, _ := json.Marshal(payload)
	if err := c.send(&Message{Version: ProtocolVersion, Type: TypeExec, ID: id, Payload: data}); err != nil {
		c.unregister(id)
		return nil, fmt.Errorf("exec send: %w", err)
	}

	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	handle := &ExecHandle{
		id:     id,
		Stdout: stdoutR,
		Stderr: stderrR,
		waitCh: make(chan int, 1),
		errCh:  make(chan error, 1),
	}

	go func() {
		defer c.unregister(id)
		defer stdoutW.Close()
		defer stderrW.Close()
		for msg := range ch {
			switch msg.Type {
			case TypeStdout:
				var d DataPayload
				json.Unmarshal(msg.Payload, &d)
				stdoutW.Write(d.Data)
			case TypeStderr:
				var d DataPayload
				json.Unmarshal(msg.Payload, &d)
				stderrW.Write(d.Data)
			case TypeExit:
				var exit ExitPayload
				json.Unmarshal(msg.Payload, &exit)
				handle.waitCh <- exit.Code
				return
			}
		}
	}()

	return handle, nil
}

// Send writes a raw message to the agent. Used by ExecDetached for stdin forwarding.
func (c *Client) Send(msg *Message) error {
	return c.send(msg)
}

// SessionHandle provides bidirectional access to a PTY session.
type SessionHandle struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Resize func(w, h int) error
	Close  func() error
}

// Session starts an interactive PTY session on the guest.
func (c *Client) Session(payload SessionPayload) (*SessionHandle, error) {
	id := uuid.NewString()[:8]
	ch := c.register(id)

	data, _ := json.Marshal(payload)
	if err := c.send(&Message{Version: ProtocolVersion, Type: TypeSession, ID: id, Payload: data}); err != nil {
		c.unregister(id)
		return nil, fmt.Errorf("session send: %w", err)
	}

	stdoutR, stdoutW := io.Pipe()
	stdinR, stdinW := io.Pipe()

	// Forward stdin to agent.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stdinR.Read(buf)
			if n > 0 {
				d, _ := json.Marshal(DataPayload{Data: buf[:n]})
				c.send(&Message{Version: ProtocolVersion, Type: TypeStdin, ID: id, Payload: d})
			}
			if err != nil {
				return
			}
		}
	}()

	// Read agent stdout.
	go func() {
		defer stdoutW.Close()
		defer c.unregister(id)
		for msg := range ch {
			switch msg.Type {
			case TypeStdout:
				var d DataPayload
				json.Unmarshal(msg.Payload, &d)
				stdoutW.Write(d.Data)
			case TypeExit:
				return
			}
		}
	}()

	return &SessionHandle{
		Stdin:  stdinW,
		Stdout: stdoutR,
		Resize: func(w, h int) error {
			d, _ := json.Marshal(ResizePayload{Width: w, Height: h})
			return c.send(&Message{Version: ProtocolVersion, Type: TypeResize, ID: id, Payload: d})
		},
		Close: func() error {
			stdinW.Close()
			d, _ := json.Marshal(SignalPayload{Signal: "HUP"})
			return c.send(&Message{Version: ProtocolVersion, Type: TypeSignal, ID: id, Payload: d})
		},
	}, nil
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/provider/firecracker/vsock/ -v -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/provider/firecracker/vsock/client.go internal/provider/firecracker/vsock/client_test.go
git commit -m "feat(firecracker): add host-side vsock client

Multiplexed client for communicating with the guest agent.
Supports ping health checks and exec with streamed output.
Uses correlation IDs for concurrent request routing."
```

---

### Task 10: Firecracker provider — lifecycle and stubs

**Files:**
- Create: `internal/provider/firecracker/firecracker.go`
- Create: `internal/provider/firecracker/sandbox.go`
- Create: `internal/provider/firecracker/exec.go`
- Create: `internal/provider/firecracker/stubs.go`
- Modify: `go.mod`

All files in this task have `//go:build firecracker` build tag.

- [ ] **Step 1: Add firecracker-go-sdk dependency**

Run: `go get github.com/firecracker-microvm/firecracker-go-sdk@latest`
Run: `go get golang.org/x/sys@latest`
Run: `go mod tidy`

- [ ] **Step 2: Create firecracker.go — provider struct, New(), Health()**

Create `internal/provider/firecracker/firecracker.go`:

```go
//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
)

const backendName = "firecracker"

// Config holds the provider configuration.
type Config struct {
	FirecrackerBin string
	JailerBin      string
	KernelPath     string
	ImageDir       string
	ChrootBase     string
	VsockCIDBase   uint32
	HostInterface  string
}

func (c *Config) defaults() {
	if c.ChrootBase == "" {
		c.ChrootBase = "/srv/firecracker"
	}
	if c.VsockCIDBase == 0 {
		c.VsockCIDBase = 100
	}
}

// Provider implements domain.Provider for Firecracker microVMs.
type Provider struct {
	config    Config
	subnets   *network.Allocator
	uids      *jailer.UIDAllocator
	cidNext   uint32
	cidMu     sync.Mutex
	vms       map[string]*VMInfo
	vmMu      sync.RWMutex
	hostIface string
}

// New creates a Firecracker provider and recovers any orphaned VMs.
func New(cfg Config) (*Provider, error) {
	cfg.defaults()

	// Validate required fields.
	for _, check := range []struct{ name, val string }{
		{"firecracker-bin", cfg.FirecrackerBin},
		{"jailer-bin", cfg.JailerBin},
		{"kernel-path", cfg.KernelPath},
		{"image-dir", cfg.ImageDir},
	} {
		if check.val == "" {
			return nil, fmt.Errorf("firecracker: %s is required", check.name)
		}
	}

	// Detect host interface.
	hostIface := cfg.HostInterface
	if hostIface == "" {
		detected, err := network.DetectDefaultInterface()
		if err != nil {
			return nil, fmt.Errorf("firecracker: %w", err)
		}
		hostIface = detected
	}

	// Check ip_forward.
	if err := network.CheckIPForward(); err != nil {
		return nil, fmt.Errorf("firecracker: %w", err)
	}

	p := &Provider{
		config:    cfg,
		subnets:   network.NewAllocator(),
		uids:      jailer.NewUIDAllocator(10000),
		cidNext:   cfg.VsockCIDBase,
		vms:       make(map[string]*VMInfo),
		hostIface: hostIface,
	}

	// Recover orphaned VMs from disk.
	if err := p.recover(); err != nil {
		slog.Warn("firecracker: recovery scan", "error", err)
	}

	return p, nil
}

func (p *Provider) recover() error {
	infos, err := ScanVMDirs(p.config.ChrootBase)
	if err != nil {
		return err
	}
	for _, info := range infos {
		p.vmMu.Lock()
		p.vms[info.ID] = info
		p.vmMu.Unlock()

		// Advance allocators past in-use values.
		p.cidMu.Lock()
		if info.CID >= p.cidNext {
			p.cidNext = info.CID + 1
		}
		p.cidMu.Unlock()
		p.uids.InitPast(info.UID)
		if info.SubnetIdx > 0 {
			p.subnets.InitPast(info.SubnetIdx)
		}
		slog.Info("firecracker: recovered VM", "id", info.ID, "pid", info.PID)
	}
	return nil
}

func (p *Provider) allocateCID() uint32 {
	p.cidMu.Lock()
	defer p.cidMu.Unlock()
	cid := p.cidNext
	p.cidNext++
	return cid
}

// Health checks if the Firecracker binary is accessible.
func (p *Provider) Health(ctx context.Context) domain.ProviderHealth {
	start := time.Now()
	_, err := os.Stat(p.config.FirecrackerBin)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return domain.ProviderHealth{
			Backend: backendName, Healthy: false,
			LatencyMS: latency, Error: fmt.Sprintf("firecracker binary not found: %v", err),
		}
	}
	return domain.ProviderHealth{
		Backend: backendName, Healthy: true, LatencyMS: latency,
	}
}

var _ domain.Provider = (*Provider)(nil)
```

- [ ] **Step 3: Create sandbox.go — lifecycle methods**

Create `internal/provider/firecracker/sandbox.go`:

```go
//go:build firecracker

package firecracker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	fcsdk "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
	"github.com/google/uuid"
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
	"github.com/navaris/navaris/internal/provider/firecracker/network"
	"github.com/navaris/navaris/internal/provider/firecracker/vsock"
)

func vmName() string {
	return "nvrs-fc-" + uuid.NewString()[:8]
}

func (p *Provider) CreateSandbox(ctx context.Context, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
	vmID := vmName()
	vmDir := jailer.ChrootPath(p.config.ChrootBase, vmID)

	// Create VM directory.
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		return domain.BackendRef{}, fmt.Errorf("firecracker create dir %s: %w", vmID, err)
	}

	// Copy rootfs image.
	srcImage := filepath.Join(p.config.ImageDir, req.ImageRef+".ext4")
	dstImage := filepath.Join(vmDir, "rootfs.ext4")
	if err := copyFile(srcImage, dstImage); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker copy rootfs %s: %w", vmID, err)
	}

	// Allocate resources.
	cid := p.allocateCID()
	uid := p.uids.Allocate()

	// Write vminfo.json.
	info := &VMInfo{ID: vmID, CID: cid, UID: uid, NetworkMode: string(req.NetworkMode)}
	if err := info.Write(jailer.VMInfoPath(p.config.ChrootBase, vmID)); err != nil {
		os.RemoveAll(vmDir)
		return domain.BackendRef{}, fmt.Errorf("firecracker write vminfo %s: %w", vmID, err)
	}

	return domain.BackendRef{Backend: backendName, Ref: vmID}, nil
}

func (p *Provider) StartSandbox(ctx context.Context, ref domain.BackendRef) error {
	vmID := ref.Ref

	// Read vminfo.
	infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
	info, err := ReadVMInfo(infoPath)
	if err != nil {
		return fmt.Errorf("firecracker start %s: %w", vmID, err)
	}

	// Check if already running.
	if info.PID > 0 && processAlive(info.PID) {
		return nil
	}

	// Allocate networking.
	subnetIdx := p.subnets.Allocate()
	tapName := network.TapName(vmID)
	hostIP := p.subnets.HostIP(subnetIdx).String()

	if err := network.CreateTap(tapName, hostIP, "255.255.255.252"); err != nil {
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker create tap %s: %w", vmID, err)
	}

	// Build Firecracker config.
	vmDir := jailer.ChrootPath(p.config.ChrootBase, vmID)
	rootfsPath := filepath.Join(vmDir, "rootfs.ext4")
	bootArgs := "console=ttyS0 reboot=k panic=1 pci=off " + p.subnets.KernelBootArg(subnetIdx)

	fcCfg := fcsdk.Config{
		SocketPath:      filepath.Join(vmDir, "firecracker.sock"),
		KernelImagePath: p.config.KernelPath,
		KernelArgs:      bootArgs,
		Drives: []models.Drive{
			{
				DriveID:      fcsdk.String("rootfs"),
				PathOnHost:   fcsdk.String(rootfsPath),
				IsRootDevice: fcsdk.Bool(true),
				IsReadOnly:   fcsdk.Bool(false),
			},
		},
		NetworkInterfaces: []fcsdk.NetworkInterface{
			{
				StaticConfiguration: &fcsdk.StaticNetworkConfiguration{
					MacAddress:  fmt.Sprintf("02:FC:00:00:%02x:%02x", subnetIdx>>8, subnetIdx&0xFF),
					HostDevName: tapName,
				},
			},
		},
		VsockDevices: []fcsdk.VsockDevice{
			{Path: "vsock", CID: uint32(info.CID)},
		},
		JailerCfg: &fcsdk.JailerConfig{
			GID:           fcsdk.Int(info.UID),
			UID:           fcsdk.Int(info.UID),
			ID:            vmID,
			NumaNode:      fcsdk.Int(0),
			ExecFile:      p.config.FirecrackerBin,
			JailerBinary:  p.config.JailerBin,
			ChrootBaseDir: p.config.ChrootBase,
			ChrootStrategy: fcsdk.NewNaiveChrootStrategy(p.config.KernelPath),
		},
	}

	// Apply resource limits.
	if req := info; req != nil {
		// CPU/memory limits are set via cgroup by the jailer.
		// Additional config can be added here.
	}

	// Launch VM.
	machine, err := fcsdk.NewMachine(ctx, fcCfg)
	if err != nil {
		network.DeleteTap(tapName)
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker new machine %s: %w", vmID, err)
	}

	if err := machine.Start(ctx); err != nil {
		network.DeleteTap(tapName)
		p.subnets.Release(subnetIdx)
		return fmt.Errorf("firecracker start machine %s: %w", vmID, err)
	}

	// Update vminfo with runtime state.
	info.PID = machine.PID()
	info.TapDevice = tapName
	info.SubnetIdx = subnetIdx
	info.Write(infoPath)

	// Register in memory.
	p.vmMu.Lock()
	p.vms[vmID] = info
	p.vmMu.Unlock()

	// Add masquerade for published mode.
	if info.NetworkMode == string(domain.NetworkPublished) {
		guestIP := p.subnets.GuestIP(subnetIdx).String()
		if err := network.AddMasquerade(guestIP, p.hostIface); err != nil {
			// Non-fatal — log but continue.
			slog.Warn("firecracker: masquerade failed", "vm", vmID, "error", err)
		}
	}

	// Wait for agent health check.
	if err := p.waitForAgent(ctx, info.CID, 30*time.Second); err != nil {
		// Agent didn't respond — leave VM running, caller can retry or destroy.
		return fmt.Errorf("firecracker agent timeout %s: %w", vmID, err)
	}

	return nil
}

func (p *Provider) StopSandbox(ctx context.Context, ref domain.BackendRef, force bool) error {
	vmID := ref.Ref
	infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
	info, err := ReadVMInfo(infoPath)
	if err != nil {
		return fmt.Errorf("firecracker stop %s: %w", vmID, err)
	}

	if info.PID > 0 && processAlive(info.PID) {
		info.Stopping = true
		info.Write(infoPath)

		if force {
			syscall.Kill(info.PID, syscall.SIGKILL)
		} else {
			// Graceful: send CtrlAltDel via Firecracker API socket.
			vmDir := jailer.ChrootPath(p.config.ChrootBase, vmID)
			sockPath := filepath.Join(vmDir, "root", "run", "firecracker.socket")
			machine, merr := fcsdk.NewMachine(ctx, fcsdk.Config{SocketPath: sockPath})
			if merr == nil {
				machine.SendCtrlAltDel(ctx)
			}
			deadline := time.After(30 * time.Second)
			for processAlive(info.PID) {
				select {
				case <-deadline:
					syscall.Kill(info.PID, syscall.SIGKILL)
					goto stopped
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
	}
stopped:

	// Clean up networking.
	if info.TapDevice != "" {
		network.DeleteTap(info.TapDevice)
		// Only remove masquerade if one was added (published mode only).
		if info.SubnetIdx > 0 && info.NetworkMode == string(domain.NetworkPublished) {
			guestIP := p.subnets.GuestIP(info.SubnetIdx).String()
			network.RemoveMasquerade(guestIP, p.hostIface)
		}
		p.subnets.Release(info.SubnetIdx)
	}

	// Update vminfo.
	info.ClearRuntime()
	info.Write(infoPath)

	p.vmMu.Lock()
	delete(p.vms, vmID)
	p.vmMu.Unlock()

	return nil
}

func (p *Provider) DestroySandbox(ctx context.Context, ref domain.BackendRef) error {
	// Stop first if running.
	p.StopSandbox(ctx, ref, true)

	vmID := ref.Ref
	vmDir := jailer.ChrootPath(p.config.ChrootBase, vmID)

	if err := os.RemoveAll(vmDir); err != nil {
		return fmt.Errorf("firecracker destroy %s: %w", vmID, err)
	}

	p.vmMu.Lock()
	delete(p.vms, vmID)
	p.vmMu.Unlock()

	return nil
}

func (p *Provider) GetSandboxState(ctx context.Context, ref domain.BackendRef) (domain.SandboxState, error) {
	vmID := ref.Ref
	vmDir := jailer.ChrootPath(p.config.ChrootBase, vmID)

	// Check if VM directory exists.
	if _, err := os.Stat(vmDir); os.IsNotExist(err) {
		return domain.SandboxDestroyed, nil
	}

	infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
	info, err := ReadVMInfo(infoPath)
	if err != nil {
		return domain.SandboxDestroyed, nil
	}

	// Check if stopping.
	if info.Stopping {
		return domain.SandboxStopping, nil
	}

	// Check process liveness.
	if info.PID == 0 || !processAlive(info.PID) {
		if info.PID > 0 {
			// Had a PID but it's dead — unexpected crash.
			return domain.SandboxFailed, nil
		}
		return domain.SandboxStopped, nil
	}

	// Process alive — check agent health.
	if err := p.pingAgent(ctx, info.CID); err != nil {
		return domain.SandboxStarting, nil
	}

	return domain.SandboxRunning, nil
}

func (p *Provider) CreateSandboxFromSnapshot(ctx context.Context, snapshotRef domain.BackendRef, req domain.CreateSandboxRequest) (domain.BackendRef, error) {
	return domain.BackendRef{}, ErrNotImplemented
}

// Helper functions.

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func (p *Provider) waitForAgent(ctx context.Context, cid uint32, timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		// Try to dial and ping — reconnect on each attempt since the
		// agent may not be listening yet during VM boot.
		client, err := p.dialAgent(cid)
		if err == nil {
			pingErr := client.Ping(2 * time.Second)
			client.Close()
			if pingErr == nil {
				return nil
			}
		}
		select {
		case <-deadline:
			return fmt.Errorf("agent at CID %d did not respond within %s", cid, timeout)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (p *Provider) pingAgent(ctx context.Context, cid uint32) error {
	client, err := p.dialAgent(cid)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.Ping(2 * time.Second)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
```

- [ ] **Step 4: Create exec.go — execution methods**

Create `internal/provider/firecracker/exec.go`:

```go
//go:build firecracker

package firecracker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker/jailer"
	fcvsock "github.com/navaris/navaris/internal/provider/firecracker/vsock"
	"golang.org/x/sys/unix"
)

func (p *Provider) dialAgent(cid uint32) (*fcvsock.Client, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}

	sa := &unix.SockaddrVM{CID: cid, Port: 1024}
	if err := unix.Connect(fd, sa); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("vsock connect CID %d: %w", cid, err)
	}

	f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock:%d", cid))
	conn, err := net.FileConn(f)
	f.Close() // FileConn dups the fd
	if err != nil {
		return nil, fmt.Errorf("vsock fileconn CID %d: %w", cid, err)
	}

	return fcvsock.NewClientFromConn(conn), nil
}

func (p *Provider) getVMInfo(vmID string) (*VMInfo, error) {
	infoPath := jailer.VMInfoPath(p.config.ChrootBase, vmID)
	return ReadVMInfo(infoPath)
}

func (p *Provider) Exec(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.ExecHandle, error) {
	info, err := p.getVMInfo(ref.Ref)
	if err != nil {
		return domain.ExecHandle{}, fmt.Errorf("firecracker exec %s: %w", ref.Ref, err)
	}

	client, err := p.dialAgent(info.CID)
	if err != nil {
		return domain.ExecHandle{}, fmt.Errorf("firecracker exec %s: %w", ref.Ref, err)
	}

	handle, err := client.Exec(fcvsock.ExecPayload{
		Command: req.Command,
		Env:     req.Env,
		WorkDir: req.WorkDir,
	})
	if err != nil {
		client.Close()
		return domain.ExecHandle{}, fmt.Errorf("firecracker exec %s: %w", ref.Ref, err)
	}

	return domain.ExecHandle{
		Stdout: handle.Stdout,
		Stderr: handle.Stderr,
		Wait: func() (int, error) {
			code, err := handle.Wait()
			client.Close()
			return code, err
		},
		Cancel: func() error {
			return client.Close()
		},
	}, nil
}

func (p *Provider) ExecDetached(ctx context.Context, ref domain.BackendRef, req domain.ExecRequest) (domain.DetachedExecHandle, error) {
	info, err := p.getVMInfo(ref.Ref)
	if err != nil {
		return domain.DetachedExecHandle{}, fmt.Errorf("firecracker exec-detached %s: %w", ref.Ref, err)
	}

	client, err := p.dialAgent(info.CID)
	if err != nil {
		return domain.DetachedExecHandle{}, fmt.Errorf("firecracker exec-detached %s: %w", ref.Ref, err)
	}

	// Start exec — stdin is streamed via vsock TypeStdin messages.
	execHandle, err := client.Exec(fcvsock.ExecPayload{
		Command: req.Command,
		Env:     req.Env,
		WorkDir: req.WorkDir,
	})
	if err != nil {
		client.Close()
		return domain.DetachedExecHandle{}, fmt.Errorf("firecracker exec-detached %s: %w", ref.Ref, err)
	}
	execID := execHandle.ID() // correlation ID for stdin routing

	// Wrap stdin writes to forward to vsock.
	stdinR, stdinW := io.Pipe()
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stdinR.Read(buf)
			if n > 0 {
				data, _ := json.Marshal(fcvsock.DataPayload{Data: buf[:n]})
				client.Send(&fcvsock.Message{
					Version: fcvsock.ProtocolVersion,
					Type:    fcvsock.TypeStdin,
					ID:      execID,
					Payload: data,
				})
			}
			if err != nil {
				return
			}
		}
	}()

	return domain.DetachedExecHandle{
		Stdin:  stdinW,
		Stdout: execHandle.Stdout,
		Resize: func(w, h int) error { return nil }, // Not PTY-based
		Close: func() error {
			stdinW.Close()
			return client.Close()
		},
	}, nil
}

func (p *Provider) AttachSession(ctx context.Context, ref domain.BackendRef, req domain.SessionRequest) (domain.SessionHandle, error) {
	info, err := p.getVMInfo(ref.Ref)
	if err != nil {
		return domain.SessionHandle{}, fmt.Errorf("firecracker session %s: %w", ref.Ref, err)
	}

	client, err := p.dialAgent(info.CID)
	if err != nil {
		return domain.SessionHandle{}, fmt.Errorf("firecracker session %s: %w", ref.Ref, err)
	}

	session, err := client.Session(fcvsock.SessionPayload{Shell: req.Shell})
	if err != nil {
		client.Close()
		return domain.SessionHandle{}, fmt.Errorf("firecracker session %s: %w", ref.Ref, err)
	}

	// Adapt session Stdin/Stdout into a single io.ReadWriteCloser
	// to satisfy domain.SessionHandle.Conn.
	conn := &sessionConn{r: session.Stdout, w: session.Stdin}

	return domain.SessionHandle{
		Conn:   conn,
		Resize: session.Resize,
		Close: func() error {
			session.Close()
			return client.Close()
		},
	}, nil
}

// sessionConn adapts separate read/write streams into io.ReadWriteCloser.
type sessionConn struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (s *sessionConn) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *sessionConn) Write(p []byte) (int, error)  { return s.w.Write(p) }
func (s *sessionConn) Close() error {
	s.w.Close()
	return s.r.Close()
}
```

- [ ] **Step 5: Create stubs.go — Phase 2 methods**

Create `internal/provider/firecracker/stubs.go`:

```go
//go:build firecracker

package firecracker

import (
	"context"
	"errors"

	"github.com/navaris/navaris/internal/domain"
)

// ErrNotImplemented is returned by Phase 2 stub methods.
var ErrNotImplemented = errors.New("firecracker provider: operation not implemented (phase 2)")

func (p *Provider) CreateSnapshot(ctx context.Context, ref domain.BackendRef, label string, mode domain.ConsistencyMode) (domain.BackendRef, error) {
	return domain.BackendRef{}, ErrNotImplemented
}

func (p *Provider) RestoreSnapshot(ctx context.Context, sandboxRef domain.BackendRef, snapshotRef domain.BackendRef) error {
	return ErrNotImplemented
}

func (p *Provider) DeleteSnapshot(ctx context.Context, snapshotRef domain.BackendRef) error {
	return ErrNotImplemented
}

func (p *Provider) PublishSnapshotAsImage(ctx context.Context, snapshotRef domain.BackendRef, req domain.PublishImageRequest) (domain.BackendRef, error) {
	return domain.BackendRef{}, ErrNotImplemented
}

func (p *Provider) DeleteImage(ctx context.Context, imageRef domain.BackendRef) error {
	return ErrNotImplemented
}

func (p *Provider) GetImageInfo(ctx context.Context, imageRef domain.BackendRef) (domain.ImageInfo, error) {
	return domain.ImageInfo{}, ErrNotImplemented
}

func (p *Provider) PublishPort(ctx context.Context, ref domain.BackendRef, targetPort int, opts domain.PublishPortOptions) (domain.PublishedEndpoint, error) {
	return domain.PublishedEndpoint{}, ErrNotImplemented
}

func (p *Provider) UnpublishPort(ctx context.Context, ref domain.BackendRef, publishedPort int) error {
	return ErrNotImplemented
}
```

- [ ] **Step 6: Verify compilation**

Run: `go build -tags firecracker ./internal/provider/firecracker/...`
Expected: Compiles

- [ ] **Step 7: Commit**

```bash
git add internal/provider/firecracker/firecracker.go internal/provider/firecracker/sandbox.go internal/provider/firecracker/exec.go internal/provider/firecracker/stubs.go go.mod go.sum
git commit -m "feat(firecracker): add provider with VM lifecycle and Phase 2 stubs

Implements CreateSandbox, StartSandbox, StopSandbox, DestroySandbox,
GetSandboxState with full domain state machine compliance. Exec
delegates to vsock client. Snapshot/image/port methods stubbed
for Phase 2. Uses jailer, tap networking, and vminfo persistence."
```

---

### Task 11: Build tag wiring

**Files:**
- Create: `cmd/navarisd/provider_firecracker.go`
- Create: `cmd/navarisd/provider_firecracker_stub.go`
- Modify: `cmd/navarisd/main.go`
- Modify: `cmd/navarisd/provider_mock.go`

- [ ] **Step 1: Create provider_firecracker.go**

Create `cmd/navarisd/provider_firecracker.go`:

```go
//go:build firecracker

package main

import (
	"github.com/navaris/navaris/internal/domain"
	"github.com/navaris/navaris/internal/provider/firecracker"
)

func newFirecrackerProvider(cfg config) (domain.Provider, error) {
	return firecracker.New(firecracker.Config{
		FirecrackerBin: cfg.firecrackerBin,
		JailerBin:      cfg.jailerBin,
		KernelPath:     cfg.kernelPath,
		ImageDir:       cfg.imageDir,
		ChrootBase:     cfg.chrootBase,
		HostInterface:  cfg.hostInterface,
	})
}
```

- [ ] **Step 2: Create provider_firecracker_stub.go**

Create `cmd/navarisd/provider_firecracker_stub.go`:

```go
//go:build !firecracker

package main

import (
	"fmt"

	"github.com/navaris/navaris/internal/domain"
)

func newFirecrackerProvider(_ config) (domain.Provider, error) {
	return nil, fmt.Errorf("firecracker provider not available: binary built without 'firecracker' build tag")
}
```

- [ ] **Step 3: Leave provider_mock.go unchanged**

`provider_mock.go` keeps its existing `//go:build !incus` tag. It provides the `newIncusProvider` stub when building without the `incus` tag. The new `provider_firecracker_stub.go` independently provides `newFirecrackerProvider` when building without the `firecracker` tag. All four tag combinations compile correctly:
- No tags: mock (`!incus`) + fc_stub (`!firecracker`)
- `-tags incus`: incus + fc_stub
- `-tags firecracker`: mock (`!incus`) + fc
- `-tags incus,firecracker`: incus + fc

- [ ] **Step 4: Update main.go — config, flags, provider selection**

In `cmd/navarisd/main.go`, add fields to config struct:

```go
type config struct {
	listen         string
	dbPath         string
	logLevel       string
	authToken      string
	incusSocket    string
	firecrackerBin string
	jailerBin      string
	kernelPath     string
	imageDir       string
	chrootBase     string
	hostInterface  string
	gcInterval     time.Duration
	concurrency    int
}
```

Add flags in `parseFlags()`:
```go
flag.StringVar(&cfg.firecrackerBin, "firecracker-bin", "", "path to Firecracker binary")
flag.StringVar(&cfg.jailerBin, "jailer-bin", "", "path to jailer binary")
flag.StringVar(&cfg.kernelPath, "kernel-path", "", "path to vmlinux kernel")
flag.StringVar(&cfg.imageDir, "image-dir", "", "directory containing rootfs images")
flag.StringVar(&cfg.chrootBase, "chroot-base", "/srv/firecracker", "jailer chroot base directory")
flag.StringVar(&cfg.hostInterface, "host-interface", "", "network interface for masquerade (auto-detect if empty)")
```

Replace the provider selection block in `run()`:
```go
// Provider
var prov domain.Provider
var backendName string
switch {
case cfg.firecrackerBin != "":
    p, err := newFirecrackerProvider(cfg)
    if err != nil {
        return fmt.Errorf("firecracker provider: %w", err)
    }
    prov = p
    backendName = "firecracker"
    logger.Info("using firecracker provider")
case cfg.incusSocket != "":
    p, err := newIncusProvider(cfg.incusSocket)
    if err != nil {
        return fmt.Errorf("incus provider: %w", err)
    }
    prov = p
    backendName = "incus"
    logger.Info("using incus provider", "socket", cfg.incusSocket)
default:
    prov = provider.NewMock()
    backendName = "mock"
    logger.Info("using mock provider")
}
```

Update the `NewSandboxService` call to pass `backendName`:
```go
sbxSvc := service.NewSandboxService(
    store.SandboxStore(), store.SnapshotStore(), store.OperationStore(), store.PortBindingStore(),
    store.SessionStore(), prov, bus, disp, backendName,
)
```

- [ ] **Step 5: Verify compilation for all build tag combinations**

Run: `go build ./cmd/navarisd/` (mock)
Run: `go build -tags incus ./cmd/navarisd/` (Incus)
Run: `go build -tags firecracker ./cmd/navarisd/` (Firecracker)
Expected: All three compile

- [ ] **Step 6: Run all tests**

Run: `go test ./... -count=1`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/navarisd/ internal/service/sandbox.go
git commit -m "feat(firecracker): wire provider into navarisd with build tags

Add --firecracker-bin flag and related config. Provider selection
uses switch/case: Firecracker > Incus > Mock. Build tags gate
provider availability. SandboxService receives backend name
via constructor instead of hardcoding 'incus'."
```

---

### Task 12: Final verification and go mod tidy

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Run go mod tidy**

Run: `go mod tidy`

- [ ] **Step 2: Run all unit tests**

Run: `go test ./... -count=1`
Expected: All PASS

- [ ] **Step 3: Verify build tags**

Run: `go vet ./...`
Run: `go vet -tags firecracker ./internal/provider/firecracker/...`
Run: `go vet -tags firecracker ./cmd/navarisd/`
Expected: No issues

- [ ] **Step 4: Commit if needed**

```bash
git add go.mod go.sum
git commit -m "chore: go mod tidy for firecracker dependencies"
```

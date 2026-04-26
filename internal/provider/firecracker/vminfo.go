package firecracker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type VMInfo struct {
	ID                  string      `json:"id"`
	PID                 int         `json:"pid,omitempty"`
	CID                 uint32      `json:"cid"`
	TapDevice           string      `json:"tap_device,omitempty"`
	SubnetIdx           int         `json:"subnet_idx"`
	UID                 int         `json:"uid"`
	NetworkMode         string      `json:"network_mode,omitempty"`
	Stopping            bool        `json:"stopping,omitempty"`
	Ports               map[int]int `json:"ports,omitempty"`
	RestoreFromSnapshot bool        `json:"restore_from_snapshot,omitempty"`
	RestoreSubnetIdx    int         `json:"restore_subnet_idx,omitempty"` // original subnet for live restore
	// ForkPointID is set when the VM was spawned from a fork-point. The
	// fork-point's vmstate.bin must remain on disk (the kernel may still
	// page-fault clean memory pages from it via MAP_PRIVATE) until this VM
	// is destroyed; T17 wires the release call.
	ForkPointID string `json:"fork_point_id,omitempty"`
	// VcpuCount and MemSizeMib are resolved at CreateSandbox time from the
	// request limits (or provider defaults) and used when the VM is started.
	VcpuCount  int64 `json:"vcpu_count,omitempty"`
	MemSizeMib int64 `json:"mem_size_mib,omitempty"`

	// LimitCPU is the user-facing CPU limit (what the guest sees enforced).
	// VcpuCount above is the booted ceiling (limit * VcpuHeadroomMult,
	// clamped). Recorded for use by runtime resize.
	LimitCPU int64 `json:"limit_cpu,omitempty"`

	// LimitMemMib is the user-facing memory limit. The balloon device is
	// inflated to (CeilingMemMib - LimitMemMib) at boot to enforce it.
	LimitMemMib int64 `json:"limit_mem_mib,omitempty"`

	// CeilingCPU is the boot-time vCPU count (== VcpuCount). Stored
	// separately from VcpuCount for forward compatibility with runtime
	// vCPU hotplug, which would otherwise mutate VcpuCount.
	CeilingCPU int64 `json:"ceiling_cpu,omitempty"`

	// CeilingMemMib is the boot-time mem_size_mib (== MemSizeMib).
	// Bounds the maximum memory_limit_mb a runtime resize can grant.
	CeilingMemMib int64 `json:"ceiling_mem_mib,omitempty"`
}

func (v *VMInfo) Write(path string) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vminfo: %w", err)
	}
	// Atomic write: temp file + fsync + rename to prevent corruption on crash.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".vminfo-*.tmp")
	if err != nil {
		return fmt.Errorf("write vminfo tmp %s: %w", path, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write vminfo %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("sync vminfo %s: %w", path, err)
	}
	tmp.Close()
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename vminfo %s: %w", path, err)
	}
	// Sync the directory to ensure the rename is durable.
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open dir for sync %s: %w", dir, err)
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return fmt.Errorf("sync dir %s: %w", dir, err)
	}
	d.Close()
	return nil
}

func (v *VMInfo) ClearRuntime() {
	v.PID = 0
	v.TapDevice = ""
	v.SubnetIdx = 0
	v.Stopping = false
	v.Ports = nil
}

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

func ScanVMDirs(base string) ([]*VMInfo, []error) {
	pattern := filepath.Join(base, "firecracker", "nvrs-fc-*")
	return ScanVMDirsGlob(pattern)
}

func ScanVMDirsGlob(pattern string) ([]*VMInfo, []error) {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, []error{fmt.Errorf("scan VM dirs: %w", err)}
	}
	var infos []*VMInfo
	var errs []error
	for _, dir := range matches {
		path := filepath.Join(dir, "vminfo.json")
		info, err := ReadVMInfo(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("scan %s: %w", dir, err))
			continue
		}
		infos = append(infos, info)
	}
	return infos, errs
}

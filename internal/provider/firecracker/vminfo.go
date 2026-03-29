package firecracker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type VMInfo struct {
	ID          string `json:"id"`
	PID         int    `json:"pid,omitempty"`
	CID         uint32 `json:"cid"`
	TapDevice   string `json:"tap_device,omitempty"`
	SubnetIdx   int    `json:"subnet_idx"`
	UID         int    `json:"uid"`
	NetworkMode string `json:"network_mode,omitempty"`
	Stopping    bool   `json:"stopping,omitempty"`
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
	return nil
}

func (v *VMInfo) ClearRuntime() {
	v.PID = 0
	v.TapDevice = ""
	v.SubnetIdx = 0
	v.Stopping = false
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

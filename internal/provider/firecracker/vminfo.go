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
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write vminfo %s: %w", path, err)
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
			continue
		}
		infos = append(infos, info)
	}
	return infos, nil
}

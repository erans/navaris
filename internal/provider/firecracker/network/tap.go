package network

import (
	"fmt"
	"os/exec"
	"strings"
)

func TapName(vmID string) string {
	const prefix = "nvrs-"
	if strings.HasPrefix(vmID, prefix) {
		return vmID[len(prefix):]
	}
	suffix := vmID
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	return "fc-" + suffix
}

func CreateTap(name string, hostIP string) error {
	cmds := [][]string{
		{"ip", "tuntap", "add", "dev", name, "mode", "tap"},
		{"ip", "addr", "add", hostIP + "/30", "dev", name},
		{"ip", "link", "set", name, "up"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = childEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("tap %s: %s: %w: %s", name, strings.Join(args, " "), err, out)
		}
	}
	return nil
}

func DeleteTap(name string) error {
	cmd := exec.Command("ip", "link", "del", name)
	cmd.Env = childEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("delete tap %s: %w: %s", name, err, out)
	}
	return nil
}

func MasqueradeArgs(guestIP string, hostIface string) []string {
	return []string{
		"-t", "nat", "-A", "POSTROUTING",
		"-s", guestIP + "/32",
		"-o", hostIface,
		"-j", "MASQUERADE",
	}
}

func DeleteMasqueradeArgs(guestIP string, hostIface string) []string {
	return []string{
		"-t", "nat", "-D", "POSTROUTING",
		"-s", guestIP + "/32",
		"-o", hostIface,
		"-j", "MASQUERADE",
	}
}

func AddMasquerade(guestIP string, hostIface string) error {
	args := MasqueradeArgs(guestIP, hostIface)
	cmd := exec.Command("iptables", args...)
	cmd.Env = childEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("add masquerade for %s: %w: %s", guestIP, err, out)
	}
	return nil
}

func RemoveMasquerade(guestIP string, hostIface string) error {
	args := DeleteMasqueradeArgs(guestIP, hostIface)
	cmd := exec.Command("iptables", args...)
	cmd.Env = childEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("remove masquerade for %s: %w: %s", guestIP, err, out)
	}
	return nil
}

func DetectDefaultInterface() (string, error) {
	cmd := exec.Command("ip", "route", "get", "1.1.1.1")
	cmd.Env = childEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("detect default interface: %w: %s", err, out)
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return "", fmt.Errorf("detect default interface: 'dev' not found in: %s", out)
}

func CheckIPForward() error {
	cmd := exec.Command("sysctl", "-n", "net.ipv4.ip_forward")
	cmd.Env = childEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("check ip_forward: %w: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "1" {
		return fmt.Errorf("net.ipv4.ip_forward is not enabled; run: sysctl -w net.ipv4.ip_forward=1")
	}
	return nil
}

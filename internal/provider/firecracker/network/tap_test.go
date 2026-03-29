package network

import (
	"testing"
)

func TestTapName(t *testing.T) {
	name := TapName("nvrs-fc-a1b2c3d4")
	if name != "fc-a1b2c3d4" {
		t.Errorf("got %q, want %q", name, "fc-a1b2c3d4")
	}
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

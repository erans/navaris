//go:build incus

package incus

import (
	"context"
	"errors"
	"testing"
)

func TestIsCowDriver(t *testing.T) {
	cases := []struct {
		driver string
		cow    bool
	}{
		{"dir", false},
		{"btrfs", true},
		{"zfs", true},
		{"lvm", false},
		{"lvmcluster", false},
		{"lvm-thin", true},
		{"ceph", true},
		{"cephfs", true},
		{"powerflex", true}, // unknown driver: default to capable
		{"", false},
	}
	for _, c := range cases {
		if got := isCowDriver(c.driver); got != c.cow {
			t.Errorf("isCowDriver(%q) = %v, want %v", c.driver, got, c.cow)
		}
	}
}

func TestClassifyPool_DirReturnsAdvisory(t *testing.T) {
	advisory := classifyPool("dir")
	if advisory == nil {
		t.Fatal("expected an advisory for dir driver")
	}
	if !errors.Is(advisory, ErrIncusPoolNotCoW) {
		t.Errorf("advisory should wrap ErrIncusPoolNotCoW, got %v", advisory)
	}
}

func TestClassifyPool_BtrfsNoAdvisory(t *testing.T) {
	if classifyPool("btrfs") != nil {
		t.Error("btrfs driver must not produce an advisory")
	}
}

func TestCheckPool_FetchErrorDoesNotGateStartup(t *testing.T) {
	// Non-strict mode: fetch failure logs and returns nil.
	err := CheckPool(context.Background(), func(ctx context.Context) (string, error) {
		return "", errors.New("simulated fetch failure")
	}, false)
	if err != nil {
		t.Errorf("non-strict CheckPool with fetch error should return nil, got %v", err)
	}
}

func TestCheckPool_StrictAdvisoryFails(t *testing.T) {
	err := CheckPool(context.Background(), func(ctx context.Context) (string, error) {
		return "dir", nil
	}, true)
	if err == nil {
		t.Fatal("strict CheckPool with non-CoW driver must error")
	}
	if !errors.Is(err, ErrIncusPoolNotCoW) {
		t.Errorf("expected ErrIncusPoolNotCoW, got %v", err)
	}
}

func TestCheckPool_NonStrictAdvisoryWarns(t *testing.T) {
	// Non-strict with non-CoW driver: warning only, returns nil.
	err := CheckPool(context.Background(), func(ctx context.Context) (string, error) {
		return "dir", nil
	}, false)
	if err != nil {
		t.Errorf("non-strict CheckPool with advisory should return nil, got %v", err)
	}
}

func TestCheckPool_CowDriverNoAdvisory(t *testing.T) {
	err := CheckPool(context.Background(), func(ctx context.Context) (string, error) {
		return "btrfs", nil
	}, true)
	if err != nil {
		t.Errorf("btrfs driver must not produce an error even in strict mode, got %v", err)
	}
}

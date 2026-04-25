//go:build linux

package storage

import "testing"

func TestReflinkBackend_Capabilities_Linux(t *testing.T) {
	caps := (ReflinkBackend{}).Capabilities()
	if !caps.InstantClone || !caps.SharesBlocks || !caps.RequiresSameFS {
		t.Errorf("ReflinkBackend caps want all-true on Linux, got %+v", caps)
	}
}

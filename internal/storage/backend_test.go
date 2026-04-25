package storage

import (
	"errors"
	"testing"
)

func TestErrUnsupported_Is(t *testing.T) {
	wrapped := errors.New("zfs not configured: " + ErrUnsupported.Error())
	if errors.Is(wrapped, ErrUnsupported) {
		t.Errorf("plain string-wrapping should not match ErrUnsupported")
	}
	wrapped2 := errors.Join(ErrUnsupported, errors.New("zfs not configured"))
	if !errors.Is(wrapped2, ErrUnsupported) {
		t.Errorf("errors.Join should preserve ErrUnsupported")
	}
}

func TestCapabilitiesZeroValue(t *testing.T) {
	var c Capabilities
	if c.InstantClone || c.SharesBlocks || c.RequiresSameFS {
		t.Errorf("zero Capabilities must be all-false")
	}
}

package storage

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrUnsupported_Is(t *testing.T) {
	wrapped := fmt.Errorf("ctx: %v", ErrUnsupported)
	if errors.Is(wrapped, ErrUnsupported) {
		t.Errorf("%%v wrapping must not establish an errors.Is chain")
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

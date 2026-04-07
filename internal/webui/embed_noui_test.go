//go:build !withui

package webui_test

import (
	"testing"

	"github.com/navaris/navaris/internal/webui"
)

func TestAssetsNilWithoutTag(t *testing.T) {
	if webui.Assets != nil {
		t.Fatalf("webui.Assets should be nil without -tags withui, got %v", webui.Assets)
	}
}

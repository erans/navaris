//go:build !withui

package webui

import "io/fs"

// Assets is nil when the binary is built without -tags withui.
// Callers must treat nil as "UI disabled".
var Assets fs.FS = nil

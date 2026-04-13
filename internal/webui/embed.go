//go:build withui

package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// Assets holds the built SPA files rooted at dist/.
var Assets fs.FS

func init() {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// Panic at startup if the embedded tree is malformed — this is a build bug.
		panic("webui: embedded dist fs.Sub failed: " + err.Error())
	}
	Assets = sub
}

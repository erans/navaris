package webui

import (
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// NewAssetHandler returns an http.Handler that serves the embedded SPA.
// It:
//   - returns 404 for paths starting with /v1/ or /ui/ (defense in depth)
//   - serves static assets under /assets/* directly from the filesystem
//   - serves index.html for / and any other path (SPA deep-link fallback)
//   - returns 404 if assetsFS is nil (UI disabled)
func NewAssetHandler(assetsFS fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if assetsFS == nil {
			http.NotFound(w, r)
			return
		}
		p := r.URL.Path
		if strings.HasPrefix(p, "/v1/") || strings.HasPrefix(p, "/ui/") {
			http.NotFound(w, r)
			return
		}

		// Static assets come from a real file under /assets/.
		if strings.HasPrefix(p, "/assets/") {
			serveFile(w, r, assetsFS, strings.TrimPrefix(p, "/"))
			return
		}

		// Everything else (including /) falls back to index.html.
		serveFile(w, r, assetsFS, "index.html")
	})
}

func serveFile(w http.ResponseWriter, r *http.Request, assetsFS fs.FS, name string) {
	f, err := assetsFS.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)

	// Cache headers: hashed assets get a long TTL; index.html always revalidates.
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	_, _ = io.Copy(w, f)
}

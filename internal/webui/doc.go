// Package webui bundles the Navaris web frontend and its session layer.
//
// When built with -tags withui the SPA is embedded via go:embed and the
// exported Assets variable is a fs.FS rooted at the built dist directory.
// Without the tag, Assets is nil and the server registers no SPA routes.
package webui

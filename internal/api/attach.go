package api

import "net/http"

// attachSandbox is a placeholder that Task 13 replaces with the real
// WebSocket bridge.
func (s *Server) attachSandbox(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

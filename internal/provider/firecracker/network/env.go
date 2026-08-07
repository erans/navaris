package network

import (
	"os"
	"strings"
)

// secretEnvVars are stripped from child process environments. The daemon
// reads these from its own environment as an alternative to argv flags;
// short-lived helpers (ip, iptables, sysctl) have no need for them, and
// same-UID processes can read /proc/<pid>/environ.
var secretEnvVars = []string{
	"NAVARIS_AUTH_TOKEN",
	"NAVARIS_UI_PASSWORD",
	"NAVARIS_UI_SESSION_KEY",
}

// childEnv returns the current environment minus navaris secrets. It is a
// targeted strip, NOT a blanket NAVARIS_* filter: non-secret config may
// legitimately flow to children.
func childEnv() []string {
	env := os.Environ()
	for _, name := range secretEnvVars {
		prefix := name + "="
		filtered := env[:0]
		for _, e := range env {
			if !strings.HasPrefix(e, prefix) {
				filtered = append(filtered, e)
			}
		}
		env = filtered
	}
	return env
}

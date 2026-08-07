package network

import (
	"strings"
	"testing"
)

func TestChildEnvStripsSecrets(t *testing.T) {
	t.Setenv("NAVARIS_AUTH_TOKEN", "tok")
	t.Setenv("NAVARIS_UI_PASSWORD", "pw")
	t.Setenv("NAVARIS_UI_SESSION_KEY", "key")
	t.Setenv("NAVARIS_LOG_LEVEL", "debug") // non-secret NAVARIS_* must survive

	env := childEnv()
	joined := strings.Join(env, "\n")
	for _, name := range []string{"NAVARIS_AUTH_TOKEN", "NAVARIS_UI_PASSWORD", "NAVARIS_UI_SESSION_KEY"} {
		if strings.Contains(joined, name+"=") {
			t.Errorf("child env must not contain %s", name)
		}
	}
	if !strings.Contains(joined, "NAVARIS_LOG_LEVEL=debug") {
		t.Error("non-secret NAVARIS_LOG_LEVEL should be preserved")
	}
}

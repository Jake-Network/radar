package cli

import (
	"os"
	"testing"
)

// TestMain isolates every test from the developer's workspace registry and
// run records; workspace tests set their own directories.
func TestMain(m *testing.M) {
	config, err := os.MkdirTemp("", "radar-test-config-")
	if err != nil {
		panic(err)
	}
	state, err := os.MkdirTemp("", "radar-test-state-")
	if err != nil {
		panic(err)
	}
	os.Setenv("RADAR_CONFIG_DIR", config)
	os.Setenv("RADAR_STATE_DIR", state)
	code := m.Run()
	os.RemoveAll(config)
	os.RemoveAll(state)
	os.Exit(code)
}

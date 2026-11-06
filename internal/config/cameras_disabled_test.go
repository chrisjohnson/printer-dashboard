package config

import (
	"reflect"
	"testing"
)

// TestCamerasDisabledByEnv pins the accepted spellings. The flag exists to be
// typed into a `docker run -e` line under time pressure, so the common human
// variants must work and the empty/unset case must not accidentally disable
// cameras.
func TestCamerasDisabledByEnv(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"no", false},
		{"off", false},
		{"lan", false}, // a stray transport value must not disable cameras
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"yes", true},
		{"on", true},
		{" 1 ", true},
		{" True ", true},
	}

	for _, tc := range cases {
		t.Run("DISABLE_CAMERAS="+tc.value, func(t *testing.T) {
			t.Setenv("DISABLE_CAMERAS", tc.value)
			if got := CamerasDisabledByEnv(); got != tc.want {
				t.Errorf("CamerasDisabledByEnv() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCameraToggleIsNotAConfigKey guards the design decision itself.
//
// The toggle is an environment variable on purpose: config.yaml is shared
// machine-wide across every worktree's container because it describes the
// physical printers, so a config key could only disable cameras for everything
// at once — including production. Per-instance is the whole point. If someone
// later adds a config-file equivalent, this fails and forces that tradeoff to
// be argued for rather than arrived at by accident.
func TestCameraToggleIsNotAConfigKey(t *testing.T) {
	for _, name := range []string{"CamerasEnabled", "DisableCameras", "CamerasDisabled"} {
		if hasField(reflect.TypeOf(Config{}), name) {
			t.Errorf("Config has a %s field: the camera toggle must stay per-instance (DISABLE_CAMERAS env), not machine-wide (config.yaml)", name)
		}
	}
}

func hasField(t reflect.Type, name string) bool {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	_, ok := t.FieldByName(name)
	return ok
}

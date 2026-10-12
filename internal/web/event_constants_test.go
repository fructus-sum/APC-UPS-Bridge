package web

import (
	"strings"
	"testing"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
)

// The history and dashboard scripts match event types and detail keys by
// string. This keeps them in step with the Go constants.
func TestScriptsUseSharedEventNames(t *testing.T) {
	scripts := map[string][]string{
		"assets/history.js": {
			events.TypePowerLost, events.TypeOnBatteryDetected, events.TypePowerRestored,
			events.TypeBatteryFullyCharged, events.DetailBatteryPercent, events.DetailRuntimeMinutes,
		},
		"assets/app.js": {events.DetailBatteryPercent, events.DetailRuntimeMinutes},
	}
	for file, names := range scripts {
		data, err := static.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, name := range names {
			if !strings.Contains(string(data), name) {
				t.Errorf("%s no longer mentions %q", file, name)
			}
		}
	}
}

package firmware

import (
	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"testing"
)

func TestFirmwareStatus(t *testing.T) {
	tests := []struct{ name, model, firmware, want string }{
		{"older version", "SMT1500C", "UPS 17.2 / ID=1027", "outdated"},
		{"current", "SMT1500C", "UPS 18.0 / ID=1027", "current"},
		{"unknown model", "Back-UPS RS 1500", "UPS 01.0 / ID=1027", "unknown"},
		{"unidentified Smart UPS", "Smart-UPS 1500", "UPS 17.2 / ID=1027", "unknown"},
		{"unknown UPS ID", "SMT1500C", "UPS 1.0 / ID=9999", "unknown"},
		{"missing firmware", "SMT1500C", "", "unknown"},
		{"legacy not upgradeable", "SUA1500I", "UPS 03.1 / ID=1027", "not_upgradeable"},
		{"known exception", "SMT2200", "UPS 17.2 / ID=1027", "unknown"},
		{"different family", "SMX1500", "UPS 17.2 / ID=1027", "unknown"},
		{"malformed firmware", "SMT1500C", "17.2", "unknown"},
		{"newer installed", "SMT1500C", "UPS 19.0 / ID=1027", "unknown"},
		{"matching family", "SMC1500C", "UPS 17.2 / ID=1018", "outdated"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Check(&apc.Snapshot{Model: tc.model, Firmware: tc.firmware})
			if result.Status != tc.want {
				t.Fatalf("got %q want %q: %+v", result.Status, tc.want, result)
			}
		})
	}
}
func TestUnavailable(t *testing.T) {
	if got := Check(nil); got.Status != "unavailable" {
		t.Fatalf("%+v", got)
	}
}

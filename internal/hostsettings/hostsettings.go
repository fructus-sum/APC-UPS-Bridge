package hostsettings

import "fmt"

type Settings struct {
	Managed bool `json:"managed"`
	BatteryPercent int `json:"battery_percent"`
	RuntimeMinutes int `json:"runtime_minutes"`
}

func (s Settings) Validate() error {
	if s.BatteryPercent < 5 || s.BatteryPercent > 95 { return fmt.Errorf("battery shutdown threshold must be between 5 and 95 percent") }
	if s.RuntimeMinutes < 0 || s.RuntimeMinutes > 120 { return fmt.Errorf("runtime shutdown threshold must be between 0 and 120 minutes") }
	return nil
}

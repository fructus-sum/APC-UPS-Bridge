package events

// Event types written by the monitor and read back by the reports and history
// graphs. The dashboard and history scripts match on the same strings.
const (
	TypePowerLost                = "power_lost"
	TypeOnBatteryDetected        = "on_battery_detected"
	TypePowerRestored            = "power_restored"
	TypeLowBattery               = "low_battery"
	TypeUPSCommunicationLost     = "ups_communication_lost"
	TypeUPSCommunicationRestored = "ups_communication_restored"
	TypeBatteryFullyCharged      = "battery_fully_charged"
)

// Detail keys shared between the packages that write and read event details.
const (
	DetailBatteryPercent        = "battery_percent"
	DetailRuntimeMinutes        = "runtime_minutes"
	DetailObservedOutageSeconds = "observed_outage_seconds"
)

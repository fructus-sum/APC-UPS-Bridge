// Package apc reads and parses the text status output of apcupsd's apcaccess.
package apc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PowerState is a conservative interpretation of apcupsd's STATUS flags.
type PowerState string

const (
	Unknown   PowerState = "UNKNOWN"
	Online    PowerState = "ONLINE"
	OnBattery PowerState = "ON_BATTERY"
)

// Snapshot describes one UPS reading. Unsupported values remain nil, not zero.
type Snapshot struct {
	ObservedAt     time.Time  `json:"observed_at"`
	Name           string     `json:"name,omitempty"`
	Model          string     `json:"model,omitempty"`
	Status         string     `json:"status"`
	Flags          []string   `json:"flags"`
	Power          PowerState `json:"power"`
	BatteryPercent *float64   `json:"battery_percent,omitempty"`
	RuntimeMinutes *float64   `json:"runtime_minutes,omitempty"`
	LoadPercent    *float64   `json:"load_percent,omitempty"`
	InputVolts     *float64   `json:"input_volts,omitempty"`
	OutputVolts    *float64   `json:"output_volts,omitempty"`
}

// Parse accepts any apcaccess output containing a STATUS line. Unknown metrics
// are deliberately tolerated, as older APC models supply fewer fields.
func Parse(data []byte, now time.Time) (Snapshot, error) {
	fields := make(map[string]string)
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 4096), 1024*1024)
	for scan.Scan() {
		line := scan.Text()
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.ToUpper(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		fields[key] = strings.TrimSpace(value)
	}
	if err := scan.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("scan apcaccess: %w", err)
	}
	status := strings.ToUpper(strings.TrimSpace(fields["STATUS"]))
	if status == "" {
		return Snapshot{}, errors.New("apcaccess output has no STATUS field")
	}
	flags := strings.Fields(status)
	snap := Snapshot{
		ObservedAt: now.UTC(), Name: fields["UPSNAME"], Model: fields["MODEL"],
		Status: status, Flags: flags, Power: powerFromFlags(flags),
		BatteryPercent: metric(fields["BCHARGE"], true),
		RuntimeMinutes: metric(fields["TIMELEFT"], false),
		LoadPercent:    metric(fields["LOADPCT"], false),
		InputVolts:     metric(fields["LINEV"], false),
		OutputVolts:    metric(fields["OUTPUTV"], false),
	}
	return snap, nil
}

func metric(value string, percent bool) *float64 {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return nil
	}
	number, err := strconv.ParseFloat(strings.TrimSuffix(parts[0], "%"), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return nil
	}
	if percent && number > 100 {
		return nil
	}
	return &number
}

func powerFromFlags(flags []string) PowerState {
	var online, battery bool
	for _, f := range flags {
		switch f {
		case "COMMLOST", "NOBATT", "SHUTTING", "DOWN", "SLAVEDOWN":
			return Unknown
		case "ONLINE":
			online = true
		case "ONBATT":
			battery = true
		}
	}
	if online == battery {
		return Unknown
	} // absent or contradictory
	if battery {
		return OnBattery
	}
	return Online
}

// Reader abstracts the hardware-facing command for repeatable tests.
type Reader interface {
	Read(context.Context) (Snapshot, error)
}

// CommandReader invokes apcaccess status with a fixed argument list. It never
// shells out and times out even if apcupsd is unreachable.
type CommandReader struct {
	Path    string
	Timeout time.Duration
}

func (r CommandReader) Read(ctx context.Context) (Snapshot, error) {
	path := r.Path
	if path == "" {
		path = "apcaccess"
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "status")
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return Snapshot{}, fmt.Errorf("apcaccess: %w", ctx.Err())
		}
		return Snapshot{}, fmt.Errorf("apcaccess failed: %w", err)
	}
	snap, err := Parse(output, time.Now())
	if err != nil {
		return Snapshot{}, fmt.Errorf("apcaccess parse: %w", err)
	}
	return snap, nil
}

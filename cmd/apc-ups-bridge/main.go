// APC UPS Bridge Phase 2: read-only monitoring core.
// No web interface, remote shutdown, or Wake-on-LAN is enabled by this binary.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
)

func main() { os.Exit(run()) }

func run() int {
	command := flag.String("apcaccess", "apcaccess", "apcaccess executable (resolved from PATH, or absolute path)")
	interval := flag.Duration("interval", 10*time.Second, "UPS polling interval")
	timeout := flag.Duration("timeout", 4*time.Second, "maximum time for each apcaccess command")
	logDir := flag.String("log-dir", "/var/log/apc-ups-bridge", "directory for event-only monthly JSONL files")
	retention := flag.Int("retention-days", 365, "event-log retention days; 0 keeps logs forever")
	low := flag.Float64("low-battery", 20, "event-only low battery warning threshold percent (no shutdown)")
	once := flag.Bool("once", false, "read once and print JSON status, then exit")
	flag.Parse()
	if *interval <= 0 || *timeout <= 0 || *retention < 0 {
		fmt.Fprintln(os.Stderr, "interval and timeout must be positive; retention must be nonnegative")
		return 2
	}
	reader := apc.CommandReader{Path: *command, Timeout: *timeout}
	mon, err := monitor.New(reader, events.FileWriter{Dir: *logDir}, *low)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	logger := log.New(os.Stderr, "apc-ups-bridge: ", log.LstdFlags|log.LUTC)
	if n, err := events.Cleanup(*logDir, *retention, time.Now()); err != nil {
		logger.Printf("log retention cleanup failed: %v", err)
	} else if n > 0 {
		logger.Printf("removed %d old event log files", n)
	}

	if *once {
		err := mon.Poll(context.Background())
		output, marshalErr := json.MarshalIndent(mon.Snapshot(), "", "  ")
		if marshalErr != nil {
			logger.Printf("encode status: %v", marshalErr)
			return 1
		}
		fmt.Println(string(output))
		if err != nil {
			logger.Printf("UPS poll: %v", err)
			return 1
		}
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	lastPollError := false
	poll := func() {
		err := mon.Poll(ctx)
		if err != nil && ctx.Err() == nil && !lastPollError {
			logger.Printf("UPS poll failed (repeated failures suppressed until recovery): %v", err)
		}
		if err == nil && lastPollError {
			logger.Printf("UPS polling recovered")
		}
		lastPollError = err != nil
	}
	poll()
	pollTick := time.NewTicker(*interval)
	defer pollTick.Stop()
	cleanupTick := time.NewTicker(24 * time.Hour)
	defer cleanupTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0
		case <-pollTick.C:
			poll()
		case <-cleanupTick.C:
			if _, err := events.Cleanup(*logDir, *retention, time.Now()); err != nil {
				logger.Printf("log retention cleanup: %v", err)
			}
		}
	}
}

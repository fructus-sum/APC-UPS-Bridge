// APC UPS Bridge Phase 4: protected web monitoring and opt-in, fail-closed
// remote power automation. Local host shutdown is intentionally excluded.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fructus-sum/APC-UPS-Bridge/internal/apc"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/auth"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/config"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/events"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/monitor"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/notify"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/power"
	"github.com/fructus-sum/APC-UPS-Bridge/internal/web"
)

var version = "development" // overridden by -ldflags at release build time

func main() { os.Exit(run()) }
func run() int {
	showVersion := flag.Bool("version", false, "print application version and exit")
	command := flag.String("apcaccess", "apcaccess", "apcaccess executable (PATH or absolute path)")
	interval := flag.Duration("interval", 10*time.Second, "initial UPS poll interval, overridden by saved web settings")
	timeout := flag.Duration("timeout", 4*time.Second, "apcaccess command timeout")
	logDir := flag.String("log-dir", "/var/log/apc-ups-bridge", "directory for monthly event-only logs")
	retention := flag.Int("retention-days", 365, "initial retention days, 0=forever; overridden by saved settings")
	low := flag.Float64("low-battery", 20, "initial low battery warning percentage (no shutdown)")
	once := flag.Bool("once", false, "single hardware poll printed as JSON; requires no web password")
	listen := flag.String("listen", "127.0.0.1:8080", "web listening address (LAN opt-in: 0.0.0.0:8080)")
	authFile := flag.String("auth-file", "/var/lib/apc-ups-bridge/auth.json", "password hash file")
	settingsFile := flag.String("settings-file", "/etc/apc-ups-bridge/settings.json", "editable settings file")
	powerConfigFile := flag.String("power-config", "/etc/apc-ups-bridge/power.json", "approved remote devices and power rules")
	powerStateFile := flag.String("power-state", "/var/lib/apc-ups-bridge/power-state.json", "durable power-action state (must persist across reboot)")
	emailConfigPath := flag.String("email-config", "/var/lib/apc-ups-bridge/email.json", "SMTP account credentials file")
	emailStatePath := flag.String("email-state", "/var/lib/apc-ups-bridge/email-state.json", "persistent email incident and report state")
	setPassword := flag.Bool("set-password-stdin", false, "initialize/reset admin password from standard input, then exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("APC UPS Bridge", version)
		return 0
	}
	if *setPassword {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
		if err != nil {
			fmt.Fprintln(os.Stderr, "read password:", err)
			return 1
		}
		p := strings.TrimRight(string(b), "\r\n")
		if strings.ContainsAny(p, "\r\n") {
			fmt.Fprintln(os.Stderr, "password must occupy one line")
			return 2
		}
		if err = auth.Initialize(*authFile, p); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		fmt.Println("Administrator password configured. Restart a running service after CLI password reset.")
		return 0
	}
	if *interval < 5*time.Second || *interval > time.Minute || *interval%time.Second != 0 || *timeout <= 0 || *retention < 0 || *retention > 36500 {
		fmt.Fprintln(os.Stderr, "invalid duration/retention: poll interval must be whole seconds 5–60; timeout positive; retention 0–36500")
		return 2
	}
	logger := log.New(os.Stderr, "apc-ups-bridge: ", log.LstdFlags|log.LUTC)
	defaults := config.Settings{PollIntervalSeconds: int((*interval) / time.Second), LowBatteryPercent: *low, RetentionDays: *retention}
	apply := make(chan config.Settings, 1)
	settingsChanged := func(s config.Settings) {
		select {
		case apply <- s:
		default:
			{
				select {
				case <-apply:
				default:
				}
				apply <- s
			}
		}
	}
	settings, err := config.Open(*settingsFile, defaults, settingsChanged)
	if err != nil {
		logger.Print(err)
		return 2
	}
	current := settings.Get()
	reader := apc.CommandReader{Path: *command, Timeout: *timeout}
	mon, err := monitor.New(reader, events.FileWriter{Dir: *logDir}, current.LowBatteryPercent)
	if err != nil {
		logger.Print(err)
		return 2
	}
	clean := func(days int) {
		// Preserve event logs while an incident report is still pending.
		if b, readErr := os.ReadFile(*emailStatePath); readErr == nil {
			var pending struct {
				Current json.RawMessage `json:"current"`
			}
			if json.Unmarshal(b, &pending) != nil || (len(pending.Current) > 0 && string(pending.Current) != "null") {
				return
			}
		} else if readErr != nil && !os.IsNotExist(readErr) {
			return
		}
		if n, e := events.Cleanup(*logDir, days, time.Now()); e != nil {
			logger.Printf("event cleanup: %v", e)
		} else if n > 0 {
			logger.Printf("removed %d expired monthly log files", n)
		}
	}
	clean(current.RetentionDays)
	if *once {
		err := mon.Poll(context.Background())
		data, encErr := json.MarshalIndent(mon.Snapshot(), "", "  ")
		if encErr != nil {
			logger.Print(encErr)
			return 1
		}
		fmt.Println(string(data))
		if err != nil {
			logger.Printf("poll error: %v", err)
			return 1
		}
		return 0
	}
	admin, err := auth.Open(*authFile)
	if err != nil {
		logger.Printf("cannot start web server: %v; configure with --set-password-stdin", err)
		return 2
	}
	powerStore, err := power.OpenConfig(*powerConfigFile)
	if err != nil {
		logger.Printf("power automation config (fail closed): %v", err)
		return 2
	}
	powerController, err := power.OpenController(powerStore, *powerStateFile, events.FileWriter{Dir: *logDir}, power.LinuxExecutor{})
	if err != nil {
		logger.Printf("power automation state (fail closed): %v", err)
		return 2
	}
	mailStore, err := notify.OpenConfig(*emailConfigPath)
	if err != nil {
		logger.Printf("mail configuration: %v", err)
		return 2
	}
	mailEngine, err := notify.Open(mailStore, *emailStatePath, *logDir, events.FileWriter{Dir: *logDir}, notify.SMTPSender{})
	if err != nil {
		logger.Printf("mail state: %v", err)
		return 2
	}
	handler, err := web.New(mon, admin, settings, *logDir, powerController)
	if err == nil {
		web.AttachEmail(handler, mailEngine)
	}
	if err != nil {
		logger.Print(err)
		return 2
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		logger.Printf("listen %s: %v", *listen, err)
		return 1
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()
	go func() {
		if e := server.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
			logger.Printf("web server stopped: %v", e)
			cancel()
		}
	}()
	logger.Printf("web interface listening at %s (password protected)", listener.Addr())
	defer func() {
		timeoutCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = server.Shutdown(timeoutCtx)
	}()
	emailUpdates := make(chan notify.Sample, 64)
	go mailEngine.Run(ctx, emailUpdates, func(err error) { logger.Printf("email report state: %v", err) })
	lastPollError := false
	lastPowerError := false
	poll := func() {
		err := mon.Poll(ctx)
		if err != nil && ctx.Err() == nil && !lastPollError {
			logger.Printf("UPS poll error (repeats suppressed): %v", err)
		}
		if err == nil && lastPollError {
			logger.Print("UPS polling restored")
		}
		lastPollError = err != nil
		status := mon.Snapshot()
		automationErr := powerController.Observe(ctx, status)
		if automationErr != nil && ctx.Err() == nil && !lastPowerError {
			logger.Printf("power automation error (repeats suppressed): %v", automationErr)
		}
		if automationErr == nil && lastPowerError {
			logger.Print("power automation operating normally")
		}
		lastPowerError = automationErr != nil
		pcfg := powerStore.Get()
		var targets []notify.Target
		for _, d := range pcfg.Devices {
			if d.ShutdownEnabled {
				targets = append(targets, notify.Target{ID: d.ID, Name: d.Name, Host: d.Host, Port: d.SSHPort})
			}
		}
		select {
		case emailUpdates <- notify.Sample{UPS: status, Targets: targets}:
		default:
			logger.Print("email worker backed up; newest UPS notification sample dropped")
		}
	}
	poll()
	tick := time.NewTicker(time.Duration(current.PollIntervalSeconds) * time.Second)
	defer tick.Stop()
	cleanupTick := time.NewTicker(24 * time.Hour)
	defer cleanupTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return 0
		case <-tick.C:
			poll()
		case next := <-apply:
			if err = mon.SetLowBatteryThreshold(next.LowBatteryPercent); err != nil {
				logger.Print(err)
				continue
			}
			tick.Reset(time.Duration(next.PollIntervalSeconds) * time.Second)
			if next.RetentionDays != current.RetentionDays {
				clean(next.RetentionDays)
			}
			current = next
			logger.Print("monitoring settings applied")
		case <-cleanupTick.C:
			clean(current.RetentionDays)
		}
	}
}

# APC UPS Bridge

**Give older USB-connected APC UPS units a new life on the network.**

APC UPS Bridge is a lightweight, self-hosted gateway for monitoring and protecting equipment powered by legacy APC USB uninterruptible power supplies. A Raspberry Pi or another Linux computer will connect to the UPS over USB and make status available across the local network.

> **Development status: Phase 2 monitoring core implemented for review, not a production release.** There is no web dashboard, installer, or working remote shutdown/recovery yet. Do not rely on this version to protect equipment.

## Design goals

- APC USB UPS support through `apcupsd` / `apcaccess` first.
- One lightweight application, minimal dependencies, no database and no cloud services.
- Live web dashboard and password-protected administration (planned).
- Configurable remote shutdown and Wake-on-LAN recovery (planned, disabled until tested).
- Event-only logs with adjustable retention, including unlimited.
- Autostart, secure installation, and Raspberry Pi/Linux support (planned).

## Phase 2 monitoring core

Go 1.22+ is needed to compile, but the compiled executable has no extra Go runtime dependency. An independently configured `apcupsd` and `apcaccess` remain necessary.

```sh
go test ./...
go build -o apc-ups-bridge ./cmd/apc-ups-bridge
./apc-ups-bridge --once --log-dir ./test-events
```

For more details, flags, and logging behaviour see [Phase 2 usage](docs/PHASE-2.md) and the [Architecture proposal](docs/ARCHITECTURE.md).

## Security

No remotely accessible web service is running in Phase 2. Once a web interface is added, authentication will be required for administration and internet exposure will not be recommended. This project is independent and not affiliated with or endorsed by APC or Schneider Electric.

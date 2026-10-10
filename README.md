# APC UPS Bridge

**Give older USB-connected APC UPS units a new life on the network.**

APC UPS Bridge is a planned lightweight, self-hosted gateway for monitoring and protecting equipment powered by legacy APC USB uninterruptible power supplies (UPS). A Raspberry Pi or another Linux computer connects to the UPS over USB and makes its status available across the local network.

> **Project status: planning only.** There is no released application or working installer yet. Do not rely on this repository for power protection until the software has been implemented and tested.

## Design goals

- Support APC USB UPS units compatible with `apcupsd` and `apcaccess` first.
- Live, responsive network dashboard showing only readings supported by the connected UPS.
- Password-protected administration and configurable remote SSH shutdown / Wake-on-LAN recovery.
- No database, no cloud service, and as few dependencies and exposed ports as possible.
- Record significant events only, with configurable long-term log retention.
- Run unattended at boot on Raspberry Pi OS and other supported systemd Linux distributions.
- Make installation, backup, updates and removal straightforward and documented.

## Planned first release

The first release will focus on one locally attached APC USB UPS, a single lightweight application process, reliable power-state handling, a simple browser interface, and event logs. It will use the existing `apcupsd` daemon to communicate with the hardware rather than implementing a competing USB driver.

A network UPS gateway is not the same as adding a physical network card to the UPS. The Linux host remains connected to the UPS by USB and must be powered appropriately.

## Security

The intended default is local-network operation with authentication for administration. External internet exposure and port forwarding are not recommended. The monitoring web service will run without root privileges; elevated shutdown actions will be restricted and explicitly configured.

## Development

Architecture and scope are being defined before implementation. Source code, installation instructions and compatibility results will be published as they become available.

APC is a trademark of its respective owner. This independent project is not affiliated with or endorsed by APC or Schneider Electric.

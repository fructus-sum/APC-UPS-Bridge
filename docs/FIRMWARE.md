# Firmware checker (manual updates only)

APC UPS Bridge reads the `MODEL`, optional `SKU`, and `FIRMWARE` fields reported by `apcaccess status`. It does **not** install firmware, run update commands, or require an additional service, port, database, scheduled network request, or Go dependency.

The authenticated dashboard **always** displays the UPS's reported firmware string (or **Unknown** when absent), UPS ID if identifiable, and a separate firmware update status. An **Unknown** update status does not mean the device is outdated. When communication fails, the display says that the UPS is unavailable rather than showing a stale version as current.

On the authenticated dashboard, a warning appears **only when** the firmware string explicitly provides a UPS firmware revision and UPS ID, the device's model/SKU explicitly identifies a supported series, and that **series + UPS ID** is present in the reviewed reference table in `internal/firmware/firmware.go`. The reference is manually curated using [Schneider Electric FA279197](https://www.se.com/us/en/faqs/FA279197/) and was reviewed on 2026-10-10. Check the official guide before acting: this local reference can become stale.

The checker is deliberately conservative. Many old APC USB models report no usable ID, model SKU, or firmware version; for those UPSes **no out-of-date claim is made**. Some legacy Smart-UPS families are not user-upgradeable. The API `GET /api/firmware` returns statuses `outdated`, `current`, `unknown`, `not_upgradeable`, or `unavailable`, but only an `outdated` result generates a dashboard warning. Device communication loss suppresses any old warning. This is a local **reference comparison**, not a live firmware lookup.

Firmware updates, where supported, may interrupt battery backup or output power, and some models require a serial connection. Use the manufacturer guide and an appropriate maintenance window. Never assume the USB monitoring connection means USB flashing is supported. Do not flash firmware through APC UPS Bridge.

### Updating the reference

Review Schneider's firmware table and release notes. Modify `latest` entries **only** for confirmed `series/UPS ID` pairs, and update `ReferenceDate`. Keep model exceptions and exclusion checks current. Add a test for every new family or special case. The web UI warning remains advisory and links to the manufacturer; it performs no network requests.

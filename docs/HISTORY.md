# Voltage, load, battery and runtime history

The authenticated **History graphs** tab plots UPS input voltage, output voltage, load percentage, battery charge, estimated runtime and observed mains power status across three separate graphs. The default view is **14 days**, selectable ranges are 24 hours, 7 days, 14 days and 30 days. All graph points use real UTC timestamps, displayed in your browser's local time zone.

## Storage, coverage and retention

- Separate, small JSON Lines files in `/var/lib/apc-ups-bridge/history/samples-YYYY-MM-DD.jsonl`, independent of monthly event-only logs. No database, charting library, second daemon or extra port.
- One reading per **15-minute UTC interval**, only while the UPS is reachable and reports at least one valid load/voltage, battery charge or runtime measurement; this is roughly 96 small records a day (about 2,880 records for 30 days).
- Daily files older than the **30-day** retention boundary are cleaned automatically; the API never returns samples older than the chosen lookback window. The boundary day's file may be held until the following cleanup to avoid constant rewrites.
- Current interval readings are taken at the first healthy poll of that interval. Rebooting the Pi in the same interval does not create duplicate records. No regular status samples are added to the existing event logs.
- Load is `LOADPCT` (UPS output-load percentage), **not household energy consumption or kWh**. Voltage is `LINEV` (input) and `OUTPUTV` (output), battery charge is `BCHARGE`, estimated runtime is `TIMELEFT` when reported. Unsupported fields remain missing. The third graph’s mains-online/UPS-on-battery status is the *observed power mode*, **not the uptime of Debian, the Pi, or a remote host**.

## Handling missing periods correctly

The server returns a full 15-minute **time grid**, including explicit empty points. Old stored samples without the new battery/runtime fields are still supported. The SVG charts place samples by time on a true linear scale and break the line at every missing/unsupported reading. An off or rebooting Pi has no samples and thus shows a gap, not a synthetic voltage of 0 V or a straight connecting line. There is **no interpolation**.

Available historical event logs are overlaid as timestamped **on-battery intervals**, where the start and restoration were actually observed. For the third chart, existing `power_lost`, `on_battery_detected`, `low_battery`, `power_restored` and new `battery_fully_charged` events backfill actual battery or runtime values **only where the event recorded that specific numeric value**. They are isolated dots, not synthetic continuous readings. The monitor now logs a `battery_fully_charged` event on an **observed** return to 100% after previously observing a lower charge. Restart or lost connectivity can make this transition unobservable. Events cannot reconstruct voltage, load or any readings between observed times. The 30-day graph history therefore starts accumulating when this version is installed; older graphs only backfill numeric battery or runtime readings explicitly present in recorded event details; other readings remain unknown.

The graph API `GET /api/history?days=14` is available to authenticated users only, on the existing dashboard port. It reads at most 30 days of compact sample files and a bounded number of already retained UPS event records. Invalid ranges are rejected.

## Testing

`go test ./... && go test -race ./... && go vet ./...` cover interval deduplication across reboots, absent data, UTC spacing, event timing, retention and authenticated web access. Real USB UPS testing and browser visual checks are still recommended before publication.

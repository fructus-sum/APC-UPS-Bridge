# Voltage and UPS load history

The authenticated **History graphs** tab plots UPS input voltage, output voltage and UPS load as a percentage of rated capacity. The default view is **14 days**, selectable ranges are 24 hours, 7 days, 14 days and 30 days. All graph points use real UTC timestamps, displayed in your browser's local time zone.

## Storage, coverage and retention

- Separate, small JSON Lines files in `/var/lib/apc-ups-bridge/history/samples-YYYY-MM-DD.jsonl`, independent of monthly event-only logs. No database, charting library, second daemon or extra port.
- One reading per **15-minute UTC interval**, only while the UPS is reachable and reports at least one valid load/voltage measurement; this is roughly 96 small records a day (about 2,880 records for 30 days).
- Daily files older than the **30-day** retention boundary are cleaned automatically; the API never returns samples older than the chosen lookback window. The boundary day's file may be held until the following cleanup to avoid constant rewrites.
- Current interval readings are taken at the first healthy poll of that interval. Rebooting the Pi in the same interval does not create duplicate records. No regular status samples are added to the existing event logs.
- Load is `LOADPCT` (UPS output-load percentage), **not household energy consumption or kWh**. Voltage is `LINEV` (input) and `OUTPUTV` (output) when provided by the UPS. Unsupported fields remain missing.

## Handling missing periods correctly

The server returns a full 15-minute **time grid**, including explicit empty points. The SVG charts place samples by time on a true linear scale and break the line at every missing/unsupported reading. An off or rebooting Pi has no samples and thus shows a gap, not a synthetic voltage of 0 V or a straight connecting line. There is **no interpolation**.

Available historical event logs are overlaid as timestamped **on-battery intervals**, where the start and restoration were actually observed. Events tell us when mains changed; they **cannot reconstruct the numeric voltage or load** that the old event-only logger never recorded. The 30-day graph history therefore starts accumulating when this version is installed; older graphs do not magically backfill numerical readings.

The graph API `GET /api/history?days=14` is available to authenticated users only, on the existing dashboard port. It reads at most 30 days of compact sample files and a bounded number of already retained UPS event records. Invalid ranges are rejected.

## Testing

`go test ./... && go test -race ./... && go vet ./...` cover interval deduplication across reboots, absent data, UTC spacing, event timing, retention and authenticated web access. Real USB UPS testing and browser visual checks are still recommended before publication.

# History graphs

The **History graphs** page draws three graphs from readings taken every 15 minutes: voltage, load, and battery with runtime. Times are shown in your browser's time zone.

## The graphs

- **Input & output voltage:** the voltage coming in and the voltage the UPS supplies.
- **UPS load:** how much of the UPS's capacity is in use. This is not energy use or kWh.
- **Battery charge & power availability:** battery charge, estimated runtime and whether mains power was online or the UPS was on battery. This is the power state the bridge saw, not how long the bridge host or any other device was running.

The voltage and load graphs share one **Time range** menu: **Last 24 hours**, **7 days**, **14 days** (the default) or **30 days**. The yellow shading marks the times the UPS was on battery.

The battery graph has its own **Battery history range** menu: **30 days** (the default), **Quarter (90 days)**, **Six months (180 days)** or **One year (365 days)**.

## Gaps are left as gaps

If the bridge host was off or the UPS could not be read, the graph shows a gap. It does not draw a zero or join the points across the gap. Anything the UPS does not report is left blank.

## Older readings and recorded events

Full readings are kept for 30 days. For the battery graph, longer ranges add single dots from recorded power events, only where an event noted a battery or runtime value. These dots do not recreate the readings between them, and they cannot recreate voltage or load.

Events used on the graphs are power lost, on battery detected, power restored, low battery, UPS communication lost, UPS communication restored and battery fully charged. Events older than your **Event retention (days)** setting on the Settings page are gone and cannot be recovered. For very long ranges, only the most recent 8,192 of these events are used, and this applies to both the voltage/load graphs and the battery graph.

The monitor records a full-charge event when the charge returns to 100% after being seen at 95% or lower. A charge that flickers between 99% and 100% does not repeat it. A return to 100% while the monitor was not watching, such as during a restart or a communication gap, is not recorded.

## For administrators

- One reading is stored per 15-minute slot, only while the UPS can be read and reports at least one valid measurement. That is about 96 small records a day, in `/var/lib/apc-ups-bridge/history/samples-YYYY-MM-DD.jsonl`, separate from the event logs. Restarting the bridge host within a slot does not add a duplicate.
- Files older than 30 days are cleaned up automatically. The day at the boundary may be kept until the next cleanup.
- The fields recorded are `LINEV` (input voltage), `OUTPUTV` (output voltage), `LOADPCT` (load), `BCHARGE` (battery charge) and `TIMELEFT` (runtime).
- `GET /api/history` accepts 1, 7, 14 or 30 days. `GET /api/history/battery` accepts 30, 90, 180 or 365 days and returns the latest 30 days of readings plus the recorded events for the whole range. Both need a signed-in session.
- No database, charting library or extra port is used. Graphs are drawn in the browser.
- Tests are described in the [architecture guide](ARCHITECTURE.md#development).

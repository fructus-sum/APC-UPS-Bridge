# Battery health and maintenance

On the dashboard, open **Show system details** under the UPS model. The **Battery health & maintenance** group lists what your UPS reports about its battery. It updates every 10 seconds and only displays information. It never changes UPS settings and never runs a test.

## What each line means

| Line | Meaning |
| --- | --- |
| Battery voltage | The battery's voltage right now. |
| Nominal battery voltage | The voltage the battery is designed for. |
| Battery replacement date | The date recorded when the battery was installed. A date does not prove the battery is healthy. |
| Last self-test | When the UPS last tested its battery. |
| Self-test result | Shown in plain words: Passed, No result yet, Failed (battery capacity too low), Failed (overload), In progress or Warning. Any other value is shown as the UPS reports it. |
| Automatic self-test | How often the UPS tests itself: **Every N hours**, **On** or **Off**. |

Which lines appear depends on your UPS model. A line shows **Not reported** when the UPS gives no value, gives a placeholder such as N/A, NA, NONE or NO, or reports a voltage of 0 V. A missing value is never shown as a zero or as a verdict on the battery.

## Testing the battery by hand

Select **Show manual battery test steps…** and confirm the message that appears. The steps then show the warning and the commands. The page never runs them. You run them yourself at the Linux host's console.

Warning: this test can run your connected equipment on battery power, and it could switch that equipment off if the battery is weak. While the UPS service is stopped, the bridge cannot watch the UPS and your Linux host will not shut itself down automatically. Only do this when someone is present and the mains power is normal.

On the server console, stop the UPS service and start the test:

```sh
sudo systemctl stop apcupsd
sudo apctest
```

Choose a self-test from the menu. When you are done, start the UPS service again and check that it is working:

```sh
sudo systemctl start apcupsd
apcaccess status
```

Some older UPS models do not support this test. If `apctest` offers no self-test for your UPS, do not try to force one another way.

## For administrators

The values come from the `BATTV`, `NOMBATTV`, `BATTDATE`, `LASTSTEST`, `SELFTEST` and `STESTI` fields of `apcaccess status`. This feature does not widen any sudo, service user or helper permissions.

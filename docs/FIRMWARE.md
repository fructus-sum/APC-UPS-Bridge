# Firmware check

APC UPS Bridge reads the firmware information your UPS reports and compares it with a short reference list taken from [Schneider Electric's firmware guide](https://www.se.com/us/en/faqs/FA279197/). It only tells you when a newer version is listed. It never installs, uploads or flashes firmware, and it does not look anything up online.

## Where to see it

- **Settings** has a **UPS firmware** panel with the **Installed firmware version**, the **Firmware update status** and the **UPS ID**.
- The dashboard stays quiet unless an update is listed. Then it shows "A firmware update is listed for your UPS. See Settings →".

## What the update status means

| Status | Meaning |
| --- | --- |
| Update listed | The reference lists a newer version for your UPS. The panel also shows your version, the listed version and the date the reference was reviewed. |
| Matches local reference | Your version is the one in the reference. This is not a live check, so a newer one may exist. |
| Not user-upgradeable | Schneider Electric lists this older Smart-UPS family as not upgradeable by the owner. |
| Unknown | The bridge cannot tell. This does not mean your firmware is out of date. |
| UPS unavailable | The bridge cannot talk to the UPS right now, so nothing can be checked. |

The installed version shows "Unknown (not reported by UPS)" if your UPS does not report one, and "Unknown (unable to check)" if the page could not load the details.

## When the status is Unknown

The check is cautious and only says "Update listed" when it is sure. It stays Unknown when:

- the UPS does not report a model, a firmware version or a UPS ID (many older APC USB models do not);
- the model name is too general to tell which product line it belongs to, such as "Smart-UPS 2200";
- your model is one that Schneider Electric treats as a special case, or your UPS ID is not in the reference;
- your installed version is newer than the one in the reference. No warning is shown then.

## Before you update

Firmware updates can interrupt battery backup or output power, and some models need a serial connection, so a working USB monitoring cable does not mean USB flashing is possible. Follow the manufacturer's guide and choose a quiet maintenance window.

## For administrators

The reference list is part of the program and has a review date. The date appears in the "Update listed" panel. The `GET /api/firmware` endpoint returns the statuses `outdated`, `current`, `unknown`, `not_upgradeable` and `unavailable`. To change the reference, see [Development in the architecture guide](ARCHITECTURE.md#development).

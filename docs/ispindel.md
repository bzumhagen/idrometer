# Pointing your iSpindel at idrometer

1. Put the iSpindel in configuration mode (it opens its own Wi-Fi network; see the iSpindel docs) and open its config page.
2. Choose **Service Type: HTTP** and fill in:

   | Field | Value |
   |---|---|
   | Server Address | the IP or hostname of your idrometer container |
   | Server Port | `8080` (or your `IDROMETER_LISTEN` port) |
   | Path / URL | `/api/v1/ingest/ispindel` |
   | Token | shown on the Settings page when you are signed in (under "iSpindel setup"), or the value of `IDROMETER_INGEST_TOKEN` in `/etc/idrometer/idrometer.env` |

3. Set the update interval (900 s is a good default) and save.

The device appears on the dashboard after its first report and is named after what it calls itself (rename it on the
Devices page).

## How readings become fermentations

- The **first reading** from a device starts a fermentation, named after the device and the date. Rename it and link a recipe whenever you like.
- A reading that arrives **more than 72 hours** after the previous one starts a **new** fermentation (change this under
  Settings, or per device on the Devices page). Wi-Fi blips and battery swaps are far shorter than that.
- **Completing** a fermentation closes it. The next reading always starts a new one, even if the iSpindel is still in the beer.
  If that leaves a stray session, merge it back or delete it from its page.
- A silent device's fermentation is closed automatically once the gap has passed, so the dashboard stays accurate.

## Gravity units

idrometer stores gravity as SG and temperature as °C. The iSpindel can report gravity in SG or Plato; **Auto-detect**
(the default) treats values under 2 as SG and anything else as Plato. Change it per device if you need to. Display
units (SG/°P, °C/°F) are a global setting.

The unit is applied **when a reading arrives**: idrometer converts it to SG then and stores that. Changing the setting
later affects new readings only; it does not fix readings already stored. (A calibration, by contrast, applies to past
readings too.)

## Not seeing data?

- `curl -i -X POST http://<host>:8080/api/v1/ingest/ispindel -d '{"ID":1,"token":"<token>","temperature":20}'` should return `200`.
  `401` means the token is wrong; `400` explains what is wrong with the payload.
- Check `journalctl -u idrometer -f` while the iSpindel reports.

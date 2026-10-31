# Calibration

By default idrometer shows the gravity your iSpindel calculated itself. You can instead let idrometer calculate gravity
from the tilt angle (Devices → *Calibration*). The advantage: recalibrating re-computes **every past reading**, so a
better calibration improves old batches too. The device's own value is always kept.

The formula is a polynomial in the tilt angle:

```
gravity = c0 + c1·angle + c2·angle² + c3·angle³
```

Leave unused coefficients empty. Say whether the result is in SG or degrees Plato.

## Converting the iSpindel's formula

The iSpindel's own calibration is usually a quadratic, `gravity = a·tilt² + b·tilt + c`, which maps to
**c0 = c, c1 = b, c2 = a, c3 = empty**.

Example: `0.00062 * tilt^2 + 0.0079 * tilt - 1.045` (Plato) becomes c0 = `-1.045`, c1 = `0.0079`, c2 = `0.00062`, unit = Plato.

## Tips

- Calibrate with sugar-water solutions of known gravity at your fermentation temperature, and fit the polynomial in the
  iSpindel's own calibration tool or any spreadsheet.
- Hand measurements (OG/FG from a hydrometer or refractometer) can be added on a fermentation's page. They are drawn on
  the graph, and a measurement marked **OG** is used for the ABV and attenuation figures.

## Derived figures

- **OG**: a measurement marked OG, else the override you typed, else the median of the first hour of readings.
- **Current gravity**: the most recent reading.
- **Apparent attenuation**: `(OG − current) / (OG − 1)`.
- **ABV (estimate)**: `(OG − current) × 131.25`.
- **Change per day**: least-squares slope over the last 24 hours (shown once there is at least an hour of data).

## Exports

The CSV and JSON exports use the same gravity as the graph: `gravity_sg` is the calibrated value when the device has a
calibration. `gravity_device_sg` is what the device itself reported.

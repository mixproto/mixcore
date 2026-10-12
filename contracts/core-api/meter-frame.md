# Meter frame v0 (`MeterFrame()` result)

`MeterFrame()` returns the newest meter frame as bytes. It never blocks, and it returns a fresh copy each call (latest
wins: frames between two calls are skipped, never queued). Call it on the display tick.

Layout, all little-endian:

| Offset | Size | Field |
|---|---|---|
| 0 | 4 | magic `MXMF` (ASCII) |
| 4 | 2 | version, `0` |
| 6 | 2 | count `n` of levels |
| 8 | 8 | `seq`, unsigned; increases with every new frame from the console; 0 means no frame yet |
| 16 | 8 | capture time, signed Unix nanoseconds (0 when there is no frame) |
| 24 | 4*n | levels, IEEE-754 float32 in **dBFS**; `-inf` means no signal; never NaN |

Channel map (x32): level `i` is input channel `i+1` for `i` = 0..31; since **v0.1** level 32 is **Main L** and level 33 is
**Main R**, so `n` = 34 while input meters are on and the console is live (v0 had `n` = 32; a v0 UI that reads the first
32 levels keeps working). `n` = 0 otherwise (meters off, stale, paused or stopped). Main L/R read `-inf` until the
console's first bus meter packet arrives. Input levels come from bank `/meters/1`, Main L/R from bank `/meters/2`
(`docs/protocols/x32.md` X32-F51..F55); both are the console's own meter levels converted from linear amplitude
(1.0 = 0 dBFS, up to +18 dBFS). The tap point of the input meters (pre or post fader) is not documented. A UI that sees
the same `seq` twice has nothing new to draw.

## Drawing meters the same way on every UI (v0.1)
The display scale and ballistics come from the core, not from each UI (design CR-17, open question 24):
- `MeterScale()` returns `meter-scale.schema.json`: a linear dBFS scale from -60 to 0 with ticks at 0, -6, -12, -18,
  -30 and -60, clip at 0 dBFS and above, nominal -18 dBFS, release 20 dB/s and peak hold 1500 ms.
- `MeterPosition(dbfs)` maps a level to 0..1 on that scale (`-inf`, NaN and anything at or below -60 give 0; 0 dBFS and
  above give 1).
- `MeterRelease(shownDB, inputDB, elapsedMs)` is the ballistics step for one meter on one display tick: a level above
  what is shown is shown at once; otherwise the shown level falls by 20 dB/s but never below the input; a fall below
  -60 dBFS gives `-inf`. Pure and allocation-free; call it per meter per tick, or reimplement the same formula from
  `MeterScale()`.
- Peak hold: the UI keeps the highest level for `peakHoldMs`, then lets it fall with the same release. Clip: a level at or
  above `clipDB` lights the clip block.

## Reading without allocation
`MeterFrame()` (gomobile) returns a new copy each call. The Go API `ReadMeterFrame(dst)` and the C call
`MixMeterFrame(buf, cap)` copy the same bytes into a caller-owned buffer instead, with no allocation per frame; a frame
is never larger than 536 bytes (`MeterFrameMaxBytes`, `MIX_METER_FRAME_MAX_BYTES`: 24 + 4 x 128). See `capi.md`.

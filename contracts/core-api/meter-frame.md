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

The channel map depends on the family of the session (`session.schema.json` `family`). In every family the levels
are the input channels in order, then **Main L** and **Main R** as the last two levels (`n-2` and `n-1`), so a UI draws
`n-2` input meters and a main pair without knowing the family. `n` = 0 while meters are off, stale, paused or stopped.
A UI that sees the same `seq` twice has nothing new to draw.

| Family | `n` | Levels | Source and conversion (inside the driver) |
|---|---|---|---|
| x32 | 34 | 0..31 inputs 1..32, 32 Main L, 33 Main R | inputs from bank `/meters/1`, Main L/R from bank `/meters/2` (`docs/protocols/x32.md` X32-F51..F55); linear amplitude, 1.0 = 0 dBFS, up to +18 dBFS. Main L/R read `-inf` until the first bank 2 packet arrives. Input tap point not documented. Since **v0.1** (v0 had `n` = 32; a v0 UI that reads the first 32 levels keeps working). |
| xair (v0.2) | 18 | 0..15 inputs 1..16, 16 Main L, 17 Main R | one bank, `/meters/1`: values 0..15 are the inputs (pre-fader), values 36 and 37 Main L/R (post-fader) (`docs/protocols/xair.md` XA-F51..F53); signed 16-bit in 1/256 dB, so dBFS = value / 256, and -32768 is `-inf` (XA-F52). Inputs and Main L/R arrive in the same packet. The 4 FX returns, the aux return, buses, FX sends and monitor values of the bank are not in the frame. |

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
`MeterFrame()` (gomobile) returns a new copy each call. `ReadMeterFrame(dst)` (Go and, since v0.1, gomobile) and the C
call `MixMeterFrame(buf, cap)` copy the same bytes into a caller-owned buffer instead, with no allocation per frame in the
core; a frame is never larger than 536 bytes (`MeterFrameMaxBytes`, `MIX_METER_FRAME_MAX_BYTES`: 24 + 4 x 128). See
`capi.md`. gomobile passes a `[]byte` argument by reference only for an Objective-C `NSMutableData` (Swift: pass
`buf as Data` with `buf` an `NSMutableData` kept across frames; any other `Data` is copied by the binding and the frame
is lost) and for a Java `byte[]` (the JNI layer copies the bytes back after the call); see `README.md`, Bindings.

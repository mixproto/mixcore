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
are the input channels in order, then (v0.5) the **bus meters** where the family has them, then **Main L** and **Main R**
as the last two levels (`n-2` and `n-1`). A UI draws `inputCount` input meters, where `inputCount` is the number of
input strips in the latest snapshot (levels `0` .. `inputCount-1`), Main is always at `n-2`/`n-1`, and the meter of a
bus is at the level its snapshot entry names in `buses[].meterIndex` (absent: no meter for that bus); it never needs the
family. A v0.4 UI that reads inputs from 0 and Main at `n-2`/`n-1` keeps working: v0.5 only inserts levels between them
(as v0.1 appended Main L/R to the v0 frame). `n` does not shrink on a small model:
levels of inputs the console did not answer (snapshot `absentInputs`) are always `-inf`. `n` = 0 while meters are off, stale, paused or stopped.
A UI that sees the same `seq` twice has nothing new to draw.

| Family | `n` | Levels | Source and conversion (inside the driver) |
|---|---|---|---|
| x32 | 50 | 0..31 inputs 1..32, 32..47 bus masters 1..16 (v0.5), 48 Main L, 49 Main R | inputs from bank `/meters/1`; bus masters and Main L/R from bank `/meters/2` values 0..15 and 22, 23 (`docs/protocols/x32.md` X32-F51..F55); linear amplitude, 1.0 = 0 dBFS, up to +18 dBFS. Bus and Main levels read `-inf` until the first bank 2 packet arrives. Input and bus tap points not documented. Since **v0.1** (v0 had `n` = 32; a v0 UI that reads the first 32 levels keeps working); v0.4 had `n` = 34 (Main L/R at 32, 33). |
| xair (v0.2) | 28 | 0..15 inputs 1..16 (`-inf` for inputs listed in `absentInputs`), 16..21 buses 1..6 and 22..25 FX sends 1..4 (= buses 7..10, v0.5), 26 Main L, 27 Main R | one bank, `/meters/1`: values 0..15 are the inputs (pre-fader), 26..31 buses 1..6 and 32..35 FX sends 1..4 (the vendor document says "all pre": before the bus master fader, so a bus meter moves while its master is down), 36 and 37 Main L/R (post-fader) (`docs/protocols/xair.md` XA-F51..F53); signed 16-bit in 1/256 dB, so dBFS = value / 256, and -32768 is `-inf` (XA-F52). Everything arrives in one packet. The 4 FX returns, the aux return and monitor values of the bank are not in the frame. v0.4 had `n` = 18 (Main L/R at 16, 17). |
| wing (additive) | 42 | 0..39 inputs 1..40, 40 Main L, 41 Main R (no bus meters yet: no buses entry carries meterIndex) | one meter report over UDP to the engine's own port (`docs/protocols/wing.md` WG-F50..F53), renewed every 1.5 s (5 s lifetime): per channel the louder of input L and R (pre-fader, for gain staging), Main L/R are main 1's output; signed 16-bit big-endian in 1/256 dB, -32768 is `-inf` (WG-F52). Packets of any other length or report id are dropped. Whether the console sends meters to the client's IP over a routed path is unverified (item 16). |

Bus meters (v0.5, design CR-32, redesign CC-13) are for the mix picker rows and chips and the Buses layer ("which wedge is
ringing"): a UI finds them only through `buses[].meterIndex`. A bus that did not answer at connect has no snapshot entry,
so its level is never read even though the frame keeps its slot; `n` never shrinks.

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

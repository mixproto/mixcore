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

Channel map (v0, x32): level `i` is input channel `i+1`, `n` = 32 while input meters are on and the console is live,
`n` = 0 otherwise (meters off, stale, paused or stopped). The value is the console's own meter level converted from
linear amplitude (1.0 = 0 dBFS, up to +18 dBFS); see `docs/protocols/x32.md` X32-F51..F54. A UI that sees the same `seq` twice
has nothing new to draw.

## Reading without allocation
`MeterFrame()` (gomobile) returns a new copy each call. The Go API `ReadMeterFrame(dst)` and the C call
`MixMeterFrame(buf, cap)` copy the same bytes into a caller-owned buffer instead, with no allocation per frame; a frame
is never larger than 536 bytes (`MeterFrameMaxBytes`, `MIX_METER_FRAME_MAX_BYTES`: 24 + 4 x 128). See `capi.md`.

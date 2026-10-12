# mixcore core API, contract v0.1

The in-process API that every app uses to drive a console (`api.Engine` in Go; the `mobile` gomobile wrapper and the
`capi` c-shared wrapper expose the same calls). Structured data crosses the boundary as JSON strings (schemas in this
folder) or as a binary meter frame (`meter-frame.md`). v0 is unstable until the first tagged release.

**v0.1 is backwards compatible with v0.** The `version` field of every JSON message stays `0`; v0.1 only adds optional
fields, events, command types and calls (marked "v0.1" in the schemas). A v0 app that ignores unknown JSON keys keeps
working unchanged. What v0.1 adds:

| Area | Addition | Design ref |
|---|---|---|
| Connection | `staleSinceMs` and `offline` in every state message; stale after 1.5 s without any packet from the console | SR-05, CR-06, 8.1 |
| Client limit | `limited` and `limitedReason`: the console does not send this engine updates (slots full); input locked, holds cancelled with reason `limited` | SR-17, CR-10 |
| Meters | Main L/R as levels 32/33 of the meter frame; `MeterScale`, `MeterPosition`, `MeterRelease` so every UI draws meters alike | CR-17, OQ 24, OQ 27 |
| Mute groups and DCAs | strip kinds `muteGroup` and `dca` (field `mute`), membership fields `muteGroups` and `dcas`, derived `effectiveMute` per strip; command type `toggle` | CR-01, CR-02, SR-04 |
| Fader steps | `FaderStep(db, deltaDB)`: the bottom-of-scale rule (`-inf` <-> -60 dB) for keyboard, screen reader and +/- buttons | CR-21, OQ 26 |

## Calls

| Call | Input | Output | Blocks? |
|---|---|---|---|
| `NewEngine()` | | engine | no |
| `Start(configJSON)` | `config.schema.json` | `session.schema.json` | **no**: returns at once; connecting happens in the background and is reported through the state listener |
| `Stop()` | | | bounded: closes sockets and joins goroutines (milliseconds), no network wait |
| `Pause()` | | | bounded like `Stop`. Ends subscriptions and meters (for app suspension). State stays, status becomes `stopped`. The sockets stay open until `Stop`, so a quick `Resume` is not a new client to the console |
| `Resume()` | | | **no**: reconnects in the background and **re-reads the full state**. Never writes |
| `SetStateListener(l)` | `StateListener` with `OnState(stateJSON string)` | | no. The next message is a full snapshot |
| `RequestSnapshot()` | | | **no**: the next listener message is a full snapshot |
| `Apply(commandJSON)` | `command.schema.json` | error code | **no**: validates and hands the write to the session goroutine; never waits on the network |
| `MeterFrame()` | | bytes, `meter-frame.md` | **never blocks**: copies the newest frame under a lock that is never held across I/O |
| `FaderPosition(db)` | dB (`-inf` allowed) | position 0..1 | **never blocks**: pure function of the family's fader law |
| `FaderDB(position)` | position 0..1 | dB (`-inf` at 0) | **never blocks**: pure |
| `SetDiscoveryListener(l)` | `DiscoveryListener` with `OnDiscovery(resultJSON string)` | | no |
| `Discover(requestJSON)` | `discover.schema.json` | error code; result later through the listener (`discovery.schema.json`) | **no**: probes on a goroutine; `ERR_BUSY` while one runs. Only reads, never subscribes or writes |
| `SetLogSink(sink, minLevel)` | `LogSink` with `Log(level int, line string)`; levels -4 debug, 0 info, 4 warn, 8 error | | no. Lines come from one dedicated goroutine (bounded queue, overflow counted and reported) |
| `SetSocketBinder(b)` | `SocketBinder` with `BindSocket(fd int) error` | | no. Android only: every socket opened afterwards is passed to it before use; an error keeps that socket closed. Apple and Windows never call it |
| `ReadMeterFrame(dst)` (Go) / `MixMeterFrame(buf, cap)` (C) | caller buffer | frame length | **never blocks, never allocates**; snprintf style when the buffer is short (`capi.md`) |
| `MeterScale()` (v0.1) | | `meter-scale.schema.json` | **never blocks**: constant |
| `MeterPosition(dbfs)` (v0.1) | dBFS (`-inf` allowed) | position 0..1 on the meter scale | **never blocks**: pure |
| `MeterRelease(shownDB, inputDB, elapsedMs)` (v0.1) | shown and new level in dBFS, ms since the last tick | level to show | **never blocks**: pure (`meter-frame.md`) |
| `FaderStep(db, deltaDB)` (v0.1) | level (`-inf` allowed), step in dB (+1, -1, +0.1, +5, ...) | next level | **never blocks**: pure. Up from `-inf` gives -60 dB; down from -60 dB or below gives `-inf`; otherwise the level rounded to 0.1 dB plus the step, rounded to 0.1 dB, at most the top of the family's fader law (x32: +10 dB). A step of 0 or NaN returns the level unchanged |

`FaderPosition`/`FaderDB` use 64-bit floats, which carry `-inf` natively (Swift `-Double.infinity`, Kotlin
`Double.NEGATIVE_INFINITY`, C# `double.NegativeInfinity`). NaN input gives position 0 / `-inf`.

## Threading
- Every call may come from any thread and returns quickly; none performs network I/O on the calling thread.
- `OnDiscovery` is called once per `Discover`, from that probe's goroutine; it may call `Discover` again.
- `Log` is called from the log goroutine only. Console and channel names are user content and appear only at debug level.
- `OnState` is called from one dedicated goroutine, one message at a time, in `seq` order. A slow listener never
  slows the console connection: changes are coalesced, and if too many pile up the next message is a full snapshot.
  The listener must not call `Stop` or `Pause` synchronously from inside `OnState`.
- A panic inside the engine is recovered and returned as `ERR_INTERNAL`; it never crosses the boundary.

## Safety rules the API enforces (apps do not need to re-implement them)
1. `Start` and `Resume` read the console; they never write.
2. `Apply` accepts only origin kinds `gesture` and `macro`. `console`/`system` give `ERR_NOT_USER_ORIGIN`.
3. While the status is not `live`, or the state says `limited` (v0.1), `Apply` returns `ERR_STALE` and `holdBegin` is
   refused. Nothing is queued, so nothing is replayed after a reconnect. The UI shows stale values dimmed and locks input.
4. Held faders (`holdBegin` .. `holdEnd`): the UI draws the finger position while held. If the desk, a scene recall
   or another client changes that fader (anything except this client's own echo), the core at once publishes the
   console's value, ends the gesture and reports an `externalChangeOnHeld` event with the new value (UI: "Changed at
   the mixer: -10.0 dB"). Further `set` commands for that fader from that client return `ERR_GESTURE_ENDED` until the
   client sends `holdBegin` (new gesture) or `holdEnd` (finger lifted). Going stale, pausing or stopping cancels every
   active hold (`holdCancelled` event): when the status returns to `live`, no write resumes without a new `holdBegin`.
5. Echo: a diff caused by the app's own write carries the write's origin (`kind`, `clientId`, `seq`), so the UI can tell
   its own echo from a change made at the desk. A write of another client (another `clientId` through the same engine,
   or a MixLink client) carries that client's origin and is an external change for everyone else's holds.
6. Background re-read: while live, the core re-reads the console state every 15 s (read-only, paced) to recover a
   change whose push was lost. Only values that differ from the last one seen reach the listener, as ordinary diffs with
   origin `console`. A pass is skipped while any fader is held, and parameters with a recent write of ours are not
   re-read. It never writes.

## Error codes
`Apply`, `Start`, `Stop`, `Pause`, `Resume` and `RequestSnapshot` return an error whose message is exactly one of
these codes (details go to the log, never to the user):

| Code | Meaning |
|---|---|
| `ERR_INVALID_CONFIG` | config JSON malformed, unknown family, bad host or port |
| `ERR_INVALID_COMMAND` | command JSON malformed, wrong version, unknown type/field, value of the wrong type |
| `ERR_INVALID_VALUE` | value outside what the console accepts (for x32: a name longer than 12 characters or not printable ASCII) |
| `ERR_NOT_USER_ORIGIN` | origin kind is not `gesture` or `macro` |
| `ERR_STALE` | not live: connecting, stale, limited (v0.1), paused or stopped |
| `ERR_GESTURE_ENDED` | the hold this write belongs to was ended by an external change or cancelled by a connection loss; send `holdBegin` again |
| `ERR_UNSUPPORTED` | the console has no such parameter, or hold on a non-fader field |
| `ERR_NOT_STARTED` | call needs `Start` first |
| `ERR_ALREADY_STARTED` | `Start` while started |
| `ERR_INTERNAL` | a bug; recovered panic |
| `ERR_ROLE` | reserved for MixLink roles (not emitted in v0) |
| `ERR_BUSY` | `Discover` while a probe is running |
| `ERR_NETWORK` | discovery result only: no socket could be opened or every probe send was refused (e.g. Local Network access denied) |
| `ERR_INVALID_ARGUMENT` | C binding only: a NULL pointer or a negative length from native code |

## Bindings

Both bindings expose the calls above and nothing else; behaviour is identical because both only translate types.

**gomobile (`mobile/`, `make xcframework`, AAR by the Android build).** One package, one engine per process. Names:

| Call | Swift (`import Mixcore`) | Kotlin / Java (`import mobile.Mobile`) |
|---|---|---|
| `Start` | `MobileStart(_ configJSON: String?, _ error: NSErrorPointer) -> String` | `Mobile.start(String): String` throws `Exception` |
| `Stop`, `Pause`, `Resume`, `RequestSnapshot` | `MobileStop(_ error: NSErrorPointer) -> Bool` (same shape) | `Mobile.stop()` throws `Exception` (same shape) |
| `Apply`, `Discover` | `MobileApply(_ json: String?, _ error: NSErrorPointer) -> Bool` | `Mobile.apply(String)` throws `Exception` |
| `MeterFrame` | `MobileMeterFrame() -> Data?` | `Mobile.meterFrame(): ByteArray` |
| `FaderPosition`, `FaderDB` | `MobileFaderPosition(_ db: Double) -> Double` | `Mobile.faderPosition(Double): Double` |
| `MeterScale` (v0.1) | `MobileMeterScale() -> String` | `Mobile.meterScale(): String` |
| `MeterPosition`, `FaderStep`, `MeterRelease` (v0.1) | `MobileMeterPosition(_ dbfs: Double) -> Double`, `MobileFaderStep(_ db: Double, _ deltaDB: Double) -> Double`, `MobileMeterRelease(_ shownDB: Double, _ inputDB: Double, _ elapsedMs: Double) -> Double` | `Mobile.meterPosition(Double): Double`, `Mobile.faderStep(Double, Double): Double`, `Mobile.meterRelease(Double, Double, Double): Double` |
| `SetStateListener` | `MobileSetStateListener(_ l: MobileStateListenerProtocol?)` | `Mobile.setStateListener(StateListener)` |
| `SetDiscoveryListener` | `MobileSetDiscoveryListener(_ l: MobileDiscoveryListenerProtocol?)` | `Mobile.setDiscoveryListener(DiscoveryListener)` |
| `SetLogSink` | `MobileSetLogSink(_ s: MobileLogSinkProtocol?, _ minLevel: Int)` | `Mobile.setLogSink(LogSink, Long)` |
| `SetSocketBinder` | not used on Apple | `Mobile.setSocketBinder(SocketBinder)`; `bindSocket(fd: Long)` throws to refuse |
| codes, levels | `MobileErrStale`, ..., `MobileLogInfo`, `MobileMeterFrameMaxBytes` | `Mobile.ErrStale`, ..., `Mobile.LogInfo`, `Mobile.MeterFrameMaxBytes` |

Swift imports these C-style functions with a trailing `NSErrorPointer`; they do **not** `throw`. On failure the
`NSError` has domain `go`, code 1 and `localizedDescription` equal to the stable code (`ERR_STALE`, ...). Java/Kotlin
gets an `Exception` whose `message` is the code. Callback objects are retained by Go until replaced or cleared with nil.

**c-shared (`capi/`, `make capi-check`)**: the C ABI, numeric status codes and memory rules are in `capi.md`.

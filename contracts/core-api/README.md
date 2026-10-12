# mixcore core API, contract v0.3

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
| Scene recall | event `sceneRecalledAtConsole` with `confidence` and `signals`; ends every held fader; background re-read starts | SR-15, CR-03, CR-08 |
| Meter read | `ReadMeterFrame(dst)` in the gomobile binding: the frame is written into a buffer the app keeps (no new `Data`/`ByteArray` per frame) | S2 finding |
| Fader steps | `FaderStep(db, deltaDB)`: the bottom-of-scale rule (`-inf` <-> -60 dB) for keyboard, screen reader and +/- buttons | CR-21, OQ 26 |

**v0.2 is backwards compatible with v0.1** in the same way (only additions). What v0.2 adds, for the X Air / M Air
family (`docs/protocols/xair.md`); no call, binding signature or error code changes:

| Area | Addition | Protocol ref |
|---|---|---|
| Family | `xair` in `config`, `discover`, `discovery` and `session`; default port 10024; `faderLaw` `xair` is the x32 law | XA-F01, XA-F40 |
| Discovery | the family of a reply is told apart by its source port and model string | XA-F12..F14, XA-F70 |
| Meters | per-family channel map; xair `n` = 18 (16 inputs, Main L/R), always "inputs, then Main L, Main R" | XA-F51..F53 |
| Groups | xair: 4 mute groups, 4 DCAs; the main strip has no mute-group membership, so its `muteGroups` field is absent | XA section 3.1, 3.2 |
| Strips | `absentInputs` in snapshots: input strips the mixer did not answer | XA-F72 |
| Scene recall | signal `snapshotIndex` and field `loadedScene` (1..64) of `sceneRecalledAtConsole` | XA-F60..F62 |
| Client limit | `limited` with the xair limit of 8 push clients | XA-F21 |

Per-family facts the apps need (no field carries them yet):
- **Send on/off is absent on xair**: the console has no per-send on/off (XA section 3.3). Sends are not in this contract
  yet; when they are added, an xair send has no `on` field and the UI shows no send on/off button.
- **Snapshots**: xair has 64 (`/-snap/index`, XA-F60, "recently loaded/saved"). The core reports that index only from
  the console's own push and only next to a burst of changed values (`loadedScene`; a lone push, e.g. after a save,
  raises nothing); it never loads, saves, names or deletes a snapshot, and no command here can make it do so, so slot 64
  (another app's data, XA-F63) is never touched.
**v0.3 is backwards compatible with v0.2** (additions only). It moves the phone safety rules of
`dybyskz-mix/docs/design/phone-safety.md` (SR-21 to SR-31, CR-22 to CR-27) into the core, so Apple, Android and Windows
behave the same and a UI bug cannot bypass them:

| Area | Addition | Design ref |
|---|---|---|
| Gesture window | every `set` inside a hold is a candidate the core clamps: slip guard (6 dB above the start, floor -16 dB, upward only), Gain caps (+3 / -6 dB, never open), monitor ceiling; `openWall` after a rest; new call `Gesture` answers the applied value, `state` and window | CR-22, SR-24, SR-26 |
| Gain | strip kind `headamp`, field `gain`, snapshot `headamps` and `headampRange` (xair; x32 waits for open question 44); every Gain write bounded to 6 dB from the console value except typed entry, `ERR_GAIN_BOUND` | CR-23, SR-31 |
| Typed raises | command flags `typed`, `confirmed`; a typed raise over 6 dB without `confirmed` is `ERR_NEEDS_CONFIRM` | CR-26, SR-30 |
| Show mode | `setShowMode`, `unlock`, `relock`; raising Gain is `ERR_SHOW_MODE` unless unlocked; `showMode`, `unlockedHeadamps`, event `gainUnlockEnded` in state | CR-24, SR-27 |
| Monitor ceilings | config `safety.monitorCeiling`: engineer's latest value + headroom, never below it | CR-25, SR-28 |
| Settings | config `safety.slipGuard` and command `setSlipGuard` per client (device class) | SR-26 |
| Fixtures | `fixtures/gesture-paths.json`: the AC-PS touch paths as data for the core and every UI | CR-27 |

- **Small models**: an XR12 or XR16 that does not answer for an input strip connects with fewer input strips (XA-F72);
  the snapshot lists the missing ones in `absentInputs`. The UI takes the number of strips from the snapshot, never from
  the model name. The first connect of an engine probes the optional strips once; reconnects reuse the answer.

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
| `Gesture(commandJSON)` (v0.3) | `command.schema.json` | `gesture-result.schema.json`, or an error code | **no**: the same command and rules as `Apply`; answers what the core applied (value, `free` / `atWall` / `atCap` / `atCeiling` / `locked`, window) so the UI draws it and plays the haptic |
| `MeterFrame()` | | bytes, `meter-frame.md` | **never blocks**: copies the newest frame under a lock that is never held across I/O |
| `FaderPosition(db)` | dB (`-inf` allowed) | position 0..1 | **never blocks**: pure function of the family's fader law |
| `FaderDB(position)` | position 0..1 | dB (`-inf` at 0) | **never blocks**: pure |
| `SetDiscoveryListener(l)` | `DiscoveryListener` with `OnDiscovery(resultJSON string)` | | no |
| `Discover(requestJSON)` | `discover.schema.json` | error code; result later through the listener (`discovery.schema.json`) | **no**: probes on a goroutine; `ERR_BUSY` while one runs. Only reads, never subscribes or writes |
| `SetLogSink(sink, minLevel)` | `LogSink` with `Log(level int, line string)`; levels -4 debug, 0 info, 4 warn, 8 error | | no. Lines come from one dedicated goroutine (bounded queue, overflow counted and reported) |
| `SetSocketBinder(b)` | `SocketBinder` with `BindSocket(fd int) error` | | no. Android only: every socket opened afterwards is passed to it before use; an error keeps that socket closed. Apple and Windows never call it |
| `ReadMeterFrame(dst)` (Go, gomobile v0.1) / `MixMeterFrame(buf, cap)` (C) | caller buffer | frame length | **never blocks, never allocates**; snprintf style when the buffer is short (`capi.md`) |
| `MeterScale()` (v0.1) | | `meter-scale.schema.json` | **never blocks**: constant |
| `MeterPosition(dbfs)` (v0.1) | dBFS (`-inf` allowed) | position 0..1 on the meter scale | **never blocks**: pure |
| `MeterRelease(shownDB, inputDB, elapsedMs)` (v0.1) | shown and new level in dBFS, ms since the last tick | level to show | **never blocks**: pure (`meter-frame.md`) |
| `FaderStep(db, deltaDB)` (v0.1) | level (`-inf` allowed), step in dB (+1, -1, +0.1, +5, ...) | next level | **never blocks**: pure. Up from `-inf` gives -60 dB; down from -60 dB or below gives `-inf`; otherwise the level rounded to 0.1 dB plus the step, rounded to 0.1 dB, at most the top of the family's fader law (x32 and xair: +10 dB). A step of 0 or NaN returns the level unchanged |

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
   A scene recall detected at the console (v0.1 event `sceneRecalledAtConsole`) ends every held fader as an external
   change, whether or not that fader's own update has arrived yet.
5. Echo: a diff caused by the app's own write carries the write's origin (`kind`, `clientId`, `seq`), so the UI can tell
   its own echo from a change made at the desk. A write of another client (another `clientId` through the same engine,
   or a MixLink client) carries that client's origin and is an external change for everyone else's holds.
6. Background re-read: while live, the core re-reads the console state every 15 s (read-only, paced) to recover a
   change whose push was lost. Only values that differ from the last one seen reach the listener, as ordinary diffs with
   origin `console`. v0.3: parameters that are held (`holdBegin` without `holdEnd`, also a resting finger) and parameters with a
   recent write of ours are left out of a pass; everything else is still re-read. It never writes.

7. **Gesture window (v0.3, CR-22).** `holdBegin` opens a window for that client and parameter; every `set` of that
   client on that parameter until `holdEnd` is a candidate the core clamps, so no value ever moves past the window in
   one write, whatever the UI computed:
   - **Slip guard** (faders, when `slipGuard` is on for the client, SR-26): the value stops at `max(start, -16) + 6` dB
     (from `-inf` the first stop is -10 dB); downward is never stopped. After the UI measured a 300 ms rest at the wall it
     sends `openWall`: accepted only while the last set stopped at the wall; the core re-anchors (the next upward move
     continues from the wall value, no jump) and sets the next wall 6 dB higher. Show mode never changes it.
   - **Gain caps** (headamp gain, every client, SR-24): `[start - 6, start + 3]` dB within the range; never opens.
   - **Monitor ceiling** (`safety.monitorCeiling`, SR-28): every level write of this engine stops at the engineer's
     latest value + headroom, and never below that value; it never opens and we never lower a value to meet it.
   Typed entry (`typed`), steps and nudges outside a hold are not windowed (the ceiling and the Gain bound still apply).
8. **Gain bound (CR-23, SR-31).** No Gain write moves more than 6 dB from the console's current value, whatever sent it
   (gesture, stepper, undo, macro, another client through the same core): `ERR_GAIN_BOUND`. Only `typed` (after
   `confirmed` for a raise over 6 dB) may move further; scene recall happens at the console and is never refused.
   A Gain value outside the console's range is `ERR_INVALID_VALUE`, never clamped.
9. **Typed raises (CR-26, SR-30).** A `typed` set that raises a fader or Gain by more than 6 dB from the console value
   returns `ERR_NEEDS_CONFIRM` unless `confirmed`; the UI shows `Raise ... from {from} to {to}?` and resends with
   `confirmed: true`. Lowering never asks.
10. **Show mode (CR-24, SR-27).** `setShowMode` writes nothing. While on, raising Gain is `ERR_SHOW_MODE` (lowering stays
    live, also inside a hold: the window stops at the start with state `locked`). `unlock` (after the UI's 1 s hold)
    allows raising Gain on that headamp until `relock`, 20 s without a command on it, stale, pause or stop
    (event `gainUnlockEnded`). An `undo` set that raises Gain on that headamp stays allowed for 10 s after the relock.
    Show mode survives reconnects; the UI restores it at `Start` from `safety.showMode`. Future gates (no such command
    exists in the core yet, each gets `ERR_SHOW_MODE` when added): 48 V phantom, polarity, EQ/GEQ `Flat`, `Mute all in
    this bank`, scene store, overwrite and delete, routing. Scene recall stays live.
11. **Write origins (open question 46).** The core tells apart: our own writes and their echoes (origin `gesture` /
    `macro` with the `clientId` of the client that sent them, matched in the driver's echo window) and everything the
    console reports (`console`: the surface, another app, MIDI, a scene recall, power-on). It cannot tell the surface
    from another app, nor another Mixhand device connected directly to the console from the surface. So the monitor
    ceiling's "engineer's latest value" is every value except the writes of a monitor client of this core; with
    several monitor devices on one bus that are not behind one hub, one device's writes count as the engineer's for
    the others. The hub must add: a `clientId` per paired device on every write, the role and `monitorCeiling` per
    client from the pairing token (`SetClientSafety` in Go), and all monitor devices of a bus behind it.

## Error codes
`Apply`, `Start`, `Stop`, `Pause`, `Resume` and `RequestSnapshot` return an error whose message is exactly one of
these codes (details go to the log, never to the user):

| Code | Meaning |
|---|---|
| `ERR_INVALID_CONFIG` | config JSON malformed, unknown family, bad host or port |
| `ERR_INVALID_COMMAND` | command JSON malformed, wrong version, unknown type/field, value of the wrong type |
| `ERR_INVALID_VALUE` | value outside what the console accepts (x32 and xair: a name longer than 12 characters or not printable ASCII) |
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
| `ERR_NEEDS_CONFIRM` | v0.3: a `typed` raise of more than 6 dB without `confirmed` (CR-26); nothing was written |
| `ERR_SHOW_MODE` | v0.3: show mode locks this write (raising Gain without an unlock, CR-24); nothing was written |
| `ERR_GAIN_BOUND` | v0.3: a Gain write more than 6 dB from the console value without `typed` (CR-23); nothing was written |

## Bindings

Both bindings expose the calls above and nothing else; behaviour is identical because both only translate types.

Both bindings are unchanged by v0.2: the family is a string inside the config JSON, the meter frame carries its own `n`. v0.3 adds one call, `Gesture`, and three error codes to both.

**gomobile (`mobile/`, `make xcframework`, AAR by the Android build).** One package, one engine per process. Names:

| Call | Swift (`import Mixcore`) | Kotlin / Java (`import mobile.Mobile`) |
|---|---|---|
| `Start` | `MobileStart(_ configJSON: String?, _ error: NSErrorPointer) -> String` | `Mobile.start(String): String` throws `Exception` |
| `Stop`, `Pause`, `Resume`, `RequestSnapshot` | `MobileStop(_ error: NSErrorPointer) -> Bool` (same shape) | `Mobile.stop()` throws `Exception` (same shape) |
| `Apply`, `Discover` | `MobileApply(_ json: String?, _ error: NSErrorPointer) -> Bool` | `Mobile.apply(String)` throws `Exception` |
| `Gesture` (v0.3) | `MobileGesture(_ json: String?, _ error: NSErrorPointer) -> String` | `Mobile.gesture(String): String` throws `Exception` |
| `MeterFrame` | `MobileMeterFrame() -> Data?` | `Mobile.meterFrame(): ByteArray` |
| `ReadMeterFrame` (v0.1) | `MobileReadMeterFrame(_ dst: Data?) -> Int`: pass `buf as Data` where `buf` is one `NSMutableData(length: MobileMeterFrameMaxBytes)` kept for the life of the view; the frame is written into `buf` (a plain Swift `Data`, or `Data(referencing:)`, is copied by the binding and receives nothing). Write the cast **inline in the call**, `MobileReadMeterFrame(buf as Data)`: `let d = buf as Data` followed by `MobileReadMeterFrame(d)` bridges to a copy and the frame never reaches `buf` (Apple review, S2c) | `Mobile.readMeterFrame(ByteArray): Long`, one `ByteArray(Mobile.MeterFrameMaxBytes.toInt())` reused; the JNI layer copies the bytes back |
| `FaderPosition`, `FaderDB` | `MobileFaderPosition(_ db: Double) -> Double` | `Mobile.faderPosition(Double): Double` |
| `MeterScale` (v0.1) | `MobileMeterScale() -> String` | `Mobile.meterScale(): String` |
| `MeterPosition`, `FaderStep`, `MeterRelease` (v0.1) | `MobileMeterPosition(_ dbfs: Double) -> Double`, `MobileFaderStep(_ db: Double, _ deltaDB: Double) -> Double`, `MobileMeterRelease(_ shownDB: Double, _ inputDB: Double, _ elapsedMs: Double) -> Double` | `Mobile.meterPosition(Double): Double`, `Mobile.faderStep(Double, Double): Double`, `Mobile.meterRelease(Double, Double, Double): Double` |
| `SetStateListener` | `MobileSetStateListener(_ l: MobileStateListenerProtocol?)` | `Mobile.setStateListener(StateListener)` |
| `SetDiscoveryListener` | `MobileSetDiscoveryListener(_ l: MobileDiscoveryListenerProtocol?)` | `Mobile.setDiscoveryListener(DiscoveryListener)` |
| `SetLogSink` | `MobileSetLogSink(_ s: MobileLogSinkProtocol?, _ minLevel: Int)` | `Mobile.setLogSink(LogSink, Long)` |
| `SetSocketBinder` | not used on Apple | `Mobile.setSocketBinder(SocketBinder)`; `bindSocket(fd: Long)` throws to refuse |
| codes, levels | `MobileErrStale`, `MobileErrNeedsConfirm`, `MobileErrShowMode`, `MobileErrGainBound` (v0.3), ..., `MobileLogInfo`, `MobileMeterFrameMaxBytes` | `Mobile.ErrStale`, ..., `Mobile.LogInfo`, `Mobile.MeterFrameMaxBytes` |

Swift imports these C-style functions with a trailing `NSErrorPointer`; they do **not** `throw`. On failure the
`NSError` has domain `go`, code 1 and `localizedDescription` equal to the stable code (`ERR_STALE`, ...). Java/Kotlin
gets an `Exception` whose `message` is the code. Callback objects are retained by Go until replaced or cleared with nil.

**c-shared (`capi/`, `make capi-check`)**: the C ABI, numeric status codes and memory rules are in `capi.md`.

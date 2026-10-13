# mixcore core API, contract v0.4

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
- **Send on/off is absent on xair**: the console has no per-send on/off (XA section 3.3). Since v0.4 an xair send has no
  `on` field (`sendFields.on` false) and the UI shows no send on/off button.
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
| Gain | strip kind `headamp`, fields `gain` and `phantom` (read-only), input field `headamp` (x32); snapshot `headamps` with each headamp's own law, `writable`, `bank` and `number` (x32 and xair); every Gain write bounded to 6 dB from the console value except typed entry, `ERR_GAIN_BOUND` | CR-23, SR-31 |
| Typed raises | command flags `typed`, `confirmed`; a typed raise over 6 dB without `confirmed` is `ERR_NEEDS_CONFIRM` | CR-26, SR-30 |
| Show mode | `setShowMode`, `unlock`, `relock`; raising Gain is `ERR_SHOW_MODE` unless unlocked; `showMode`, `unlockedHeadamps`, event `gainUnlockEnded` in state | CR-24, SR-27 |
| Monitor ceilings | config `safety.monitorCeiling`: faders stop at the engineer's latest value + headroom, never below it; sends not modelled yet | CR-25, SR-28 |
| Settings | config `safety.slipGuard` and command `setSlipGuard` per client (device class) | SR-26 |
| Fixtures | `fixtures/gesture-paths.json`: the AC-PS touch paths as data for the core and every UI | CR-27 |
| Demo mixer (additive) | `StartDemo`, `StopDemo` (`demo.schema.json`, `demo-mixer.schema.json`); `demo` in the session and every state message; section "Demo mixer" | design 6.3 |
| Re-entry | `Stop`, `Pause`, `StopDemo` from inside `OnState` return `ERR_BUSY` (Threading) | review M2 |

- **Small models**: an XR12 or XR16 that does not answer for an input strip connects with fewer input strips (XA-F72);
  the snapshot lists the missing ones in `absentInputs`. The UI takes the number of strips from the snapshot, never from
  the model name. The first connect of an engine probes the optional strips once; reconnects reuse the answer.
- **Headamps** (v0.3): Gain lives on the headamp (the physical input), not on the channel. Each snapshot entry carries
  its own law (`minDB`, `maxDB`, `stepDB`) and `writable`; the core never writes with a law it has not confirmed.

  | Family | Headamps | Index | Law | Writable | Phantom read |
  |---|---|---|---|---|---|
  | xair | mic `/headamp/01..16` (8 on XR16, 4 on XR12, probed) | NN | -12..+60 dB, 0.5 dB (k/144) | yes | yes |
  | xair | aux `/headamp/17`, 18-channel models only | 17 | -12..+20 dB, 0.5 dB (k/64) | no: law unconfirmed (XA-F112) | no |
  | xair | XR16/XR12 line inputs | - | absent: numbering unverified (XA-F110) | - | - |
  | x32 | local `/headamp/000..031` (probed) | NNN + 1 | -12..+60 dB, 0.5 dB (k/144) | yes | yes |
  | x32 | AES50 A `032..079`, B `080..127`, only those a channel's `/-ha` index points at | NNN + 1 | same | no: ownership unknown (X32-F76) | no |

  The wire maximum (`maxDB`, f = 1.0) is read but never written until hardware confirms it (xair.md item 22, x32.md
  item 17; XS10 avoids it): each entry's `writeMaxDB` is the top a write may reach (+59.5 mic, +19.5 line); a set above
  it is `ERR_INVALID_VALUE`, and a Gain gesture's cap stops there. Rule (lead decision, review pass 4): if hardware
  (x32.md item 17, xair.md item 22) shows that a family does not echo Gain writes, that family's Gain becomes read-only
  (`writable: false`): the in-flight bound would otherwise rest on re-reads alone. Until then, on a console that does
  not echo Gain, a hold that has 8 values in flight stops at `atCap` in both directions, lowerings included, until the
  next re-read ends them (intended: the core cannot know where the console is; lift and drag again after the re-read).

  The index is a model number, never UI text: the UI names a headamp by `bank` and `number` ("Local 1", "AES50 A 05",
  "Aux"). On x32 an input's `headamp` field says which headamp feeds it (`null`: none, the channel has no Gain); xair
  does not model it yet, so a UI there names headamps only. **Trim** (x32 `/ch/NN/preamp/trim`, -18..+18 dB) is not
  modelled and not bounded by the core yet (follow-up; it needs the same Gain rules).
- **Connect time** (lead decision, 2026-10-12): against the simulator on loopback, status `live` within 2 s at p95 for
  both families, headamp reads included (`api` test `TestConnectTime_*`, which fails above 3 s to leave CI a margin).
  The 5 s connect timeout stays the hard failure. Reconnect: all input back within 2 s after the link returns (SR-05),
  measured by `TestConnectTime_ReconnectLiveWithin2s` behind the `slow` build tag (`make sim-test` runs it).

**v0.4 is backwards compatible with v0.3** (additions only; the `version` field stays 0, no call or binding signature
changes). It adds buses, sends and roles for sends-on-fader and the musician monitor view (`dybyskz-mix`
`docs/design/mix-v1-mixing.md` 3 and 5, `acceptance/sends-on-fader.md`, `acceptance/monitor-view.md`,
`docs/product/roles.md`):

| Area | Addition | Ref |
|---|---|---|
| Buses | strip kind `bus` (fields `name`, `colour`, `fader`, `mute`, `linked`); snapshot `buses` with `kind` `mix` / `fx` | X32-F80, F81; xair 3.1, 3.2 |
| Sends | fields `sendLevel`, `sendOn`, `sendPan`, `sendTap` of an input strip, with param member `bus`; snapshot input `sends`, `sendFields`, `sendTaps` (the console's tap words) | X32-F82..F85; xair 3.3 |
| Matrices | strip kind `matrix` (x32 1..6), read-only; snapshot `matrices` | X32-F87 |
| DCA masters | `dca` strips gain `name`, `colour`, writable `fader`; snapshot `dcas` entries carry them | X32-F88; xair 3.2 |
| Colours | read-only field `colour` (16 tokens) on inputs, main, buses, matrices, DCAs | X32-F89; xair 3.2 |
| Safety | holds, gesture windows and the monitor ceiling on `sendLevel` and on bus and DCA faders, exactly as on channel faders; `toggle` on `sendOn` | CR-22, SR-28 |
| Roles | config `role` (`owner`, `engineer`, `monitor` with `buses` and a ceiling per bus, `viewer`); `ERR_ROLE` is emitted; hold-cancel reason `role` | roles.md, SR-09, AC-MV-03 |

Per-family facts for buses and sends:

| | x32 (`x32.md` X32-F80..F90) | xair (`xair.md` 3.1-3.3, "As implemented") |
|---|---|---|
| Buses | `mix` 1..16; pairs 1-2 .. 15-16 can be linked | `mix` 1..6 (pairs 1-2, 3-4, 5-6), `fx` 7..10 (FX send masters, no link) |
| Send fields | level (161 steps), on, pan and tap on **odd buses only, per pair** | level (161 steps) and tap per send, pan per send on buses 1..6 only (**none to FX sends 7..10**); **no send on/off** |
| Tap words | `IN/LC`, `<-EQ`, `EQ->`, `PRE`, `POST`, `GRP` | `IN`, `PREEQ`, `POSTEQ`, `PRE`, `POST`, `GRP` |
| Matrices | 1..6, read-only | none |
| Not modelled yet | sends from aux inputs and FX returns, bus-to-matrix sends, `panFollow`, bus pan, bus meters | sends from `/rtn/aux` and `/rtn/N`, `grpon`, bus meters |

Rules of v0.4:
- **Linked pairs.** The core writes exactly the parameter it is given; whether the console copies a write on one bus of a
  linked pair (or its send) to the other is unverified (x32.md item 25). A UI that shows a linked pair as one stereo mix
  writes the odd bus (and, for x32 pan, must: `sendFields.pan` `pair`). A monitor seat writes into a linked pair, or the
  x32 pair pan, only with both buses in its role (rule 13).
- **Which send fields exist** comes from each family's protocol document, encoded in the driver's family profile, never
  from what the console answers (a field the document lists but the console does not answer is absent as well). The
  snapshot says it per bus (`buses[].sendPan`) and per send (fields present or absent).
- **Tap is read-only** in v0.4 (a set is `ERR_UNSUPPORTED`): changing it changes what a musician hears at once and, on x32,
  for both buses of a pair. Colours, links and matrices are read-only too.
- **A bus that does not answer** at the engine's first connect is absent for the engine session with every send to it
  (xair XA-F72, test item 12); reconnects reuse the answer, like optional inputs.
- **Background re-read** (lead decision OQ 67): every pass re-reads faders, mutes, names, groups and Gain first, then a
  rotating slice of 256 v0.4 values (rule 6). A lost push of a fader, mute or Gain is recovered within about 50 s on
  x32 and 40 s on xair; a send, bus, colour or link value within about 6 minutes on x32 and 80 s on xair (x32.md and
  xair.md "Background re-read"). Pushes still carry every change at once.
- **Ceilings for sends** (lead decision OQ 68): an engineer's send (or fader) at `-inf` counts as -60 dB, so a seat's
  `+1` from `-inf` reaches -60 dB (AC-MV-16) and the ceiling is -60 dB + headroom (rule 11).
- **Bus meters** (AC-SF-12, the bus master meter) are not in the meter frame yet: the frame rule "Main L/R are the last two
  levels" would break. Follow-up with a meter-frame decision.

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
| `StartDemo(requestJSON)` (v0.3) | `demo.schema.json` | `demo-mixer.schema.json`, or an error code | **no**: binds one loopback UDP socket and starts the simulator on goroutines; `ERR_ALREADY_STARTED` while a demo runs, `ERR_NETWORK` if the loopback socket cannot be opened |
| `StopDemo()` (v0.3) | | | bounded: stops the session first when it is the demo's, then the simulator; frees the port and joins every demo goroutine. `ERR_NOT_STARTED` when no demo runs |

`FaderPosition`/`FaderDB` use 64-bit floats, which carry `-inf` natively (Swift `-Double.infinity`, Kotlin
`Double.NEGATIVE_INFINITY`, C# `double.NegativeInfinity`). NaN input gives position 0 / `-inf`.

## Threading
- Every call may come from any thread and returns quickly; none performs network I/O on the calling thread.
- `OnDiscovery` is called once per `Discover`, from that probe's goroutine; it may call `Discover` again.
- `Log` is called from the log goroutine only. Console and channel names are user content and appear only at debug level.
- `OnState` is called from one dedicated goroutine per `Start`, one message at a time, in `seq` order; even when `Stop`
  and `Start` race on two threads, two calls never overlap and a stopped session's last message never follows the next
  session's first (it is dropped). A slow listener never
  slows the console connection: changes are coalesced, and if too many pile up the next message is a full snapshot.
  `Stop`, `Pause` and `StopDemo` called from inside `OnState` (on the goroutine that runs it) return `ERR_BUSY` at
  once and change nothing: they would wait for that very goroutine. Call them from the app's own thread; there they
  wait as usual, even while `OnState` runs on the dispatcher. (v0.3)
  The guard sees only the goroutine that runs `OnState`. So `OnState` must never wait synchronously on another thread
  that may be calling `Stop`, `Pause` or `StopDemo` (Swift `DispatchQueue.main.sync` or `MainActor` work awaited from
  a blocking bridge, Kotlin `runBlocking(Dispatchers.Main)`, C# `Dispatcher.Invoke`): that call waits for `OnState`
  to return while `OnState` waits for it, a deadlock the core cannot detect. Hand the message off without waiting
  (`DispatchQueue.main.async`, `Handler.post` / a channel, `DispatcherQueue.TryEnqueue`) and return.
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
   change, whether or not that fader's own update has arrived yet. A report of the value the state already has (for
   example the core's re-read of an unchanged Gain) is not a change and never ends a hold; nor is the core's re-read of
   the last Gain value it sent (on a console that does not echo Gain it updates the state, but it is our own write).
5. Echo: a diff caused by the app's own write carries the write's origin (`kind`, `clientId`, `seq`), so the UI can tell
   its own echo from a change made at the desk. A write of another client (another `clientId` through the same engine,
   or a MixLink client) carries that client's origin and is an external change for everyone else's holds.
6. Background re-read: while live, the core re-reads the console state (read-only, paced at 16 reads per second); the
   next pass starts 15 s after the previous one ended, so passes never run back to back. It recovers a
   change whose push was lost. Only values that differ from the last one seen reach the listener, as ordinary diffs with
   origin `console`. v0.3: parameters that are held (`holdBegin` without `holdEnd`, also a resting finger) and parameters with a
   recent write of ours are left out of a pass; everything else is still re-read. It never writes. v0.4: each pass
   reads every v0.3 parameter, then the next 256 of the buses, sends, colours, links, matrices and DCA masters
   (rotating); a hold that begins during a pass is left out from the next batch of that pass on.

7. **Gesture window (v0.3, CR-22).** `holdBegin` opens a window for that client and parameter; every `set` of that
   client on that parameter until `holdEnd` is a candidate the core clamps, so no value ever moves past the window in
   one write, whatever the UI computed:
   - **Slip guard** (faders, when `slipGuard` is on for the client, SR-26): the value stops at `max(start, -16) + 6` dB
     (from `-inf` the first stop is -10 dB); downward is never stopped. After the UI measured a 300 ms rest at the wall it
     sends `openWall`: accepted only after the finger has been still at the wall for 300 ms (`wall.dwell`, checked in the
     core). Still means the candidates stay within 9 pt of the track (`wall.restSpeed` 30 pt/s x 300 ms, phone-safety.md
     4.1) when `holdBegin` carried `trackLengthPt` (50..10000 pt) and that is smaller than 2 dB of fader travel below the
     wall, else within those 2 dB; a larger move restarts
     the 300 ms, and earlier the result stays `atWall`. The core re-anchors (the next upward move
     continues from the wall value, no jump) and sets the next wall 6 dB higher. Show mode never changes it.
   - **Gain caps** (headamp gain, every client, SR-24): `[start - 6, start + 3]` dB within the range; never opens.
   - **Monitor ceiling** (`safety.monitorCeiling`, SR-28): every fader write of this engine (channel, bus, main; a
     musician's `My level` is the bus fader) and, since v0.4, every send-level write stops at the engineer's latest
     value + headroom, and never below that value; it never opens and we never lower a value to meet it. A `monitor`
     role (v0.4) carries a ceiling per bus (default 6 dB, `off` allowed) that applies to the sends into that bus and to
     its fader; the engineer's latest value is tracked per send and per bus fader.
   Typed entry (`typed`), steps and nudges outside a hold are not windowed (the ceiling and the Gain bound still apply).
8. **Gain bound (CR-23, SR-31).** No Gain write moves more than 6 dB from the console's current value, whatever sent it
   (gesture, stepper, undo, macro, another client through the same core): `ERR_GAIN_BOUND`. Only `typed` (after
   `confirmed` for a raise over 6 dB) may move further; scene recall happens at the console and is never refused.
   A Gain value outside the console's range is `ERR_INVALID_VALUE`, never clamped. The bound also holds against every
   Gain value in flight: each value sent that no console report has matched yet (at most 8 per headamp; a ninth is
   `ERR_GAIN_BOUND`, but inside a hold the candidate stays at the latest value sent with state `atCap` and no write,
   never an error mid-drag). Show mode's raise check uses the lowest of the console value and those values. There is no
   timer, because Gain echo is unverified (X32-F79, XA-F116): a report within half a step of an in-flight value ends
   that value; any other report only updates the console value; the core's own re-read of a Gain it wrote ends them
   all; stale, a reconnect, Stop, Pause or a failed send end them too. A set of the console value while other values
   are in flight is sent, never skipped; a set is skipped (`written: false`) only when it repeats the latest value
   sent, no report of that parameter arrived since, and less than an echo window (2 s) has passed: a drag held at a cap
   on a console that does not echo sends the cap once, while a return to an earlier value or the same value after a
   report is sent.
9. **Typed raises (CR-26, SR-30).** A `typed` set that raises a fader or Gain by more than 6 dB from the console value
   returns `ERR_NEEDS_CONFIRM` unless `confirmed`; the UI shows `Raise ... from {from} to {to}?` and resends with
   `confirmed: true`. Lowering never asks.
10. **Show mode (CR-24, SR-27).** `setShowMode` writes nothing. While on, raising Gain is `ERR_SHOW_MODE` (lowering stays
    live, also inside a hold: the window stops at the start with state `locked`). `unlock` (after the UI's 1 s hold)
    allows raising Gain on that headamp until `relock`, 20 s without a command on it, stale, pause or stop
    (event `gainUnlockEnded`). For 10 s after the relock an `undo` set may raise Gain on that headamp, but only back
    to a value Gain had before an entry of that unlock (within half a step of that headamp's law: the start of the hold, else the value in
    flight, else the console value); any other raise is `ERR_SHOW_MODE`.
    Show mode survives reconnects; the UI restores it at `Start` from `safety.showMode`. Future gates (no such command
    exists in the core yet, each gets `ERR_SHOW_MODE` when added): 48 V phantom, polarity, EQ/GEQ `Flat`, `Mute all in
    this bank`, scene store, overwrite and delete, routing. Scene recall stays live.
11. **Write origins (open question 46).** The core tells apart: our own writes and their echoes (origin `gesture` /
    `macro` with the `clientId` of the client that sent them, matched in the driver's echo window) and everything the
    console reports (`console`: the surface, another app, MIDI, a scene recall, power-on). It cannot tell the surface
    from another app, nor another Mixhand device connected directly to the console from the surface. So the monitor
    ceiling's "engineer's latest value" is every value except: the writes of a monitor client of this core and their
    echoes; our own writes read back by the re-read (`OwnWrite`); and, since v0.4, any console report within half a
    step of the grid of the last value a monitor client of this core sent for that parameter (a late or lost echo, a
    re-read), which is never credited to the engineer. It covers every fader (channel, main, bus, DCA) and send level,
    each taken from the console at connect; a parameter without such a value counts from -60 dB, never from its current
    value. With several monitor devices on one bus that are not behind one hub, one device's writes count as the
    engineer's for the others. The hub must add: a `clientId` per paired device on every write, the role and ceilings
    per client from the pairing token (`SetClientRole`; `SetClientSafety` for `safety.monitorCeiling`), and all monitor
    devices of a bus behind it.
12. **Scope: one engine.** Client ids, show mode, unlocks and their undo grace, holds and the monitor ceiling live in
    one engine (one `Start`). The core does not authenticate `clientId`: any caller of the engine may name any id.
    Until the hub binds each `clientId` to a paired session (hub requirement, ADR-0008), these are per-device rules,
    not per-person ones; two apps on two devices connected directly to one console each have their own show mode.
13. **Roles (v0.4, invariant 4, SR-09).** Every write, toggle, hold-begin, `openWall`, `unlock`, `relock` and
    `setShowMode` is checked against the role of its `clientId` in the core's session layer (`console.Supervisor`), so
    a UI bug cannot write outside a musician's mix: `ERR_ROLE`, nothing written. The role comes from config `role` for
    every client of the engine; the hub sets one per paired device (Go `Supervisor.SetClientRole`), and a role change
    applies at once: holds the new role does not allow end with `holdCancelled`, reason `role`. A `monitor` writes
    `sendLevel`, `sendOn`, `sendPan` into its buses and the `fader` and `mute` of those buses, nothing else; a `viewer`
    writes nothing; `holdEnd` and `setSlipGuard` (this device only) are always allowed. The writes of a monitor role
    (or of a client with `safety.monitorCeiling`) never move the engineer's latest value of the ceiling (rule 11).
    Reads are not filtered by the in-process API; the hub must filter what it sends to each device.
    Pairs and FX (review Sec M2, Sec L3): a write that acts on both buses of a pair needs both buses in the role, else
    `ERR_ROLE`: any write into a linked pair (the console may copy it to the partner, x32.md item 25) and the x32 send
    pan, which belongs to the pair whether linked or not (X32-F84). A link made at the console re-checks every hold at
    once (`holdCancelled`, reason `role`). A write into an FX send master (bus kind `fx`) needs `"fx": true` in that
    bus's grant. The role is checked and the write handed to the session under one lock, so a role change can never
    land between the check and the send.
    **Hub requirements** (the core cannot check them; ADR-0008): the hub derives `clientId` from the device token of the
    connection, never from the message; it gives every client a role with `SetClientRole` before routing its first
    message, viewer by default for a paired device (the config's default, owner, is only for the device user on a
    direct connection); on revoke it sets the client to viewer (ending its holds) before closing its sockets; it uses
    `SetShowModeBy` for a client's show-mode request. `SetShowMode` and `SetClientSafety` are unchecked, owner-only Go
    internals that the hub never exposes to a device.

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
| `ERR_ROLE` | v0.4: the role of the command's client does not allow it (config `role`, rule 13); nothing was written |
| `ERR_BUSY` | `Discover` while a probe is running; v0.3: `Stop`, `Pause` or `StopDemo` called from inside `OnState` (nothing changed; call again from the app's thread) |
| `ERR_NETWORK` | discovery result: no socket could be opened or every probe send was refused (e.g. Local Network access denied); `StartDemo`: the loopback socket could not be opened |
| `ERR_INVALID_ARGUMENT` | C binding only: a NULL pointer or a negative length from native code |
| `ERR_NEEDS_CONFIRM` | v0.3: a `typed` raise of more than 6 dB without `confirmed` (CR-26); nothing was written |
| `ERR_SHOW_MODE` | v0.3: show mode locks this write (raising Gain without an unlock, CR-24); nothing was written |
| `ERR_GAIN_BOUND` | v0.3: a Gain write more than 6 dB from the console value without `typed` (CR-23); nothing was written |

## Bindings

Both bindings expose the calls above and nothing else; behaviour is identical because both only translate types.

Both bindings are unchanged by v0.2: the family is a string inside the config JSON, the meter frame carries its own `n`. v0.3 adds `Gesture`, `StartDemo` and `StopDemo`, and three error codes to both. v0.4 changes no binding: buses, sends and roles travel in the JSON, and `ERR_ROLE` (`MobileErrRole`, `Mobile.ErrRole`, `MIX_ERR_ROLE`) already existed.

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

## Demo mixer (v0.3, additive; the `version` field stays 0)

The welcome screen's `Try the demo mixer` (design `mix-v1.md` 6.1, 6.3) runs the real mixing UI with no console. The core
starts the family's **console simulator** (`sim/x32`, `sim/xair`, the same test oracles the drivers are tested against)
inside the app process and the app connects to it with a normal `Start`. Every screen, gesture, echo, meter and safety
rule therefore behaves exactly as with a console; there is no fake UI path.

| Step | Call | Notes |
|---|---|---|
| 1 | `StartDemo({"version":0,"family":"xair"})` | returns `{"version":0,"family":"xair","host":"127.0.0.1","port":52113,"name":"Demo"}` |
| 2 | `Start({"version":0,"family":"xair","host":"127.0.0.1","port":52113,"meters":{"inputs":true}})` | as for any console; the session description and **every state message** carry `"demo": true` |
| 3 | `Stop()`, then `StopDemo()` | `StopDemo()` alone also stops a session that targets the demo; from inside `OnState` it returns `ERR_BUSY`, like `Stop` |

Rules the core keeps:
- **Loopback only.** The demo's socket is bound to `127.0.0.1`; the session to it uses sockets bound to `127.0.0.1`
  (without the Android `SocketBinder`, so the demo works with no network at all). Both refuse to send to, and drop
  packets from, any address that is not loopback.
- **Never a real console.** `Discover` never lists the demo, even when `127.0.0.1` and its port are probed as a manual
  host or the demo stops during the probe (the core filters every address a demo of this engine has used). `demo` is set by the core from the running demo's family, host and port, never by the app; a `Start` to the
  demo's address with another family is `ERR_INVALID_CONFIG`.
- **Nothing runs unless started.** No goroutine, socket or timer exists before `StartDemo` or after `StopDemo`.
- **Content.** Named channels of a small church or club band (Kick, Snare, Hi-Hat, Toms, Bass, Gtr L/R, Keys, Vox 1-3,
  Pastor mic, Ac Gtr, Playback L/R; x32 adds overheads, choir, host mic, click, talkback, spares), faders and mutes set,
  mute groups (Drums, Band, Vox, Speech; Speech engaged, so Pastor mic shows `muted by mute group 4`) and DCAs
  (Drums, Band, Vox, Playback). Meters move like a song at 120 BPM (kick and snare hits, hi-hat eighths, tom fills,
  sung phrases), input meters pre-fader and Main L/R from the faders, mutes and pans of the mix. Since v0.4 both
  families also seed channel colours, DCA names and faders, and six named monitor buses (`Drum IEM`, `Bass IEM`,
  `Gtr wedge`, `Keys IEM`, `Vox wedge`, `Pastor`) with pre-fader sends from every input, each musician's own channels
  louder; xair also seeds channel pans (x32 channel pan is not modelled).
- **Desk changes** (`deskChanges: true`): every 30 s one fader moves by itself (origin `console`), so `Changed at the
  mixer` and the held-fader rule (README safety rule 4) can be seen. Off by default.
- **UI copy** comes from the app (`demo.banner`: "Demo mixer: changes go nowhere", pill `Demo`); never show `name`.

Binding names: Swift `MobileStartDemo(_ json: String?, _ error: NSErrorPointer) -> String`,
`MobileStopDemo(_ error: NSErrorPointer) -> Bool`; Kotlin `Mobile.startDemo(String): String`, `Mobile.stopDemo()` (throw
`Exception` with the code); C `MixStartDemo` / `MixStopDemo` (`capi.md`).

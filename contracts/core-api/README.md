# mixcore core API, contract v0

The in-process API that every app uses to drive a console (`api.Engine` in Go; the `mobile` gomobile wrapper and the
`capi` c-shared wrapper expose the same calls). Structured data crosses the boundary as JSON strings (schemas in this
folder) or as a binary meter frame (`meter-frame.md`). v0 is unstable until the first tagged release.

## Calls

| Call | Input | Output | Blocks? |
|---|---|---|---|
| `NewEngine()` | | engine | no |
| `Start(configJSON)` | `config.schema.json` | `session.schema.json` | **no**: returns at once; connecting happens in the background and is reported through the state listener |
| `Stop()` | | | bounded: closes sockets and joins goroutines (milliseconds), no network wait |
| `Pause()` | | | bounded like `Stop`. Ends subscriptions and meters (for app suspension). State stays, status becomes `stopped` |
| `Resume()` | | | **no**: reconnects in the background and **re-reads the full state**. Never writes |
| `SetStateListener(l)` | `StateListener` with `OnState(stateJSON string)` | | no. The next message is a full snapshot |
| `RequestSnapshot()` | | | **no**: the next listener message is a full snapshot |
| `Apply(commandJSON)` | `command.schema.json` | error code | **no**: validates and hands the write to the session goroutine; never waits on the network |
| `MeterFrame()` | | bytes, `meter-frame.md` | **never blocks**: copies the newest frame under a lock that is never held across I/O |
| `FaderPosition(db)` | dB (`-inf` allowed) | position 0..1 | **never blocks**: pure function of the family's fader law |
| `FaderDB(position)` | position 0..1 | dB (`-inf` at 0) | **never blocks**: pure |

`FaderPosition`/`FaderDB` use 64-bit floats, which carry `-inf` natively (Swift `-Double.infinity`, Kotlin
`Double.NEGATIVE_INFINITY`, C# `double.NegativeInfinity`). NaN input gives position 0 / `-inf`.

## Threading
- Every call may come from any thread and returns quickly; none performs network I/O on the calling thread.
- `OnState` is called from one dedicated goroutine, one message at a time, in `seq` order. A slow listener never
  slows the console connection: changes are coalesced, and if too many pile up the next message is a full snapshot.
  The listener must not call `Stop` or `Pause` synchronously from inside `OnState`.
- A panic inside the engine is recovered and returned as `ERR_INTERNAL`; it never crosses the boundary.

## Safety rules the API enforces (apps do not need to re-implement them)
1. `Start` and `Resume` read the console; they never write.
2. `Apply` accepts only origin kinds `gesture` and `macro`. `console`/`system` give `ERR_NOT_USER_ORIGIN`.
3. While the status is not `live`, `Apply` returns `ERR_STALE`. Nothing is queued, so nothing is replayed after a
   reconnect. The UI shows stale values dimmed and locks input.
4. Held faders (`holdBegin` .. `holdEnd`): the UI draws the finger position while held. If the desk, a scene recall
   or another client changes that fader (anything except this client's own echo), the core at once publishes the
   console's value, ends the gesture and reports an `externalChangeOnHeld` event with the new value (UI: "Changed at
   the mixer: -10.0 dB"). Further `set` commands for that fader from that client return `ERR_GESTURE_ENDED` until the
   client sends `holdBegin` (new gesture) or `holdEnd` (finger lifted). Going stale, pausing or stopping cancels every
   active hold (`holdCancelled` event): when the status returns to `live`, no write resumes without a new `holdBegin`.
5. Echo: a diff caused by the app's own write carries the write's origin (`kind`, `clientId`, `seq`), so the UI can tell
   its own echo from a change made at the desk.

## Error codes
`Apply`, `Start`, `Stop`, `Pause`, `Resume` and `RequestSnapshot` return an error whose message is exactly one of
these codes (details go to the log, never to the user):

| Code | Meaning |
|---|---|
| `ERR_INVALID_CONFIG` | config JSON malformed, unknown family, bad host or port |
| `ERR_INVALID_COMMAND` | command JSON malformed, wrong version, unknown type/field, value of the wrong type |
| `ERR_INVALID_VALUE` | value outside what the console accepts (for x32: a name longer than 12 characters or not printable ASCII) |
| `ERR_NOT_USER_ORIGIN` | origin kind is not `gesture` or `macro` |
| `ERR_STALE` | not live: connecting, stale, paused or stopped |
| `ERR_GESTURE_ENDED` | the hold this write belongs to was ended by an external change or cancelled by a connection loss; send `holdBegin` again |
| `ERR_UNSUPPORTED` | the console has no such parameter, or hold on a non-fader field |
| `ERR_NOT_STARTED` | call needs `Start` first |
| `ERR_ALREADY_STARTED` | `Start` while started |
| `ERR_INTERNAL` | a bug; recovered panic |
| `ERR_ROLE` | reserved for MixLink roles (not emitted in v0) |

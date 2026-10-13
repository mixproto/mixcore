# mixcore core API v0.5: C ABI (`capi/`)

The c-shared build of the core API: `mixcore.dll` on Windows (P/Invoke from C#), `libmixcore.dylib` / `libmixcore.so`
for interop tests on macOS and Linux. `go build -buildmode=c-shared` writes the header next to the library
(`mixcore.h` / `libmixcore.h`); read names from that header, never from memory. Same calls and safety rules as
`README.md`; this file only adds the C-level rules. v0.2 (family `xair`) changes no C signature, status code or memory rule; v0.3 adds `MixGesture`, `MixStartDemo`, `MixStopDemo` and status codes 15 to 17. v0.4 and v0.5 change no C signature or status code: the v0.5 commands (`undo`, `cancelHold`, `watch`, `setFavourites`, `lockShowMode`, `unlockShowMode`) go through `MixApply` / `MixGesture`, and the meter frame grows within `MIX_METER_FRAME_MAX_BYTES` (x32 50 levels, xair 28).

## Memory and strings
- Strings in are UTF-8, NUL-terminated `char*`. They are copied on entry; the caller may free them as soon as the call returns.
- Results out are written to out-parameters, allocated with `malloc`, NUL-terminated, with the length (without NUL)
  in a separate `int32_t*`. The caller copies and then releases them with **`MixFree`**, never with its own allocator
  (C#: never declare a native string return as `string`).
- Callbacks receive `(const uint8_t* data, int32_t length, intptr_t context)`: UTF-8, **not** NUL-terminated, valid only
  during the call. Copy before returning.
- The Go runtime cannot be unloaded: load the library once per process and never unload it.

## Status codes
Every call except the fader law, `MixMeterFrame` and `MixFree` returns `int32_t`: `MIX_OK` (0) or a code below. The
numbers are part of the ABI and never change; new codes get new numbers.

| Number | Define | Code (README) |
|---|---|---|
| 0 | `MIX_OK` | |
| 1 | `MIX_ERR_INVALID_CONFIG` | `ERR_INVALID_CONFIG` |
| 2 | `MIX_ERR_INVALID_COMMAND` | `ERR_INVALID_COMMAND` |
| 3 | `MIX_ERR_INVALID_VALUE` | `ERR_INVALID_VALUE` |
| 4 | `MIX_ERR_NOT_USER_ORIGIN` | `ERR_NOT_USER_ORIGIN` |
| 5 | `MIX_ERR_STALE` | `ERR_STALE` |
| 6 | `MIX_ERR_GESTURE_ENDED` | `ERR_GESTURE_ENDED` |
| 7 | `MIX_ERR_UNSUPPORTED` | `ERR_UNSUPPORTED` |
| 8 | `MIX_ERR_NOT_STARTED` | `ERR_NOT_STARTED` |
| 9 | `MIX_ERR_ALREADY_STARTED` | `ERR_ALREADY_STARTED` |
| 10 | `MIX_ERR_INTERNAL` | `ERR_INTERNAL` (also: a recovered Go panic) |
| 11 | `MIX_ERR_ROLE` | `ERR_ROLE` (v0.4: the client's role does not allow the command) |
| 12 | `MIX_ERR_BUSY` | `ERR_BUSY` (also `MixStop`, `MixPause`, `MixStopDemo` from inside the state callback, v0.3) |
| 13 | `MIX_ERR_NETWORK` | `ERR_NETWORK` (only inside discovery results) |
| 14 | `MIX_ERR_INVALID_ARGUMENT` | NULL pointer or negative length |
| 15 | `MIX_ERR_NEEDS_CONFIRM` | `ERR_NEEDS_CONFIRM` (v0.3) |
| 16 | `MIX_ERR_SHOW_MODE` | `ERR_SHOW_MODE` (v0.3) |
| 17 | `MIX_ERR_GAIN_BOUND` | `ERR_GAIN_BOUND` (v0.3) |

## Functions

| C signature (as in the generated header) | Notes |
|---|---|
| `int32_t MixStart(char* configJSON, char** outJSON, int32_t* outLen)` | `outJSON` = session description; `MixFree` it |
| `int32_t MixStop(void)`, `MixPause(void)`, `MixResume(void)`, `MixRequestSnapshot(void)` | |
| `int32_t MixApply(char* commandJSON)` | one gesture; returns at once |
| `int32_t MixGesture(char* commandJSON, char** outJSON, int32_t* outLen)` (v0.3) | as `MixApply`; `outJSON` = `gesture-result.schema.json` on `MIX_OK`; `MixFree` it |
| `int32_t MixMeterFrame(uint8_t* buf, int32_t capacity)` | see below |
| `double MixFaderPosition(double db)`, `double MixFaderDB(double position)` | pure; `-inf` crosses as IEEE `-INFINITY` |
| `int32_t MixMeterScale(char** outJSON, int32_t* outLen)` (v0.1) | `meter-scale.schema.json`; `MixFree` it |
| `double MixMeterPosition(double dbfs)`, `double MixMeterRelease(double shownDB, double inputDB, double elapsedMs)`, `double MixFaderStep(double db, double deltaDB)` (v0.1) | pure, never allocate (`README.md`, `meter-frame.md`) |
| `int32_t MixSetStateListener(MixBytesCallback cb, intptr_t context)` | NULL clears; next message is a snapshot |
| `int32_t MixSetDiscoveryListener(MixBytesCallback cb, intptr_t context)` | NULL clears |
| `int32_t MixDiscover(char* requestJSON)` | result later through the discovery callback |
| `int32_t MixSetLogSink(MixLogCallback cb, intptr_t context, int32_t minLevel)` | `MIX_LOG_DEBUG` -4, `INFO` 0, `WARN` 4, `ERROR` 8 |
| `void MixFree(void* p)` | frees what this library returned; NULL is ignored |
| `int32_t MixStartDemo(char* requestJSON, char** outJSON, int32_t* outLen)` (v0.3) | `demo.schema.json` in, `demo-mixer.schema.json` out on `MIX_OK`; `MixFree` it (`README.md`, Demo mixer) |
| `int32_t MixStopDemo(void)` (v0.3) | stops a session to the demo, then the demo mixer |
| `int32_t MixTestPanic(void)` | **only** in a `-tags capitest` build: panics inside the export, returns `MIX_ERR_INTERNAL`. That build also exports two internal test callbacks (`mixTestOnBytes`, `mixTestOnLog`); never ship it |

```c
typedef void (*MixBytesCallback)(const uint8_t* data, int32_t length, intptr_t context);
typedef void (*MixLogCallback)(int32_t level, const uint8_t* line, int32_t length, intptr_t context);
```

### `MixMeterFrame`: meters without per-frame allocation
Copies the newest frame (`meter-frame.md`) into the caller's buffer and returns its length. Nothing is allocated.
If `capacity` is smaller than the frame, nothing is written and the needed length is returned (snprintf style);
`MIX_METER_FRAME_MAX_BYTES` (536) always fits, so a caller allocates one buffer of that size once and pins it.
`MixMeterFrame(NULL, 0)` only asks for the length. A negative result is `-MIX_ERR_*`
(`-MIX_ERR_INVALID_ARGUMENT` for a NULL buffer with a capacity, or a negative capacity).

## Threading
- Every call may come from any thread and returns quickly; none does network I/O on the calling thread.
- Each callback kind is called from one Go-owned thread at a time (state: the dispatcher; discovery: the probe;
  log: the log goroutine). They must not block. `MixStop`, `MixPause` and `MixStopDemo` called from inside the state callback return `MIX_ERR_BUSY` at once and change nothing (v0.3); call them from the app's thread. The callback must never wait synchronously on a thread that may call them (for example C# `Dispatcher.Invoke`): that is a deadlock the core cannot see; post the message and return.
- A panic anywhere in Go is recovered; it never crosses into the host process.

## Build
```bash
make capi-check   # host library + header into build/capi/, capitest build + C smoke test against mixsim
# Windows (runner): GOOS=windows CGO_ENABLED=1 CC=<llvm-mingw clang> go build -buildmode=c-shared -trimpath -ldflags="-s -w" -o mixcore.dll ./capi
```
On macOS the dylib's install name is `@rpath/libmixcore.dylib`; on Linux the soname is `libmixcore.so`.

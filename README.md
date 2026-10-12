# mixcore

An open-source Go engine for remote control of digital mixing consoles over the local network.

mixcore speaks each console family's control protocol, keeps one engineering-unit model of the console
(dB, Hz, ms), and gives apps a small, safe API: read state, apply a user gesture, pull meters.
It is the core of [Mixhand](https://mixhand.dybyskz.com), and you can use it in your own projects.

**Status:** early development. Nothing is stable yet.

## Principles
- The console is the source of truth. On connect and reconnect mixcore reads; it never pushes state on its own.
- No write without a user gesture. Timers, retries and resume never write to a console.
- Losing the network never changes the mix: last values stay (marked stale), queued writes are dropped, never replayed.
- Every protocol fact is documented with its source in `docs/protocols/`. No GPL-derived or decompiled material.

## Layout
| Path | What |
|---|---|
| `model/` | console model in engineering units (dB, on/off, names), immutable snapshots |
| `driver/` | the contract every console family implements (sessions, origins, errors) |
| `driver/x32/` | X32 / M32 family over OSC/UDP: discovery, connect and full read, subscriptions, fader law, meters |
| `driver/xair/` | X Air / M Air family (XR12, XR16, XR18, X18, MR12, MR18, V2 units) over OSC/UDP, from `docs/protocols/xair.md` |
| `driver/internal/xosc/` | the session engine both OSC families share: two sockets, echo, liveness, client limit, re-read, scene heuristic |
| `console/` | supervisor: connect, live, stale, reconnect with backoff; write gate and held-fader rule |
| `api/` | in-process facade for apps: JSON state listener, `Apply`, `MeterFrame`, fader law, `Discover`, log sink |
| `mobile/` | the gomobile binding of `api/` (iOS, iPadOS, macOS xcframework; Android AAR) |
| `capi/` | the c-shared binding of `api/` (Windows DLL via P/Invoke; dylib/.so for interop tests), ABI in `contracts/core-api/capi.md` |
| `sim/x32/`, `sim/xair/` | console simulators used as test oracles, written from `docs/protocols/x32.md` and `xair.md` only; shared parts in `sim/internal/oscsim/` |
| `cmd/mixsim/` | serves the simulator on real UDP ports |
| `cmd/mixprobe/` | guided hardware session against a real console on a lab network: checks the protocol facts, restores what it changed, writes a report |
| `contracts/core-api/` | JSON Schema and binary meter frame spec of the API (v0, unstable) |
| `docs/protocols/` | every protocol fact we implement, with its source |

## Development
```bash
make tools          # pinned golangci-lint, govulncheck, benchstat, gomobile, gobind into ./bin
make lint test      # unit tests run on an in-memory network with fake time
make sim-test       # driver and API against the simulator on loopback UDP
make fuzz           # every parser fuzz target, 30 s each
make mobile-check   # gobind type check of mobile/ (no Xcode or NDK needed)
make capi-check     # host c-shared library into build/capi + C smoke test against mixsim
make xcframework DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer   # build/Mixcore.xcframework
go run ./cmd/mixsim -demo   # a simulated X32 on 127.0.0.1:10023
go run ./cmd/mixsim -family xair -demo   # a simulated XR18 on 127.0.0.1:10024
go run ./cmd/mixprobe --lab -host 192.168.1.62   # hardware session (amplifiers off; see the package doc)
go run ./cmd/mixprobe --lab -family xair -host 192.168.1.1   # the X Air checklist of docs/protocols/xair.md section 13
```

## Compatibility
Behringer X32 / Midas M32 and Behringer X Air / Midas M Air (OSC over UDP), tested against the simulators; no hardware
captures yet (see the "to verify" lists in `docs/protocols/`).
Vendor names are used only to state compatibility. This project is not affiliated with or endorsed by any console manufacturer.

## License
[Mozilla Public License 2.0](LICENSE). You can use mixcore in closed-source apps; changes to mixcore's own files must be
shared under the same license. Contributions require a DCO sign-off, see [CONTRIBUTING.md](CONTRIBUTING.md).

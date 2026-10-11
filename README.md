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
| `console/` | supervisor: connect, live, stale, reconnect with backoff; write gate and held-fader rule |
| `api/` | in-process facade for apps: JSON state listener, `Apply`, `MeterFrame`, fader law |
| `sim/x32/` | console simulator used as the test oracle, written from `docs/protocols/x32.md` only |
| `cmd/mixsim/` | serves the simulator on real UDP ports |
| `contracts/core-api/` | JSON Schema and binary meter frame spec of the API (v0, unstable) |
| `docs/protocols/` | every protocol fact we implement, with its source |

## Development
```bash
make tools          # pinned golangci-lint, govulncheck, benchstat into ./bin
make lint test      # unit tests run on an in-memory network with fake time
make sim-test       # driver and API against the simulator on loopback UDP
make fuzz           # every parser fuzz target, 30 s each
go run ./cmd/mixsim -demo   # a simulated X32 on 127.0.0.1:10023
```

## Compatibility
Planned first: Behringer X32 / Midas M32 and Behringer X Air / Midas MR (OSC over UDP).
Vendor names are used only to state compatibility. This project is not affiliated with or endorsed by any console manufacturer.

## License
[Mozilla Public License 2.0](LICENSE). You can use mixcore in closed-source apps; changes to mixcore's own files must be
shared under the same license. Contributions require a DCO sign-off, see [CONTRIBUTING.md](CONTRIBUTING.md).

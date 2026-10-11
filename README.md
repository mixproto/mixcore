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

## Compatibility
Planned first: Behringer X32 / Midas M32 and Behringer X Air / Midas MR (OSC over UDP).
Vendor names are used only to state compatibility. This project is not affiliated with or endorsed by any console manufacturer.

## License
[Mozilla Public License 2.0](LICENSE). You can use mixcore in closed-source apps; changes to mixcore's own files must be
shared under the same license. Contributions require a DCO sign-off, see [CONTRIBUTING.md](CONTRIBUTING.md).

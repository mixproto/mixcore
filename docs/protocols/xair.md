# X Air / M Air network control protocol (as mixcore will implement it)

Scope: Behringer X Air family (XR12, XR16, XR18, X18) and Midas M Air family (MR12, MR18) digital mixers. Compatibility
statement only; mixcore is not affiliated with or endorsed by any console maker. This file lists the protocol facts that
`driver/xair` and `sim/xair` will implement. As for X32, the driver and the simulator are both written from this file,
never from each other, and when a real mixer disagrees with this file the mixer wins: record a `[CAPTURE]` fact here
first, then change the simulator, then the driver.

Baseline: `docs/protocols/x32.md` (fact IDs `X32-Fnn`). Both families speak the same OSC dialect over UDP, so this file
does not repeat what is identical; every section says what is **the same** as X32 and what **differs**, with sources.
Facts of this file have IDs `XA-Fnn`.

Research date: 2026-10-12. No X Air or M Air hardware was available: there are **no captures**, and every behaviour
claim below is from documents or code. Section "To verify with mixprobe or hardware" lists the exact tests.

## Tags
Tags used here, with the equivalent tag of `x32.md` and of the research protocol in brackets:
- `[VENDOR-DOC]` (`[OFFICIAL]`): a document published by the manufacturer (protocol note, quick start guide, release
  note quoted verbatim, store listing text), or a formal specification (OSC 1.0).
- `[PERMISSIVE-CODE <licence>]` (`[CODE]`): behaviour read in MIT or Apache-2.0 source code. The licence is named at
  each use. Code is evidence of what working clients do, not of what the firmware guarantees.
- `[COMMUNITY]` (`[COMMUNITY]`): forum, wiki, third-party app documentation, the unofficial Maillot PDF.
- `[TO-VERIFY]` (`[UNVERIFIED]`): plausible but not confirmed by two independent sources, or sources contradict.
  Every `[TO-VERIFY]` item is in the checklist at the end and becomes a simulator option.
- `[CAPTURE]`: our own packet capture of hardware we use. **None yet.**

Legal note (invariant 8): nothing here is copied from GPL code or decompiled software. GPL and unlicensed X Air projects
were found (listed under "Pointers not used") and were not read for facts. Tables below are written from the vendor
document and from MIT/Apache code, in our own words.

## Sources (accessed 2026-10-12)
- **XS1** Music Tribe, "X AIR Mixer Series Remote Control Protocol (firmware 1.11 or higher)", 4 pages, footer
  "X AIR Mixer OSC Document (fw 1.11)". Vendor URL:
  https://cdn.mediavalet.com/aunsw/musictribe/hmivS7F05kKSJlWDYPGLpA/agTKkSIWS0y1bJl97YZEjg/Original/X%20AIR%20Remote%20Control%20Protocol.pdf
  (HTTP 500 on access). Read in full via the ManualShelf mirror:
  https://www.manualshelf.com/manual/behringer/x18/air-series-osc-documentation-english.html and `/page-2.html`,
  `/page-3.html`, `/page-4.html` of the same path. Undated. `[VENDOR-DOC]`. The "Parameters.txt" file it refers to
  was not found.
- **XS2** Behringer, "Quick Start Guide X AIR X18/XR18" (MUSIC Group era, English section and specification table).
  Vendor URL https://cdn.mediavalet.com/aunsw/musictribe/11wtbydV-0aH8UtYnUGsCw/62P1eKWIl0yezqUNT06Ivw/Original/QSG_BE_0605-AAA_X18_WW.pdf
  (HTTP 500 on access); read from the mirror http://warehousesound.com/r/behringerX18-XR18manual.pdf. `[VENDOR-DOC]`.
- **XS3** Behringer, "Quick Start Guide X AIR XR16/XR12", V 5.0. Vendor URL
  https://cdn.mediavalet.com/aunsw/musictribe/HXp-EakOUkGWPdgZ96aqFQ/jY6paPtgA0KYzOaYfNpY4w/Original/QSG_BE_0605-AAB_XR16-XR12_WW.pdf
  (HTTP 500 on access); read from the mirror
  https://shop.scavino.it/files/scavino_Files/DocFiles/000/016/536/Manuale-Behringer-XR12.pdf. `[VENDOR-DOC]`.
- **XS4** P.-G. Maillot, "Unofficial X32/M32 OSC Remote Protocol" v4.06-09 (= `x32.md` S1). Printed p.11 (X Air
  `/info` replies) and p.12 footnote 7 (node form). Note: the copy lives on branch `master`:
  https://github.com/bitfocus/companion-module-behringer-x32/blob/master/docs/X32-OSC.pdf. `[COMMUNITY]`.
- **XS5** bitfocus/companion-module-behringer-xair, MIT, commit `b172eb0` (2026-09-07):
  https://github.com/bitfocus/companion-module-behringer-xair (`index.js`, `constants.js`, `defStrip.js`,
  `buildStripDefs.js`, `buildMeterDefs.js`, `actions.js`). `[PERMISSIVE-CODE MIT]`.
- **XS6** onyx-and-iris/xair-api-python, MIT, commit `16f3c5f` (2026-04-21):
  https://github.com/onyx-and-iris/xair-api-python (`xair_api/kinds.py`, `shared.py`). The README says the author only
  has an MR18 to test with. `[PERMISSIVE-CODE MIT]`.
- **XS7** stblassitude/xair-api-go, MIT, commit `b112e15` (2026-09-09): https://github.com/stblassitude/xair-api-go
  (`WIRE_PROTOCOL.md`, derived from XS6 plus public references). `[PERMISSIVE-CODE MIT]`.
- **XS8** notameadow/xair-osc, MIT, commit `5237a05` (2026-05-04): https://github.com/notameadow/xair-osc
  (`README.md`, `xair_osc/meters.py`, `protocol.py`). Early alpha; meter notes say "verified live against XR18
  firmware 1.17.1". `[PERMISSIVE-CODE MIT]`.
- **XS9** peterdikant/xair-remote, MIT, commit `b49c9bc` (2025-05-26): https://github.com/peterdikant/xair-remote
  (`lib/xair.py`). `[PERMISSIVE-CODE MIT]`.
- **XS10** matiasbarrios/magical-mixing-console, Apache-2.0, commit `8661d1c` (2026-06-25):
  https://github.com/matiasbarrios/magical-mixing-console (`docs/reference/CONNECTIVITY.md`,
  `src/mixers/drivers/xair/model.js`, `search.js`, `shared.js`, `device/index.js`, `device/bus/*.js`).
  `[PERMISSIVE-CODE Apache-2.0]`.
- **XS11** wrodie/behringer-mixer, MIT, commit `32352e7` (2026-05-21): https://github.com/wrodie/behringer-mixer
  (`behringer_mixer/mixers/mixer_type_xr12.py`, `_xr16.py`, `_xr18.py`, `_xair.py`). `[PERMISSIVE-CODE MIT]`.
- **XS12** Behringer World wiki, "X-Air / M-Air OSC Commands", last modified 2024-02-29, derived from the "Sysex OSC
  Generator" X-Air list; read via https://web.archive.org/web/20240329161700/https://behringer.world/wiki/doku.php?id=x-air_osc.
  `[COMMUNITY]`.
- **XS13** behringer.world thread t=1206 "New Firmware 1.20 and apparent vew V2 models", first post 2023-06-30 quoting
  the vendor release note; read via web.archive.org. `[COMMUNITY]` (quote of a vendor note, not the note itself).
- **XS14** behringer.world thread t=1858 "X air V1 hardware", posts 2024-10-20 to 2024-10-22; read via
  web.archive.org. `[COMMUNITY]`.
- **XS15** X AIR firmware V1.18 release note: only search-engine snippets of third-party copies were readable
  (https://www.scribd.com/document/563453480/ReleaseNote-Behringer-X-AIR-Firmware-V1-18, a Studocu copy, and a Behringer
  Facebook post). `[COMMUNITY]` `[TO-VERIFY]`.
- **XS16** Mixing Station documentation, X Air: https://mixingstation.app/ms-docs/mixers/behringer/xair/. `[COMMUNITY]`.
- **XS17** Wi-Fi field reports, search-engine snippets only: https://behringer.world/viewtopic.php?t=1737&start=10,
  https://behringer.world/viewtopic.php?t=1580,
  https://www.thegearpage.net/board/index.php?threads/behringer-xr18-dropouts-even-with-5ghz-ext-router.2169584/.
  `[COMMUNITY]` `[TO-VERIFY]`.
- **XS18** Apple iTunes Search/Lookup API, 2026-10-12: MX-Q (id 1471505954, seller Empower Tribe Innovations DE GmbH,
  v2.2 of 2026-03-18) listing text; a lookup of the X AIR iPad app id 896725230 returned 0 results in the US, DE and GB
  stores. `[VENDOR-DOC]` for the listing text; the lookup result is an observation.
- **XS19** OISecWiki "Behringer XR18", last edited 2026-04-15: https://wiki.oisec.net/wiki/Behringer_XR18. `[COMMUNITY]`.
- **S3 of x32.md** OSC 1.0 specification. `[VENDOR-DOC]` (formal specification).

Pointers not used (licence does not allow reuse, or not readable): pmaillot/X32-Behringer `XAir_Command.c` (no
licence file), Rippert/XAir-Behringer and Rippert/XAir-MIDI-OSC-BASH-utilities (GPL-3.0),
Metalab/behringer_xr18_hass (GPL-3.0), redtide/wiki-x-air (no licence), Maillot's X Air meter table on
behringer.world t=1545 (HTTP 403, no archive copy).

## 1. Models, firmware, scope

### Models
| ID | Fact | Source |
|---|---|---|
| XA-F70 | All six models run one protocol on UDP 10024. The model string is the third argument of `/xinfo` (XA-F10). Strings seen in sources: `XR18`, `XR16`, `XR12` (XS4 p.11), `MR18` (XS6 `kinds.py`), `X18` (XS5 `constants.js` `MIXER_MODEL`), and `X18`, `MR12`, `XR18V2`, `X18V2`, `MR18V2`, `XR16V2`, `XR12V2`, `MR12V2` (XS10 `model.js`). Which exact strings V2 and "W" units report is `[TO-VERIFY]`; the driver must accept any string that starts with `XR`, `X18`, `MR` and read the channel count from the console (XA-F72), not from the string. | XS4 `[COMMUNITY]`; XS5, XS6 `[PERMISSIVE-CODE MIT]`; XS10 `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F71 | **The DSP is the same on every model.** The vendor specification tables give "Number of processing channels" as "16 input channels, 1 stereo USB return channel, 4 stereo FX return channels, 6 aux buses, main LR" for XR16 and XR12 (XS3), and "18 input channels, 4 FX return channels, 6 aux buses, main LR" for X18/XR18 (XS2), where 17-18 is the stereo aux input. The protocol document lists one meter layout and one snapshot scope for the whole family (16 channels, aux 17-18, 4 FX returns, 6 buses, 4 FX sends, 4 DCAs; XS1 p.3-4). | XS1, XS2, XS3 `[VENDOR-DOC]` |
| XA-F72 | **Contradiction:** XS6 `kinds.py` and XS11 `mixer_type_xr12.py`/`_xr16.py` model XR12 as 12 channels and 2 buses and XR16 as 16 channels and 4 buses. XS6's author says he can only test on an MR18. These counts match the physical outputs, not the DSP. mixcore follows the vendor tables (XA-F71) and confirms at connect: a strip that does not answer its reads is absent for the session (same mechanism as X32-F06). Test item 12. | XS6, XS11 `[PERMISSIVE-CODE MIT]` vs XS2, XS3 `[VENDOR-DOC]`; `[TO-VERIFY]` |
| XA-F73 | Physical inputs differ (matters only for gain/phantom, which v1 does not control): XR18/X18/MR18 have 16 mic preamps; XR16 has 8 mic preamps and 8 line inputs; XR12 has 4 mic preamps and 8 line inputs. The stereo return is "USB return" on XR12/XR16 and "Aux In 17-18" on the 18-channel models. Headamp numbering in XS5: `/headamp/01..16` mic (only 01..08 on XR16, 01..04 on XR12), `17..24` TRS line on XR12/XR16, `17` the aux/RCA input on the 18-channel models. | XS2, XS3 `[VENDOR-DOC]`; XS5 `constants.js` `HA_CONFIG`, `defStrip.js` (`rtn/aux` "USB(x12/x16) or Aux In(x18)") `[PERMISSIVE-CODE MIT]` |
| XA-F74 | No model has a fader surface: X18 is a tablet-dock mixer, the others are rack or stage-box units. **Every parameter change comes from a network client, MIDI, a snapshot load or power-on**, never from an engineer at a desk. | XS2, XS3 (hook-up drawings: control by tablet, PC, phone) `[VENDOR-DOC]` |

### Firmware and hardware revisions
| ID | Fact | Source |
|---|---|---|
| XA-F75 | The vendor protocol document covers "firmware 1.11 or higher". The `/info` example in XS4 shows firmware `1.12`. XS8 verified meters on XR18 firmware 1.17.1. | XS1 p.1 `[VENDOR-DOC]`; XS4 p.11 `[COMMUNITY]`; XS8 `[PERMISSIVE-CODE MIT]` |
| XA-F76 | Firmware 1.18 (October 2021 per the snippets) lists a fix for "a memory issue with long up-times of remote connection" and "better handling of incorrect OSC meter requests". Older firmware may degrade during a long session with remote clients and may misbehave on malformed `/meters` requests. Only snippets of copies were readable. | XS15 `[COMMUNITY]` `[TO-VERIFY]` |
| XA-F77 | Firmware 1.20 (2023-06-29) "Added support for V2 mixers with alternative MCU, FPGA, USB, WIFI module: XR18V2, MR18V2, X18V2" and "for mixers with alternative WIFI module: XR12W, XR16W, MR12W". From 1.20 on, each release ships separate files for hardware V1 and V2; the vendor editor shows the hardware version and refuses the wrong file. | XS13 (verbatim quote of the release note) `[COMMUNITY]` |
| XA-F78 | As of October 2024, 1.22 was current for XR18 V1, XR18 V2, X18 V2 and XR12. A third-party download site lists 1.25 and 1.26; not confirmed on a vendor page. XS19 says the OSC protocol stayed the same across V1 and V2. | XS14, XS19 `[COMMUNITY]`; 1.25/1.26 `[TO-VERIFY]` |

Covered by this file: firmware 1.11 and later, hardware V1 and V2, all six models. The driver never refuses a firmware
version; it logs the version and relies on feature probing (absent features stay absent). Whether to warn users on
firmware older than 1.18 (XA-F76) is a product question (proposed default: show a one-time hint, never block).

### Out of scope
WING, X32/M32 (`x32.md`), DL stage boxes, MIDI control (the X Air MIDI CC map in XS2/XS3), USB audio, firmware update,
every network or preference write (`/-prefs/*`, and `/-action/*` except `/-action/clearsolo`, section 3.8: mixcore never
writes them), FX parameters, RTA, automix, solo settings, routing writes. Processing parameters (gain, EQ, dynamics) are
documented in section 3.4 for later phases and in detail for the next milestone in sections 3.7-3.9 (processing, solo,
names), 7.1 (snapshot list, recall, store) and 8.1 (network check); v1 of the driver has the same scope as `driver/x32`
(names, faders, mutes, mute groups, DCAs, input and main meters).

## 2. Transport, discovery, session

### Transport and encoding
Same as X32: OSC 1.0 messages over UDP, big-endian, 4-byte aligned, types `i`, `f`, `s`, `b`; read = address with no
argument (X32-F04); write = address with one argument (X32-F05); replies go to the requester's IP **and port** (X32-F01,
X32-F28). Differs: the port.

| ID | Fact | Source |
|---|---|---|
| XA-F01 | The mixer listens on UDP **10024** (X32: 10023) and sends replies "back to the requester's IP/port". | XS1 p.1 `[VENDOR-DOC]`; XS4 p.11 `[COMMUNITY]`; XS5 `index.js` (`remotePort: 10024`) `[PERMISSIVE-CODE MIT]` |
| XA-F02 | Type rules: big-endian, 4-byte aligned/padded; floats in 0.0-1.0; ints signed 32-bit; booleans as OSC int; strings NUL-terminated. Set and get may carry string, int, float or blob. | XS1 p.2 `[VENDOR-DOC]` |
| XA-F03 | A set "Sets the value of a console parameter, if it exists and value is in range." No acknowledgement message is defined besides the echo of XA-F22. Reaction to unknown addresses and out-of-range values is not documented `[TO-VERIFY]`; mixcore never sends either. | XS1 p.1 `[VENDOR-DOC]` |
| XA-F04 | A read of a node with several values in one multi-argument message (`/ch/01/config ,siii ...`) works on X Air and not on X32 (Maillot footnote). mixcore does not use node forms on either family. | XS4 p.12 fn. 7 `[COMMUNITY]` |
| XA-F05 | Over Wi-Fi, send bursts must be paced as for X32 (X32-F07). XS6 waits 20 ms per query and says wired links can use less. mixcore keeps the X32 window of 16 reads in flight. | XS6 README `[PERMISSIVE-CODE MIT]`; window is our design choice |

### Identity and discovery
Same as X32: `/xinfo` with no argument answers `,ssss` ip, name, model, firmware; discovery is a broadcast `/xinfo`;
no mDNS. Differs: port 10024, model strings, the name format, `/info` server fields.

| ID | Fact | Source |
|---|---|---|
| XA-F10 | `/xinfo` is answered with `/xinfo ,ssss <ip_address> <device_name> <device_model> <device_version>`. `/info` is answered with `<server_version> <device_name> <device_model> <device_version>`; "First four are defined, and required. Further args are implementation dependent". Example (XS4): `/info ,ssss V0.04 XR18-1D-DA-B4 XR18 1.12` (X32 sends `V2.05 osc-server X32 2.12`: on X Air the second field is the device name, not `osc-server`). | XS1 p.1-2 `[VENDOR-DOC]`; XS4 p.11 `[COMMUNITY]` |
| XA-F11 | Default device name = model plus the last bytes of the MAC address, e.g. `XR18-17-BE-C0`; the same string is the default SSID in access point mode. The name is user-settable (`/-prefs/name`, XS12). | XS2 (network section) `[VENDOR-DOC]`; XS12 `[COMMUNITY]` |
| XA-F12 | Discovery as X32-F12, on port 10024: send `/xinfo` to the limited and the directed broadcast address of every interface from one unconnected socket. Companion sends to each interface's directed broadcast every 30 s (`probe()` runs every 5 s and broadcasts on every 6th run); XS10 every 2 s per interface. | XS5 `index.js` `probe`, `getBroadcastAddresses` `[PERMISSIVE-CODE MIT]`; XS10 `search.js` `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F13 | A broadcasting socket can receive its own `/xinfo` request (no arguments); XS10 drops `/xinfo` messages without exactly 4 arguments for this reason. `driver/x32` already ignores them (its `parseXinfo` needs 4 strings). | XS10 `search.js` (comment "avoids processing our own broadcast messages") `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F14 | A probe that also targets port 10023 finds X32 consoles with the same packet; the reply's source port and the model string tell the family. Sending `/xinfo` to an X Air on 10023 gets no reply `[TO-VERIFY]` (nothing listens there in any source). | derived from XA-F01 and X32-F01 |
| XA-F15 | XS10 reuses the discovery socket for the session because "the desk often associates OSC traffic with the same local IP and ephemeral port that answered discovery. Opening a new socket after search can silently fail until rediscovery." No second source; it conflicts with every other client (XS5, XS6, XS9 open a fresh socket). Test item 16. | XS10 `CONNECTIVITY.md` `[PERMISSIVE-CODE Apache-2.0]` `[TO-VERIFY]` |
| XA-F16 | `/status` is answered with `<status> <ip address> <server name> ...`; status is "active" or "standby" "depending on whether server is currently able to fulfil subscriptions". XS10 sends `/status` with every keep-alive and treats the reply as its liveness signal. Whether "standby" is reported when all `/xremote` slots are taken is `[TO-VERIFY]` (test item 2); if so it is a direct client-limit signal that X32 lacks. | XS1 p.2 `[VENDOR-DOC]`; XS10 `device/index.js` `[PERMISSIVE-CODE Apache-2.0]` |

### Subscriptions, echo and liveness
Same as X32: `/xremote` with no arguments subscribes the sending IP:port to all parameter changes for 10 s and must be
renewed; pushes look like read replies; the protocol has no heartbeat. Differs: 8 clients instead of 4, the echo rule,
the `/xremotenfb` variant.

| ID | Fact | Source |
|---|---|---|
| XA-F20 | `/xremote` "Triggers server to send all parameter changes to maximum eight active clients. Timeout is 10 seconds." Renewal intervals in working clients: 9.5 s (XS5), 8 s (XS7), 5 s (XS9), 2 s (XS10). mixcore keeps its X32 choice of 3 s (X32-F27). | XS1 p.1 `[VENDOR-DOC]`; XS5 `index.js` `pulse`, XS7, XS9 `[PERMISSIVE-CODE MIT]`; XS10 `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F21 | **Client limit: 8** `/xremote` clients (X32: 4). XS9's code comment says "Max 4", without a source; it probably mixes this up with the access point limit (XA-F80). What happens to the ninth client is not documented: as for X32-F21 the simulator default ignores the newcomer until a slot expires, and the opposite policy is an option. | XS1 p.1 `[VENDOR-DOC]`; XS9 `lib/xair.py` comment `[PERMISSIVE-CODE MIT]`; ninth-client behaviour `[TO-VERIFY]` |
| XA-F22 | **Echo differs from X32.** The vendor's server-message table says the mixer "Echoes the value of a parameter in response to a get, set or fulfilment of single parameter subscription". X32 documents a reply only to a get. | XS1 p.2 `[VENDOR-DOC]` |
| XA-F23 | A second subscription command `/xremotenfb` exists on X Air and is used by XS9 (every 5 s) and XS10 (every 2 s). XS9's comment: `/xremote` broadcasts all parameter changes to all active clients; `/xremotenfb` = "No Feed Back. Parameter changes are only sent to the active clients which didn't initiate the change". So with plain `/xremote` the X Air **does push a change back to the client that made it** (X32 does not, X32-F23). The vendor document does not mention `/xremotenfb`. | XS9 `lib/xair.py` `[PERMISSIVE-CODE MIT]`; XS10 `device/index.js` `[PERMISSIVE-CODE Apache-2.0]`; vendor silence: XS1; `[TO-VERIFY]` (test item 1) |
| XA-F24 | Indirect support for XA-F23: Companion X Air keeps one socket for subscription and writes, never updates its own state when it sends, and builds its mute toggle from the last value the mixer sent (`setToggle(self.xStat[strip].isOn, ...)`), so its buttons only show a change if the mixer echoes it. | XS5 `index.js` `sendOSC`, `buildStripDefs.js` `[PERMISSIVE-CODE MIT]` |
| XA-F25 | Liveness: as X32-F25, a quiet mixer sends nothing. `/xinfo` and `/status` both get immediate replies (XA-F10, XA-F16); mixcore keeps the X32 rules (any packet proves the downlink; `/xinfo` every 1 s and after 400 ms of silence; lost after 1.5 s without any packet or 3 s without an `/xinfo` reply). | XS1 `[VENDOR-DOC]`; intervals are our design (X32-F25) |
| XA-F26 | After a power cycle the mixer reloads its autosaved state ("Saves automatically its state (every 2 min)", `/-stat/autosave`), so up to about 2 min of changes can be lost; with `/-prefs/ponmute` ON it "mutes buses/main after power-on". Subscriptions do not survive a reboot `[TO-VERIFY]`. mixcore re-subscribes and re-reads after every reconnect anyway. | XS12 `[COMMUNITY]`; `[TO-VERIFY]` (test item 15) |
| XA-F27 | X32's `/subscribe`, `/formatsubscribe`, `/batchsubscribe`, `/renew` are not in the X Air vendor document. XS10's simulator accepts `/batchsubscribe` and `/renew`, and XS10 sends `/unsubscribe ,s /meters/N` when it stops meters; whether real firmware implements them is `[TO-VERIFY]`. mixcore does not use them. | XS1 (absent) `[VENDOR-DOC]`; XS10 `shared.js`, `virtual-devices/helpers/deskRun.js` `[PERMISSIVE-CODE Apache-2.0]` |

#### Echo design for `driver/xair` (resolves XA-F22..F24 without hardware)
Keep the X32 two-socket session unchanged: the subscription socket sends `/xremote`, reads and meter requests and
never writes; the command socket sends writes and background re-reads and never sends `/xremote`. This is correct
under each of the three readings of the sources:
- H1 (X32-like: no push to the sender): our writes come from the command socket, which is "another client" for the
  subscription socket, so their echoes arrive on the subscription socket, as on X32.
- H2 (XA-F23: `/xremote` pushes to every subscriber including the one that made the change): same as H1, because the
  command socket is not a subscriber.
- H3 (XA-F22 read literally: a set is answered to its sender): the command socket also receives an echo of each write.
  The shared engine (`driver/internal/xosc`, used by `driver/x32` and `driver/xair`) treats every command-socket
  packet as a re-read reply and accepts only replies to an outstanding, untouched read
  (`driver/internal/xosc/reread.go:111-116`); every write marks its parameter touched
  (`driver/internal/xosc/session.go:302`), so such an echo is dropped and can never be taken for a desk change.
Use plain `/xremote` (vendor-documented), not `/xremotenfb` (undocumented; its "initiator" might be judged by IP only,
which would hide the echoes of our command socket). `EchoesOwnWrites` stays true. Test item 1 decides which hypothesis
holds; only the simulator default changes with the answer.

## 3. Address map for the unified model

Same as X32: `/ch/NN` two-digit inputs, `mix/fader`, `mix/on` (1 = on, 0 = muted), `config/name`, `grp/mute` and
`grp/dca` bitmaps (bit n-1 = group n), `/config/mute/N` (1 = engaged), `/dca/N/on` (0 = DCA muted), `/dca/N/fader`.
Differs: counts, unpadded indices on buses, FX and DCAs, `/lr` instead of `/main/st`, aux and FX returns under `/rtn`,
FX send masters `/fxsend/N`, no per-send `on`, no matrices, no main mono.

Polarity facts carried over from X32 and confirmed for X Air: `mix/on` 1 = not muted (XS6 `shared.py` `on` = value 1;
XS5 `isOn = !!v`) `[PERMISSIVE-CODE MIT]`; `/config/mute/N` 1 = group engaged (XS9 `mixerstate.py` toggles the value 1/0
and passes it to the group LED; XS6 README `config.mute_group[0].on = True`) `[PERMISSIVE-CODE MIT]`.

### 3.1 Strips
| Unified strip | X Air address root | Index form | X32 equivalent | Source |
|---|---|---|---|---|
| input 1..16 | `/ch/01` .. `/ch/16` | 2 digits | `/ch/01..32` | XS1 p.2 (`/ch/01/mix/fader` example) `[VENDOR-DOC]`; XS5 `defStrip.js` `[PERMISSIVE-CODE MIT]` |
| auxIn 1 (stereo, 17-18) | `/rtn/aux` | none (one strip) | `/auxin/01..08` | XS5 `defStrip.js`; XS12 `[COMMUNITY]` |
| fxReturn 1..4 (stereo) | `/rtn/1` .. `/rtn/4` | 1 digit | `/fxrtn/01..08` | XS5 `defStrip.js` (`digits: 1`) `[PERMISSIVE-CODE MIT]`; XS7 section 8 `[PERMISSIVE-CODE MIT]` |
| bus 1..6 | `/bus/1` .. `/bus/6` | 1 digit | `/bus/01..16` | XS5 `defStrip.js`; XS7 section 8 ("no padding") `[PERMISSIVE-CODE MIT]` |
| bus 7..10 (FX send masters) | `/fxsend/1` .. `/fxsend/4` | 1 digit | none (X32 uses buses) | XS5 `defStrip.js`; XS12 ("FX1 to FX4 are mixbuses 7 to 10") `[COMMUNITY]` |
| main | `/lr` | none | `/main/st` | XS5 (`/lr/mix/fader`, `/lr/mix/on`); XS11 `mixer_type_xair.py` `[PERMISSIVE-CODE MIT]` |
| dca 1..4 | `/dca/1` .. `/dca/4` | 1 digit | `/dca/1..8` | XS1 p.4 (`/meters/8` "4 dcas") `[VENDOR-DOC]`; XS5 `[PERMISSIVE-CODE MIT]` |
| muteGroup 1..4 | `/config/mute/1` .. `/config/mute/4` | 1 digit | `/config/mute/1..6` | XS5 `defStrip.js` (max 4); XS7 ("always exactly 4") `[PERMISSIVE-CODE MIT]` |
| matrix | none | | `/mtx/01..06` | XS7 section 8 `[PERMISSIVE-CODE MIT]` |

Modelling FX send masters as buses 7..10 is our proposal: sends address them as send slots 07..10 (section 3.3), so a
"bus" in the unified model stays "a destination of sends". They have a master fader, mute and name but no pan, EQ or
dynamics (XS5 `defStrip.js` `fxsend`: `hasPan: false`, `proc: []`).

### 3.2 Parameters per strip
`<s>` is a strip root from 3.1. "v1" marks what the first driver needs (parity with `driver/x32`).

| Field | Address | Type, range | Strips | Source |
|---|---|---|---|---|
| name (v1) | `<s>/config/name` | `s`; max length `[TO-VERIFY]` (X32: 12). Keep the X32 rule (<= 12 printable ASCII) until test item 14. | ch, rtn/aux, rtn/N, bus, fxsend, lr, dca | XS12 `[COMMUNITY]`; XS5 (`name` handled for every strip) `[PERMISSIVE-CODE MIT]` |
| colour | `<s>/config/color` | `i` 0..15: OFF, RD, GN, YE, BL, MG, CY, WH, then the same 8 inverted | same | XS12 `[COMMUNITY]`; XS5 `COLOR_VALUES` `[PERMISSIVE-CODE MIT]` |
| fader (v1) | `<s>/mix/fader`; DCA: `/dca/N/fader` | `f` 0..1, level law XA-F40 | ch, rtn/aux, rtn/N, bus, fxsend, lr, dca | XS5 `buildStripDefs.js` (DCA uses `/fader`, others `/mix/fader`) `[PERMISSIVE-CODE MIT]` |
| mute (v1) | `<s>/mix/on`; DCA: `/dca/N/on` | `i` 1 = on, 0 = muted | same | XS5; XS6 `[PERMISSIVE-CODE MIT]`; XS12 `[COMMUNITY]` |
| mute group engaged (v1) | `/config/mute/N` | `i` 1 = engaged | muteGroup | XS5, XS9 `[PERMISSIVE-CODE MIT]` |
| pan / balance | `<s>/mix/pan` | `f` 0..1 = -100..+100, 101 steps | ch, rtn/aux, rtn/N, bus, lr | XS5 `buildStripDefs.js` (`fSteps: 101`) `[PERMISSIVE-CODE MIT]` |
| main assign | `<s>/mix/lr` | `i` 1 = routed to main (X32: `mix/st`) | ch, rtn/aux, rtn/N, bus | XS5 `defStrip.js` (`lr: mix/lr`) `[PERMISSIVE-CODE MIT]`; XS12 `[COMMUNITY]` |
| mute group membership (v1) | `<s>/grp/mute` | `i` bitmap 0..15 | ch, rtn/aux, rtn/N, bus, fxsend; **not** `/lr` | XS10 `device/bus/mg.js` (`mgHas` excludes main) `[PERMISSIVE-CODE Apache-2.0]`; XS12 (ch, bus, fxsend) `[COMMUNITY]`; `/lr/grp/mute` absent `[TO-VERIFY]` (test item 9) |
| DCA membership (v1) | `<s>/grp/dca` | `i` bitmap 0..15 | ch, rtn/aux, rtn/N, bus, fxsend; not `/lr` | XS10 `device/bus/dca.js` `[PERMISSIVE-CODE Apache-2.0]`; XS12 `[COMMUNITY]` |
| channel link | `/config/chlink/1-2` .. `/15-16` | `i` 0/1 | input pairs | XS12 `[COMMUNITY]`; XS7 `[PERMISSIVE-CODE MIT]` |
| bus link | `/config/buslink/1-2`, `/3-4`, `/5-6` | `i` 0/1 | bus pairs | XS12 `[COMMUNITY]`; XS7 `[PERMISSIVE-CODE MIT]` |
| what a link couples | `/config/linkcfg/preamp`, `/eq`, `/dyn`, `/fdrmute` ("Fader, Mute, Sends") | `i` 0/1 | global | XS12 `[COMMUNITY]` |
| mute preferences | `/-prefs/dcamute`, `/-prefs/hardmute` | `i` OFF/ON; semantics assumed as X32-F39 | global | XS12 `[COMMUNITY]`; semantics `[TO-VERIFY]` |

### 3.3 Sends (monitor mixes)
From an input `/ch/NN`, the aux return `/rtn/aux` or an FX return `/rtn/N`, to send slot `MM`:
`MM` = `01`..`06` for buses 1..6 and `07`..`10` for FX sends 1..4 (always two digits, also after a 1-digit root).

| Field | Address | Type, range | Source |
|---|---|---|---|
| level | `<from>/mix/MM/level` | `f` 0..1, level law XA-F40, **161 steps** (k/160) | XS5 `buildStripDefs.js` (`/mix/` + 2-digit bus + `/level`, `fSteps: 161`) `[PERMISSIVE-CODE MIT]`; XS10 `bus/to/level.js` `[PERMISSIVE-CODE Apache-2.0]` |
| pan | `<from>/mix/MM/pan` | `f` 0..1 = -100..+100. **Scope disputed (re-checked 2026-10-13):** XS5 offers send pan only for the pairs `1-2`, `3-4`, `5-6`, written to the odd slot 01, 03, 05; XS10 offers it to a bus only while that bus is stereo-linked (addressed at the bus's own slot, odd or even) and **never to FX sends 07..10**; XS12 shows only `/ch/01/mix/01/pan`. Send pan on 07..10 and on even slots is `[TO-VERIFY]` (test item 10) | XS5 `buildStripDefs.js:370-393` (pan bus choices 1-2, 3-4, 5-6) `[PERMISSIVE-CODE MIT]`; XS10 `bus/to/pan.js:40-43`, `:93-94` (linked bus only, no effect target) `[PERMISSIVE-CODE Apache-2.0]`; XS12 `[COMMUNITY]` |
| tap | `<from>/mix/MM/tap` | `i` 0..5 = IN, PREEQ, POSTEQ, PRE, POST, GRP (X32: `type` on odd sends only, other labels) | XS12 `[COMMUNITY]`; XS10 `bus/to/tap.js` (same 6 values) `[PERMISSIVE-CODE Apache-2.0]` |
| subgroup on | `<from>/mix/MM/grpon` | `i` 0/1; the send's on/off when tap = GRP | XS12 ("sends group Off/On") `[COMMUNITY]`; XS10 `bus/to/on.js` (`grpon` used only with tap "Same level") `[PERMISSIVE-CODE Apache-2.0]` |
| send on/off | **none**: no `<from>/mix/MM/on` in any source (X32 has `/ch/NN/mix/MM/on`) | | XS12 `[COMMUNITY]`, XS10, XS5 (absent in all three); `[TO-VERIFY]` (test item 10) |

Model consequence: on X Air `Send.on` is an absent capability (never faked); `Send.tap` maps IN/PREEQ/POSTEQ/PRE to
`pre` and POST/GRP to `post` for the model's two-valued tap, with the console word kept for display. Whether tap and
pan exist per send or only per odd send of a linked pair (as on X32) is `[TO-VERIFY]` (test item 10). Sends from FX
returns to FX sends 07..10 are `[TO-VERIFY]`. Proposed default until item 10: no send pan to FX sends 07..10 (two of
the three sources never address it), and send pan to buses 1..6 kept per send as implemented, flagged as possibly
without effect while the pair is unlinked (XS10).

### 3.4 Processing (later phases; not in driver v1)
| Field | Address | Encoding | Source |
|---|---|---|---|
| headamp gain | `/headamp/NN/gain` (2 digits, 01..24) | mic inputs: dB = -12 + 72 f, 145 steps of 0.5 dB; line inputs: dB = -12 + 32 f, 65 steps of 0.5 dB; details and contradictions in section 3.6 (XA-F110..F112) | XS5 `constants.js` `LIMITS` `h145`, `h65`, `HA_CONFIG` `[PERMISSIVE-CODE MIT]`; XS10 `input/gain.js` `[PERMISSIVE-CODE Apache-2.0]`. XS12 lists only -12..+20 for `/headamp/01/gain` (contradiction, trust the code until item 26) |
| phantom | `/headamp/NN/phantom` | `i` 0/1, mic inputs only, per headamp (as X32); XA-F113 | XS5 `HA_CONFIG` (`ph: true` on 1..16) `[PERMISSIVE-CODE MIT]` |
| input source | `/ch/NN/config/insrc` (`i`), `/ch/NN/preamp/rtnsw` (0 = preamp, 1 = USB), `/ch/NN/config/rtnsrc` (USB 1..18) | range of `insrc` contradictory in XS12 ("0-15", 17 labels) | XS12 `[COMMUNITY]`; XS10 README ("`rtnsw` decides whether `insrc` or `rtnsrc` is active") `[PERMISSIVE-CODE Apache-2.0]`; `[TO-VERIFY]` |
| USB trim | `<s>/preamp/rtntrim` | dB = -18 + 36 f, 145 steps | XS5 `LIMITS` `r145` `[PERMISSIVE-CODE MIT]` |
| polarity | `<s>/preamp/invert` | `i` 0/1 | XS5, XS6 `[PERMISSIVE-CODE MIT]` |
| low cut | `/ch/NN/preamp/hpon` (`i`), `/ch/NN/preamp/hpf` (`f`) | log: Hz = 20 x 20^f (20..400 Hz) | XS7; XS10 `bus/lowCut.js` `[PERMISSIVE-CODE]` (MIT, Apache-2.0). XS12 says 20..200 (contradiction) `[TO-VERIFY]` |
| EQ | `<s>/eq/on`; bands `<s>/eq/B/type`, `/f`, `/g`, `/q`; 4 bands on ch, rtn/aux, rtn/N; 6 bands on bus and lr; `<s>/eq/mode` PEQ/GEQ/TEQ on bus and lr | type 0..5 LCut, LShv, PEQ, VEQ, HShv, HCut; Hz = 20 x 1000^f; dB = -15 + 30 f; Q = 10 x 0.03^f (f = 0 is Q 10, f = 1 is Q 0.3) | XS7 section 8; XS10 `bus/equalizer.js` `[PERMISSIVE-CODE]` (MIT, Apache-2.0); XS12 `[COMMUNITY]` |
| graphic EQ | `<s>/geq/<band>` on bus and lr, 31 bands, dB = -15 + 30 f | | XS7 `[PERMISSIVE-CODE MIT]` |
| gate, dynamics | `<s>/gate/...`, `<s>/dyn/...` (thr, ratio index 0..11 = 1.1 .. 100, attack, hold, release, knee, mgain, mix) | ranges in XS7 section 6 | XS7 `[PERMISSIVE-CODE MIT]`; XS12 `[COMMUNITY]` |

Quirk for later phases: XS10 never writes 1.0 to gain, gate, pan and FX parameters but 0.999999, with the comment
"Strange bug when setting 1 in some binary values" (`shared.js:39`) `[PERMISSIVE-CODE Apache-2.0]` `[TO-VERIFY]`
(test item 7). Faders are not in that list.

Sections 3.7-3.9 (XA-F118 onward, read 2026-10-13) refine these rows per strip with sources; where they differ (for
example polarity and low cut exist on input channels only), sections 3.7-3.9 win.

### 3.5 Examples
| What | Message | Bytes (hex) |
|---|---|---|
| Ch 1 fader to 0 dB (wire 0.75) | `/ch/01/mix/fader ,f 0.75` | `2f63682f30312f6d69782f6661646572000000002c6600003f400000` |
| Mute main LR | `/lr/mix/on ,i 0` | `2f6c722f6d69782f6f6e00002c69000000000000` |
| Read ch 1 name | `/ch/01/config/name` (no argument) | |
| Engage mute group 2 | `/config/mute/2 ,i 1` | |
| Bus 3 send from ch 5 to -10 dB | `/ch/05/mix/03/level ,f 0.5` | |
| Read current snapshot | `/-snap/index` (no argument) | |

These byte strings were computed with OSC 1.0 rules (XA-F02); the layout is the one of the X32 example in X32-F43.

### 3.6 Headamp gain, trim and phantom (for core rule CR-23)
Same as X32 (`x32.md` X32-F70..F79): gain and phantom live on the headamp (the physical input), not on the channel;
mic gain uses the same linear law, range and step. Differs: two-digit 1-based indices, a second law for line inputs,
no channel trim, no `/-ha` index, no stage boxes or AES50, no gain split. The vendor protocol document (XS1) has no
headamp entry; everything below is from MIT/Apache code and the quick start guides.

| ID | Fact | Source |
|---|---|---|
| XA-F110 | **Addresses.** `/headamp/NN/gain` (`f`) and `/headamp/NN/phantom` (`i`), NN two digits, **1-based**. Mic inputs `01`..`16` on XR18/X18/MR18, `01`..`08` on XR16, `01`..`04` on XR12. On the 18-channel models `17` is the stereo aux (RCA) input, **one gain for both sides** (XS10 sends the gain of aux L, aux R and the stereo aux all to `/headamp/17`). **Contradiction on XR16/XR12 line inputs:** XS5 addresses them as `/headamp/17`..`24` ("TRS 1..8"); XS10 addresses them as the next numbers after the mic inputs (`/headamp/09`..`16` on XR16, `/05`..`12` on XR12). `[TO-VERIFY]`, item 21; proposed default until then: line-input gain on XR16/XR12 is absent in mixcore. | XS5 `constants.js:162-235` (`HA_CONFIG`), `buildHADefs.js:13-15` `[PERMISSIVE-CODE MIT]`; XS6 `headamp.py` (`index + 1`, 2 digits) `[PERMISSIVE-CODE MIT]`; XS11 `mixer_type_xair.py:60-75` (2 digits), `mixer_type_xr18.py:13`, `_xr16.py:12`, `_xr12.py:12` (16, 8, 4 headamps) `[PERMISSIVE-CODE MIT]`; XS10 `device/input/options.js` (`inputOsc`, input numbers), `device/input/gain.js` (`gainOsc`) `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F111 | **Mic gain = X32 law** (X32-F71, X32-F72): linear, **dB = 72 f - 12**, **-12.0 to +60.0 dB**, **0.5 dB steps, 145 values** (f = k/144). Four code sources agree; XS12 lists -12..+20 for `/headamp/01/gain` (contradiction, probably the line range; unresolved until hardware `[TO-VERIFY]`, item 26). mixcore writes mic gain on this law (lead decision, 2026-10-12: four code sources); if item 26 finds the XS12 range, mic gain becomes read-only until the law is fixed. | XS5 `constants.js:35` (`h145: -12..60`), `:45` (145 steps) `[PERMISSIVE-CODE MIT]`; XS6 `headamp.py` (`lin_get(-12, 60, ...)`) `[PERMISSIVE-CODE MIT]`; XS10 `device/input/gain.js` (`GAIN_MINIMUM_INPUT` -12, `GAIN_MAXIMUM_INPUT` 60, linear scale) `[PERMISSIVE-CODE Apache-2.0]`; XS12 `[COMMUNITY]` |
| XA-F112 | **Line and aux gain:** linear, **dB = 32 f - 12**, **-12.0 to +20.0 dB**, **0.5 dB steps, 65 values** (f = k/64). Applies to the line headamps of XR16/XR12 and to `/headamp/17` (aux) on the 18-channel models. Two code sources; no vendor or community confirmation `[TO-VERIFY]`, item 22. | XS5 `constants.js:38` (`h65: -12..20`, "aux gain"), `HA_CONFIG` (`fSteps: 65` on 17..24) `[PERMISSIVE-CODE MIT]`; XS10 `device/input/gain.js` (`GAIN_MINIMUM_LINE` -12, `GAIN_MAXIMUM_LINE` 20) `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F113 | **Phantom:** `/headamp/NN/phantom` `i`, **0 = off, 1 = on**, mic headamps only (no phantom on line or aux headamps). Vendor specification: "Phantom power, switchable per input 48 V". | XS5 `HA_CONFIG` (`ph: true` only on 1..16), `buildHADefs.js:40-58` `[PERMISSIVE-CODE MIT]`; XS6 `headamp.py` (1/0) `[PERMISSIVE-CODE MIT]`; XS10 `device/input/phantom.js` (preamp inputs only) `[PERMISSIVE-CODE Apache-2.0]`; XS2, XS3 specification tables `[VENDOR-DOC]` |
| XA-F114 | **No channel digital trim.** No source has `/ch/NN/preamp/trim` on X Air (X32: X32-F75). The only input trim is `<s>/preamp/rtntrim`, the **USB return trim**: linear, dB = 36 f - 18, **-18 to +18 dB, 0.25 dB, 145 values**, on `/ch/01..16` and `/rtn/aux`, effective when `<s>/preamp/rtnsw` = 1 (USB source). XS5 labels it "USB Trim (x18 only)"; on XR16/XR12 `[TO-VERIFY]`, item 23. | XS5 `defStrip.js` (`hasTrim`), `buildStripDefs.js:488-530` (`/preamp/rtntrim`, 145 steps), `constants.js:36` (`r145: -18..+18`) `[PERMISSIVE-CODE MIT]`; XS6 `shared.py` `Preamp.usbtrim` (`rtntrim`, -18..18) `[PERMISSIVE-CODE MIT]`; XS10 `device/bus/input.js:15-17`, `:249-253` (`preamp/rtntrim`, -18..18) `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F115 | **Channel to headamp.** No `/-ha/NN/index` exists in any X Air source. A channel's input is `/ch/NN/config/insrc` (`i`), unless `/ch/NN/preamp/rtnsw` = 1, when it is USB return `config/rtnsrc` and has no headamp gain. In XS10 the `insrc` values are: 18-channel models 0..15 = mic 1..16 (`/headamp/01..16`), 16, 17 = aux L, R (`/headamp/17`), 18 = off; XR16 0..7 = mic 1..8, 8..15 = line, 16, 17 = USB L, R, 18 = off; XR12 0..3 = mic 1..4, 4..11 = line, 12, 13 = USB L, R, 18 = off. One source; XS12's `insrc` range is contradictory (section 3.4, input source row) `[TO-VERIFY]`, item 24. Several channels may use one headamp. | XS10 `device/input/options.js` (`optionsPerModelAndBusType`, `xAirId`), `device/bus/input.js` (`idRead`) `[PERMISSIVE-CODE Apache-2.0]`; XS12 `[COMMUNITY]` |
| XA-F116 | **Rounding and echo** are as unknown as for X32 (X32-F72, X32-F79): the driver sends grid values only (k/144 mic, k/64 line, k/144 USB trim) and matches echoes within half a step. XS10 never writes 1.0 to gain (it scales to 0.999999, "Strange bug when setting 1", XA-F101), so its +60 dB is 59.99993 dB on the wire; whether 1.0 is safe `[TO-VERIFY]`, item 22. A gain change from another app is pushed like any parameter under the echo rules of section 2 `[TO-VERIFY]`. | XS10 `shared.js:39`, `device/input/gain.js` `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F117 | **Scope of a gain change.** X Air has no AES50 or stage-box sharing (XA-F73), and no source mentions gain split or gain compensation, so a headamp gain change reaches every channel that uses that input and every mix of this one mixer. A snapshot load can change gain and phantom: snapshot scope has "source" and "input" flags (XA-F60); which of them covers headamp gain is `[TO-VERIFY]`, item 25. | XS1 p.4 (scope flags) `[VENDOR-DOC]`; XS2, XS3 (no AES50 port) `[VENDOR-DOC]` |

Reference points for the line law (the mic law has the X32 table, `x32.md` after X32-F79; the USB trim has the X32
trim column):

| Line gain dB | k (of 64) | f = k/64 | float32 hex |
|---|---|---|---|
| -12.0 (min) | 0 | 0.000000 | `00000000` |
| 0.0 | 24 | 0.375000 | `3ec00000` |
| +10.0 | 44 | 0.687500 | `3f300000` |
| +19.5 | 63 | 0.984375 | `3f7c0000` |
| +20.0 (max) | 64 | 1.000000 | `3f800000` |

Example: mic headamp 1 to 0 dB, `/headamp/01/gain ,f 0.16666667` =
`2f68656164616d702f30312f6761696e000000002c6600003e2aaaab`.

Consequence for CR-23: the core takes range and step from the headamp kind (mic -12..+60 dB, line/aux -12..+20 dB,
both 0.5 dB), not from the family. A 6 dB bound is 12 steps on both laws; on a line input the whole range is 32 dB.
mixcore never writes the wire maximum 1.0 until item 22 shows it is safe (XA-F116; lead decision 2026-10-12, re-review
LOW D): the writable top is +59.5 dB on the mic law and +19.5 dB on the line law; reading 1.0 is +60 / +20 dB.
If item 22 shows that the mixer does not echo a Gain write, X Air Gain becomes read-only in mixcore (lead decision,
review pass 4): the in-flight bound would otherwise rest on the background re-read alone. Until then, on a mixer that
does not echo Gain, a hold with 8 values in flight stops at `atCap` in both directions, lowerings included, until the
next re-read (intended).

### 3.7 Processing per strip (next milestone)
Read 2026-10-13 from XS1 (pages 3-4 re-read on the mirror), XS5, XS6/XS7 (one lineage: XS7 is derived from XS6), XS10
(code and its virtual-X18 state file `src/virtual-devices/x18/data.json`, which XS10's capture tool `x18/capture/`
recorded from a connected mixer; model and firmware of that mixer are not stated, so it is code evidence only) and
XS12. Same as X32: the leaf names, enums and laws of `x32.md` X32-F91..F106, with `<s>` a strip root of section 3.1.
Differs: which strips have what, the key-source list, the EQ mode switch with a built-in GEQ, no crossover EQ types, no
compressor position, no HPF slope. Grid counts are X32's; on X Air they are `[TO-VERIFY]` (item 31), and XS10's state
file holds values on those grids (ch 01 band 1 f 0.245 = 49/200, q 0.2676056 = 19/71; bus 1 band 1 q 0.4647887 = 33/71).
Every write follows the X Air 1.0 rule of XA-F116 until item 7: never the wire maximum 1.0.

| ID | Fact | Source |
|---|---|---|
| XA-F118 | **Low cut, input channels only:** `/ch/NN/preamp/hpon` (1 = on), `/ch/NN/preamp/hpf` **Hz = 20 x 20^f** (20..400 Hz); no `hpslope` in any source `[TO-VERIFY]`, item 27. XS12 gives 20..200 Hz (contradiction, item 28). | XS5 `buildStripDefs.js:46-49` (`preamp/hpon`, proc list of `ch` only), XS7 section 8 `[PERMISSIVE-CODE MIT]`; XS10 `device/bus/lowCut.js` (`lowCutHas` = channel, 20..400 log) `[PERMISSIVE-CODE Apache-2.0]`; XS12 `[COMMUNITY]` |
| XA-F119 | **Polarity, input channels only:** `/ch/NN/preamp/invert`, 1 = inverted. Section 3.4's `<s>/preamp/invert` is narrowed: no source addresses it on `/rtn`, `/bus` or `/lr`. | XS5 `defStrip.js:15` (`invert` only in the `ch` proc list) `[PERMISSIVE-CODE MIT]`; XS10 `device/bus/polarity.js` (channel only) `[PERMISSIVE-CODE Apache-2.0]`; XS12 (`/ch/01` only) `[COMMUNITY]` |
| XA-F120 | **Return sources and trims.** Besides XA-F114/F115 (channels): `/rtn/aux` and `/rtn/1..4` have `preamp/rtnsw` ("preamp/USB" switch, 1 = USB), `preamp/rtntrim` (-18..+18 dB) and `config/rtnsrc` (int 0..8, XS12 labels "U0102, U0304, U0506, - U1718", incomplete); channels' `config/rtnsrc` is int 0..17 = USB 1..18. Meaning per model `[TO-VERIFY]`, item 27. | XS12 `[COMMUNITY]`; XS5 `defStrip.js:34`, `:122` (`rtnsw` on `rtn`, `rtn/aux`) `[PERMISSIVE-CODE MIT]` |
| XA-F121 | **EQ bands, types, laws.** `<s>/eq/on`; **4 bands** `eq/1..4` on `/ch/01..16`, `/rtn/aux`, `/rtn/1..4`; **6 bands** `eq/1..6` on `/bus/1..6`, `/lr`; none on `/fxsend` and `/dca`. Band type int 0..5 = LCut, LShv, PEQ, VEQ, HShv, HCut on every strip (no crossover types). Laws as X32-F97: Hz = 20 x 1000^f, dB = 30 f - 15, Q = 10 x 0.03^f. | XS10 `device/bus/equalizer.js` (`parametersPerType`, `types`, scales) `[PERMISSIVE-CODE Apache-2.0]`; XS7 section 8 (4 or 6 bands, laws) `[PERMISSIVE-CODE MIT]`; XS5 `defStrip.js` (`eq` in the proc lists of ch, rtn, bus, lr, rtn/aux; `fxsend` proc empty) `[PERMISSIVE-CODE MIT]`; XS12 (types 0-5 on ch, bus, lr, rtn) `[COMMUNITY]` |
| XA-F122 | **EQ mode switch (D09).** `/bus/N/eq/mode` and `/lr/eq/mode`: int **0 PEQ, 1 GEQ, 2 TEQ** (parametric, graphic, TrueEQ). Only buses 1..6 and main LR have it: XS10 offers mode, GEQ and TEQ only on bus and main; XS10's state file has `/bus/1/eq/mode` 1 and no `/ch/01/eq/mode`; XS12 lists the mode on bus and LR only. XS6/XS7 put `mode` on every EQ (one generic class, author tested an MR18 only) and XS12 has one stray row `/ch/01/geq/0` "Channel graphics EQ level": input-channel GEQ `[TO-VERIFY]`, item 29. What the switch does to the stored PEQ bands is unknown (item 29); it changes the bus sound at once. | XS10 `device/bus/equalizer.js` (`modeOptions`, `modeHas`, `graphicHas`, `trueHas` = main, secondary) `[PERMISSIVE-CODE Apache-2.0]`; XS6 `shared.py` `EQ.mode` (`peq`, `geq`, `teq`), XS7 `[PERMISSIVE-CODE MIT]`; XS12 `[COMMUNITY]` |
| XA-F123 | **Built-in GEQ bands.** `<s>/geq/<band>` on `/bus/1..6` and `/lr`, 31 bands addressed by name: `20 25 31.5 40 50 63 80 100 125 160 200 250 315 400 500 630 800 1k 1k25 1k6 2k 2k5 3k15 4k 5k 6k3 8k 10k 12k5 16k 20k`; **dB = 30 f - 15** (-15..+15). TEQ uses the same addresses in XS10. XS12 prints `/bus/1/geq/0` (an index), against the named form of XS7, XS10 and the state file `[TO-VERIFY]`, item 29; step not documented (item 29). 217 reads for 7 strips. | XS10 `device/bus/equalizer.js` (`graphOptions`, `graphEqGainOsc`, `trueEqGainOsc`), state file (`/bus/1/geq/1k25` etc.) `[PERMISSIVE-CODE Apache-2.0]`; XS7 section 8 `[PERMISSIVE-CODE MIT]`; XS12 `[COMMUNITY]` |
| XA-F124 | **Compressor** `<s>/dyn/...` on `/ch/01..16`, `/bus/1..6`, `/lr` only; leaves and laws of X32-F102 (`on, mode, det, env, thr, ratio 0..11, knee, mgain, attack, hold, release, mix, auto, filter/on, filter/type, filter/f`), **without `pos`** (no source has it, item 27). `keysrc` on `/ch` and `/bus` only: int **0..22 = SELF, Ch01..Ch16, Bus1..Bus6** (X32: 0..64). | XS10 `device/bus/compressor.js` (`compressorHas` = channel, secondary, main; `sidechainHas` = channel, secondary; `ratioOptions`, `sidechainSourceOptions`) `[PERMISSIVE-CODE Apache-2.0]`; XS6 `shared.py` `Dyn`, XS7 section 6 and 8 `[PERMISSIVE-CODE MIT]`; XS12 `[COMMUNITY]` |
| XA-F125 | **Gate**, input channels only: leaves and laws of X32-F103, `keysrc` 0..22 as XA-F124. **Mode order contradiction:** XS6/XS7 and XS10 `0 EXP2, 1 EXP3, 2 EXP4, 3 GATE, 4 DUCK` (the X32 order) against XS12 "GATE, EXP2, EXP3, EXP4, DUCK"; mixcore follows the code `[TO-VERIFY]`, item 30. | XS10 `device/bus/gate.js` (`modeOptions`, `gateHas` = channel) `[PERMISSIVE-CODE Apache-2.0]`; XS6 `shared.py` `Gate.mode`, XS7 section 6 `[PERMISSIVE-CODE MIT]`; XS12 `[COMMUNITY]` |
| XA-F126 | **Insert** `<s>/insert/on`; the selector is `insert/fxslot` 0..8 = OFF, FX1A, FX1B .. FX4B in XS12 but `insert/sel` in XS10's state file (contradiction). Not needed for D09 (the GEQ is built in); not modelled. | XS12 `[COMMUNITY]`; XS10 state file `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F127 | **Gain-reduction meters.** `/meters ,si "/meters/0" <ch>` returns 8 int16: pre-fader L, R (0, 1), **gate GR (2), comp GR (3)**, post-fader L, R (4, 5), gate key (6), comp key (7). `/meters/6` ("ALL DYN") returns 39 int16: 16 gate GR, 16 channel comp GR, 6 bus comp GR, 1 LR comp GR: one request for a whole GR row. Scale int16/256 dB (XA-F51); sign and zero point of GR are not documented `[TO-VERIFY]`, item 32. XS8 calls `/meters/0` "NOT input meters" (probably requested without the channel int). Rate and lifetime as XA-F56. | XS1 p.3 `[VENDOR-DOC]`; XS10 `device/bus/gate.js`, `compressor.js` (`gainReduction`: `meterId: 0`, `v[2]`, `v[3]`) `[PERMISSIVE-CODE Apache-2.0]`; XS8 README `[PERMISSIVE-CODE MIT]` |

Processing pushes and echoes follow section 2 (no surface: every change comes from another client, MIDI or a snapshot
load); none is vendor-documented for these addresses `[TO-VERIFY]`, item 31.

### 3.8 Solo
| ID | Fact | Source |
|---|---|---|
| XA-F128 | **Solo switches** `/-stat/solosw/NN`, int 1 = on, two digits: **01-16 ch, 17 aux return, 18-21 FX return 1..4, 40-45 bus 1..6, 46-49 FX send 1..4, 50 LR, 51-54 DCA 1..4**. 22-39 are USB returns 1..18 in XS10, marked "Guessing..." `[TO-VERIFY]`, item 33. `/-stat/solo` (1 = any solo) and `/-action/clearsolo ,i 1` as X32-F108. | XS5 `defSolo.js` (offsets 0, 16, 17, 39, 45, 49, 50), `buildSoloDefs.js` (`pad0(offset + n)`) `[PERMISSIVE-CODE MIT]`; XS10 `device/bus/to/on.js` (`monitorValue`), `device/dca/solo.js` (51..54) `[PERMISSIVE-CODE Apache-2.0]`; XS12 ("[01..20??]") `[COMMUNITY]` |
| XA-F129 | **Solo settings** `/config/solo/...` (the phones/monitor bus): `level` (161-step level law), `source` int 0..14 = OFF, LR, LRPFL, LRAFL, AUX, U1718, Bus1..Bus6, Bus12, Bus34, Bus56, `sourcetrim` -18..+12 dB (73 values; X32 -18..+18), `chmode`, `busmode` 0 PFL 1 AFL, `dim`, `dimatt` -40..0 dB, `dimpfl`, `mono`, `mute`. No `dcamode` or `exclusive` in any source. mixcore reads `chmode` and `busmode` and never writes `/config/solo`. | XS12 `[COMMUNITY]`; XS5 `defSolo.js` (`fadeType` 161, 73, 40) `[PERMISSIVE-CODE MIT]`; XS7 section 8 `[PERMISSIVE-CODE MIT]` |
| XA-F130 | **Solo pushes.** XS5 reads each `solosw` once (`pollStats` polls only values not yet received) and then updates only from received messages, so another client's solo is expected as a push like any parameter; not vendor-documented `[TO-VERIFY]`, item 33. | XS5 `index.js:492-534` (`pollStats`), `:740-745` `[PERMISSIVE-CODE MIT]` |

### 3.9 Names, colours, icons
| ID | Fact | Source |
|---|---|---|
| XA-F131 | **Writes** `<s>/config/name ,s` and `<s>/config/color ,i 0..15` on `/ch`, `/rtn/aux`, `/rtn/1..4`, `/bus/1..6`, `/fxsend/1..4`, `/lr`, `/dca/1..4` (section 3.2). **No icons on X Air**: no source has `config/icon`, and XS10's icon feature is hard-wired to "absent". Name length `[TO-VERIFY]`, item 14. Snapshots carry names and colours, so a load can change them (scope flag "config"). The mixer has no surface: these apps and the vendor editors are the only place a name is set or seen, which is why D14 matters on X Air. | XS10 `device/bus/name.js`, `color.js`, `icon.js` (`has` false) `[PERMISSIVE-CODE Apache-2.0]`; XS12 `[COMMUNITY]` |

## 4. Value encodings

### Fader law (level type)
| ID | Fact | Source |
|---|---|---|
| XA-F40 | **Same law as X32-F40**: four straight segments, dB = 40f - 30 (0.5..1), 80f - 50 (0.25..0.5), 160f - 70 (0.0625..0.25), 480f - 90 (0..0.0625); 0.0 is -inf; 1.0 is +10 dB. Used for faders, send levels and solo level. | XS5 `index.js` `faderToDB`; XS6 `util.py` `db_from`/`db_to`; XS7 section 6 `[PERMISSIVE-CODE MIT]` |
| XA-F41 | Faders have **1024 steps** (k/1023) as on X32 (X32-F41); send levels have **161 steps** (k/160). | XS5 `constants.js` (`fLevels[1024]`), `buildStripDefs.js` (`fSteps: 1024` for faders, `161` for sends) `[PERMISSIVE-CODE MIT]`; quantization on X Air `[TO-VERIFY]` (test items 7, 8) |
| XA-F42 | 0.0 = -inf and the lowest non-zero step (k = 1, -89.53 dB) as X32-F42 `[TO-VERIFY]`. | derived from XA-F40, XA-F41 |

Reference points (computed from XA-F40 and XA-F41; the driver's table test must reproduce them):

| dB | exact f | fader step k (of 1023) | f sent = k/1023 | dB shown after rounding | send step (of 160) |
|---|---|---|---|---|---|
| +10 | 1.0 | 1023 | 1.000000 | +10.00 | 160 |
| +5 | 0.875 | 895 | 0.874878 | +5.00 | 140 |
| 0 | 0.75 | 767 | 0.749756 | -0.01 | 120 |
| -5 | 0.625 | 639 | 0.624633 | -5.01 | 100 |
| -10 | 0.5 | 512 | 0.500489 | -9.98 | 80 |
| -20 | 0.375 | 384 | 0.375367 | -19.97 | 60 |
| -30 | 0.25 | 256 | 0.250244 | -29.98 | 40 |
| -40 | 0.1875 | 192 | 0.187683 | -39.97 | 30 |
| -50 | 0.125 | 128 | 0.125122 | -49.98 | 20 |
| -60 | 0.0625 | 64 | 0.062561 | -59.99 | 10 |
| -70 | 0.041667 | 43 | 0.042033 | -69.82 | 7 (-69.00) |
| -80 | 0.020833 | 21 | 0.020528 | -80.15 | 3 (-81.00) |
| -inf | 0.0 | 0 | 0.0 | -inf | 0 |

### Other laws
| Parameter | Formula (f = wire float) | Steps | Reference points | Source |
|---|---|---|---|---|
| pan | -100 + 200 f | 101 (step 2) | f 0 = L100, 0.5 = C, 1 = R100 | XS5 `[PERMISSIVE-CODE MIT]` |
| mic gain | -12 + 72 f dB | 145 (0.5 dB) | 0 dB at f = 24/144 | XS5 `[PERMISSIVE-CODE MIT]` |
| line gain | -12 + 32 f dB | 65 (0.5 dB) | 0 dB at f = 24/64 | XS5 `[PERMISSIVE-CODE MIT]` |
| USB trim | -18 + 36 f dB | 145 (0.25 dB) | 0 dB at f = 0.5 | XS5 `[PERMISSIVE-CODE MIT]` |
| EQ frequency | 20 x 1000^f Hz | `[TO-VERIFY]` | f 0.25 = 112.5 Hz, 0.5 = 632.5 Hz, 0.75 = 3556.6 Hz | XS7, XS10 `[PERMISSIVE-CODE]` |
| EQ gain | -15 + 30 f dB | `[TO-VERIFY]` | f 0.5 = 0 dB | XS7, XS10 |
| EQ Q | 10 x 0.03^f | `[TO-VERIFY]` | f 0 = 10, 0.5 = 1.73, 1 = 0.3 | XS7, XS10 |
| low cut | 20 x 20^f Hz | `[TO-VERIFY]` | f 0.5 = 89.4 Hz, 1 = 400 Hz | XS7, XS10 |
| switches | `i` 0/1 | | `mix/on` 1 = on; `/config/mute/N` 1 = engaged | section 3 |
| bitmaps | `i` 0..15 | | bit n-1 = group n (X32-F36) | XS10 `bus/mg.js` (bit position = group id) `[PERMISSIVE-CODE Apache-2.0]` |

## 5. Echo, push and client limits (summary)
| Question | X32 (baseline) | X Air / M Air |
|---|---|---|
| Changes pushed to subscribers | yes, `/xremote`, 10 s | yes, `/xremote`, 10 s (XA-F20) |
| Push back to the client that made the change | no (X32-F23) | yes with `/xremote`, no with `/xremotenfb` (XA-F23) `[TO-VERIFY]` |
| Reply to a set | none | echo "in response to a ... set" (XA-F22) `[TO-VERIFY]` |
| Push clients | 4 (X32-F21) | 8 (XA-F21) |
| N+1st client | reads and writes, no pushes, no signal | not documented; `/status` "standby" may signal it (XA-F16) `[TO-VERIFY]` |
| Extra device limit | none in the console | 4 Wi-Fi clients in access point mode (XA-F80) |
| Client identity | source IP and port (X32-F28) | replies to "requester's IP/port" (XA-F01); whether a port that never sent `/xremote` takes a slot `[TO-VERIFY]` |

Client-limit detection: reuse the X32 design (unechoed writes, unpushed changes found by re-reads) with limit 8, and add
`/status` as a third signal only if test item 2 shows "standby" for a ninth client.

## 6. Meters
Same as X32: `/meters ,s <bank>` plus optional ints starts a stream to the sender; replies are OSC messages addressed
to the bank with one blob; the blob holds a little-endian int32 count then the values. Differs: values are **int16 in
1/256 dB** (X32: float32 linear), one bank carries both inputs and main L/R, banks have other contents, rate and
lifetime are not in the vendor document.

| ID | Fact | Source |
|---|---|---|
| XA-F50 | Request: `/meters ,s "<id>"` or `/meters ,si "<id>" <chnmeterid>` (the int is optional and selects the channel for `/meters/0`). The vendor example `/meters ,si "/meters/0" 8` returns the meters of channel 9, so `chnmeterid` is 0-based. Bytes from the vendor document: `2f 6d 65 74 65 72 73 00 2c 73 69 00 2f 6d 65 74 65 72 73 2f 30 00 00 00 00 00 00 08`. | XS1 p.1-2 `[VENDOR-DOC]` |
| XA-F51 | Reply: "regular updates ... as a single binary blob, to OSC address <id>"; blob = one int32 size value then N signed int16 values, "resolution 1/256 dB". Working clients read the count as **little-endian** and the number of values (Companion checks `buf[0] == 40` for bank 1) and the values as little-endian int16. | XS1 p.1-3 `[VENDOR-DOC]`; XS5 `index.js` `parseMeters1`, XS7, XS8 `[PERMISSIVE-CODE MIT]`; XS10 `shared.js` (`readIntLE(i, 2) / 256`) `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F52 | Conversion: dBFS = int16 / 256. 0 = 0 dBFS; -32768 (`0x8000`) = -128 dB, which XS7 treats as silence. mixcore maps -32768 to -inf and every other value to v/256. Whether values above 0 occur is `[TO-VERIFY]`. Reading the blob as float32 is a known bug that gives values like -1e29 (XS7, XS8). | XS1 `[VENDOR-DOC]`; XS7 `meters.go` (`MeterSilenceDB = -128.0`), XS8 README `[PERMISSIVE-CODE MIT]` |
| XA-F53 | Bank `/meters/1` "ALL CHANNELS": 40 values = "16 mono + 5x2 fx/aux + 6 bus + 4 fx send (all pre) + 2 st (post) + 2 monitor". Index order used by two independent clients: 0..15 input channels 1..16; 16, 17 aux L, R; 18..25 FX returns 1..4 L, R; 26..31 buses 1..6; 32..35 FX sends 1..4; **36, 37 main L, R**; 38, 39 monitor L, R. Input values are pre-fader; main L/R are post-fader. | XS1 p.3 `[VENDOR-DOC]`; XS5 `buildMeterDefs.js` `[PERMISSIVE-CODE MIT]`; XS7 section 1a `[PERMISSIVE-CODE MIT]` |
| XA-F54 | Contradiction: XS8 says the first 16 values of bank 1 are "post-EQ" input meters (XR18 1.17.1) and that `/meters/2` has 40 values. The vendor document says "all pre" and 36 values for bank 2. "Pre" and "post-EQ" can both be true (pre-fader, post-EQ). Test item 6. | XS8 `[PERMISSIVE-CODE MIT]` vs XS1 `[VENDOR-DOC]`; `[TO-VERIFY]` |
| XA-F55 | Other banks: `/meters/0 <ch>` 8 values (pre-fader L/R, gate GR, comp GR, post-fader L/R, gate key, comp key); `/meters/2` 36 (16 mic, 2 aux, 18 USB: physical inputs, best for gain setting); `/meters/3` 56 (4 x (2 in, 10 FX, 2 out)); `/meters/4` 100 RTA bins; `/meters/5` 44 (6 aux, LR, 16 P16, 18 USB, phones); `/meters/6` 39 (16 gate, 16 ch dyn, 6 bus dyn, LR dyn); `/meters/7` 16 automix gains; `/meters/8` 4 DCAs; `/meters/9` 4 (2 record, 2 play). | XS1 p.3-4 `[VENDOR-DOC]` |
| XA-F56 | Rate and lifetime are **not in the vendor document**. Working clients: XS10 sends `/meters ,siii "/meters/N" <arg> 0 <tf>` with tf 1 commented "50ms * METERS_TIME_FACTOR" and renews every 9 s; XS8 renews every 5 s and says subscriptions "expire at ~10 s" (XR18 1.17.1); XS5 sends `/meters ,si "/meters/1" 0` every 9.5 s, each time after `/-local/updrate ,i 1` whose meaning nobody documents. mixcore assumes 50 ms and 10 s like X32-F50 and renews with `/xremote` every 3 s. | XS10 `shared.js` `[PERMISSIVE-CODE Apache-2.0]`; XS8 `[PERMISSIVE-CODE MIT]`; XS5 `index.js` `pulse` `[PERMISSIVE-CODE MIT]`; `[TO-VERIFY]` (test item 5) |
| XA-F57 | Request form for mixcore: `/meters ,si "/meters/1" 0` (vendor-documented shape, and the exact form Companion has sent for years). mixcore does **not** send `/-local/updrate`: it is a write to an undocumented address. Firmware before 1.18 may mishandle malformed meter requests (XA-F76), so the driver never sends any other shape. | XS1, XS5; design choice |

Meter frame for the core API (contract change needed first, `contracts/core-api/meter-frame.md`): for family `xair`,
level `i` = input `i+1` for `i` = 0..15, level 16 = Main L, level 17 = Main R, `n` = 18, all from bank 1 (one bank, where
X32 needs banks 1 and 2). Input levels are pre-fader by vendor definition.

ОЦЕНКА of meter traffic: one bank 1 packet = 12 (address) + 4 (`,b` tag) + 4 (blob size) + 4 (count) + 80 (40 x int16)
= 104 bytes; at 20 packets/s that is 2080 B/s, about 2 KB/s per client before UDP/IP headers (+28 B per packet, about
2.6 KB/s). Range 1-3 KB/s if the real rate is 10-30 packets/s.

## 7. Scenes (snapshots)
Same as X32: no dedicated "recall happened" event is documented; a recall reaches subscribers as parameter pushes.
Differs: X Air has 64 snapshots and an explicit "recently loaded/saved" index; X32's `/-show/...` and `/-undo/time`
markers do not exist in any X Air source.

| ID | Fact | Source |
|---|---|---|
| XA-F60 | Snapshot node (vendor spelling `/snap/`): `name` (31 chars, "snapshot name for save, recently loaded snapshot name"), `index` [1,64] "recently loaded/saved snapshot number", `load` [1,64] "triggers snapshot load", `save`, `delete`; per slot `01..64/name` (31 chars) and `01..64/scope` (59-char string of `+`/`-` flags: ch1..16, aux 17-18, fxreturn 1..4, busmaster 1..6, fxsendmaster 1..4, main lr, dca 1..4, fx 1..4, source, input, config, eq, dyn, bussend 1..6, fxsend 1..4, fader-pan, mute, routing, console configuration). Writing a slot's `name` or `scope` changes it at once, without a save. | XS1 p.4 `[VENDOR-DOC]` |
| XA-F61 | Every client uses the dashed path `/-snap/...` (`/-snap/index`, `/-snap/load ,i N`, `/-snap/NN/name`); the vendor document prints `/snap/`. mixcore reads `/-snap/index` and treats `/snap/` as `[TO-VERIFY]`. | XS5 `index.js`, `actions.js`; XS11 `mixer_type_xair.py` `[PERMISSIVE-CODE MIT]`; XS10 `scene/load.js` `[PERMISSIVE-CODE Apache-2.0]`; XS12 `[COMMUNITY]` |
| XA-F62 | Whether `/-snap/index` is **pushed** to subscribers after a load by another client is not documented. Companion reads it once at connect and also handles an unsolicited `/-snap/index` message. Test item 13. | XS5 `index.js` (`firstPoll`, handler for `/-snap/index`) `[PERMISSIVE-CODE MIT]`; `[TO-VERIFY]` |
| XA-F63 | Mixing Station stores its bus-protection password, unencrypted, in snapshot 64; official apps ignore it; deleting slot 64 resets it. mixcore must never save into or delete slot 64 as a side effect, and should show slot 64's name as is. | XS16 `[COMMUNITY]` |

mixcore's recall heuristic for X Air (design): the only signal is (a) a burst, at least 8 different parameters pushed
from other clients within 250 ms (the X32 rule, which needs no marker), so every X Air recall has confidence `low`.
(b) A push of `/-snap/index` (if test item 13 shows it is pushed) is only a hint, like the X32 highlighted scene: the
index is "recently loaded/saved" (XA-F60), so a save from X AIR Edit moves it as well. It never raises a recall alone
(a recall ends every held fader, SR-15, and the UI would announce a load that did not happen) and never raises the
confidence; within 1 s of a burst it is attached as `LoadedScene` ("loaded or saved") with signal `snapshotIndex`.
Because no X Air has a surface (XA-F74), "external change" always means another app, MIDI or a load. If test item 13
shows that a save does not move the pushed index, it can become a signal of its own.

### 7.1 Snapshot list, recall and store (next milestone)
Same as X32: a load reaches clients as a burst of pushes, overwrites happen without asking, a slot's contents cannot
be read. Differs: 64 slots 1..64, no `hasdata` flag, a per-slot scope string instead of global safes, no GO, no
snippets or cues, no undo in any source.

| ID | Fact | Source |
|---|---|---|
| XA-F132 | **List.** Read `/-snap/01..64/name` (31 characters) and `/scope`; a slot with an empty (or blank) name is treated as unused by XS10 and shown as "#NN" by XS5. No source has a "slot holds data" flag: whether an unnamed slot can hold a snapshot is `[TO-VERIFY]`, item 34. 128 reads for the whole list. | XS1 p.4 `[VENDOR-DOC]`; XS10 `device/scene/active.js` (non-empty trimmed name), `options.js` (`/-snap/NN`) `[PERMISSIVE-CODE Apache-2.0]`; XS5 `buildSnapshotDefs.js`, `index.js:707-710` (empty name -> default) `[PERMISSIVE-CODE MIT]` |
| XA-F133 | **Scope string** (59 characters, `+` = recalled, `-` or any other character = not recalled): it is the X Air form of recall safes, per slot. **Order contradiction:** the vendor list reads ch1..16, aux, fxreturn1..4, busmaster1..6, fxsendmaster1..4, main lr, **dca1..4, fx1..4**, source, input, config, eq, dyn, bussend1..6, fxsend1..4, fader-pan, mute, routing, console configuration; XS10 indexes 0-15 ch, 16 aux, 17-20 FX return, 21-26 bus, 27-30 FX send, 31 LR, 32 source, 33 input, 34 configuration, 35 EQ, 36 dynamics, 37 level and pan, 38 mute, 39-44 bus sends, 45-48 FX sends, 49 in/out, 50 configuration, **51-54 DCA, 55-58 FX**. Both are 59 long `[TO-VERIFY]`, item 35. Writing a slot's `scope` changes it at once (XA-F60): mixcore reads it and never writes it. | XS1 p.4 `[VENDOR-DOC]`; XS10 `device/scene/scope.js` (`options`, `valueRead`) `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F134 | **Recall.** `/-snap/load ,i N` (1..64) is the only recall: no GO, no highlight, no snippets, no cues. The slot's own scope applies. A reply to the sender is not documented; XS11 sends N as a string, mixcore sends an int (XS5, XS10). After a load expect a burst of pushes (section 7) and run one full read-only re-read once it settles (XS11 waits 1 s and reloads for the same reason, `x32.md` X32-F116). XS12 lists `/-action/mididump` as "used at end of snapshot recall": the mixer's own step, never sent by mixcore. Reply, burst and scope behaviour `[TO-VERIFY]`, item 36. | XS1 p.4 `[VENDOR-DOC]`; XS5 `actions.js:152-175` `[PERMISSIVE-CODE MIT]`; XS10 `device/scene/load.js` `[PERMISSIVE-CODE Apache-2.0]`; XS11 `mixer_type_xair.py:8`, `mixer_base.py:173-183` `[PERMISSIVE-CODE MIT]`; XS12 `[COMMUNITY]` |
| XA-F135 | **Store and delete.** `/-snap/save ,i N` stores the live state into slot N **without asking**; the stored name is `/-snap/name` ("snapshot name for save"), so a store is `/-snap/name ,s <name>` then `/-snap/save ,i N`; XS10 also writes `/-snap/NN/name` first. `/-snap/delete ,i N` deletes. Replies are not documented. Warning from code: XS5's action "Snapshot: Save" sends `/-snap/load` (a store button that loads), so store must be proven on hardware before it ships `[TO-VERIFY]`, item 34. Never slot 64 (XA-F63). | XS1 p.4 `[VENDOR-DOC]`; XS10 `device/scene/save.js`, `erase.js` `[PERMISSIVE-CODE Apache-2.0]`; XS5 `actions.js:177-200` (`save_snap_num` sends `/-snap/load`), `:220-226` (`save_snap` sends `/-snap/save`) `[PERMISSIVE-CODE MIT]` |

Design for the next milestone (not protocol facts): the v1 rule "never sends any other `/-snap/` address" (section "As
implemented") is relaxed only for these gestures: list reads on demand, `/-snap/load` after a confirmation naming the
slot, `/-snap/name` + `/-snap/save` after a confirmation that names the slot it overwrites, never slot 64, never
`scope` or `delete` writes in the first release.

## 8. Wi-Fi, network modes and limits
| ID | Fact | Source |
|---|---|---|
| XA-F80 | Three connection modes chosen with the REMOTE switch: Ethernet LAN, Wi-Fi client, access point. **Access point mode: DHCP server only, "a maximum of 4 clients", Wi-Fi channels 1-11, security WEP 40-bit (5 ASCII characters) or WEP 104-bit (13), default no security.** Specification table: "Access Point, number of clients: Max. 4" for XR18/X18 and XR16/XR12. | XS2, XS3 `[VENDOR-DOC]` |
| XA-F81 | In access point mode the mixer is always `192.168.1.1` and leases `192.168.1.101` to `192.168.1.132`. | XS2 `[VENDOR-DOC]` |
| XA-F82 | Radio: IEEE 802.11 b/g, 2.4 GHz only (2412-2462 MHz). Wi-Fi client mode supports WEP, WPA and WPA2, DHCP client or static IP. | XS2 specification and network section `[VENDOR-DOC]` |
| XA-F83 | Ethernet mode: DHCP client by default, optional DHCP server or static IP; with no DHCP server the mixer takes a link-local address in 169.254.1.0 to 169.254.254.255. "There are no security options for LAN connections". | XS2 `[VENDOR-DOC]` |
| XA-F84 | RESET held 2 s restores default network settings; held 10 s resets all console functions to factory defaults. A wrong static IP or Wi-Fi password makes the mixer unreachable in that mode. | XS2, XS3 `[VENDOR-DOC]` |
| XA-F85 | Whether the Ethernet port still answers OSC while the switch is on access point or Wi-Fi client is not stated `[TO-VERIFY]` (test item 4). | XS2 (silent) |
| XA-F86 | Field reports describe the built-in 2.4 GHz access point as prone to dropouts and recommend an external router wired to the mixer. | XS17 `[COMMUNITY]` `[TO-VERIFY]` |

Product consequence (for the lead, not a protocol fact): in access point mode the whole band shares 4 Wi-Fi
associations, so 4 devices in total can reach the mixer (for example the engineer's tablet plus 3 musician phones), long
before the 8-client OSC limit. Musician monitor seats need an external router; the onboarding screen should say so
when the mixer reports `192.168.1.1` (XA-F81) and 4 seats are used.

### 8.1 Network check data (next milestone)
| ID | Fact | Source |
|---|---|---|
| XA-F136 | **What the mixer reports.** `/xinfo`, `/info` (XA-F10), `/status` "active" or "standby" (XA-F16). Ethernet: `/-prefs/lan/mode` int 0..2 = STATIC, DHCP, DHCPS (DHCP server), `/-prefs/lan/addr/0..3`, `/mask/0..3`, `/gateway/0..3`, `/-stat/netmode` 0..2. Wi-Fi client ("is"): `/-prefs/is/mode` 0 DHCP, 1 STATIC, `/-prefs/is/addr`, `/mask`, `/gateway` `/0..3`, `/-prefs/is/ssid`, `/-prefs/is/security` 0..3 = OPEN, WEP, WPA, WPA2. Access point: `/-prefs/ap/ssid`, `/-prefs/ap/channel` 1..11, `/-prefs/ap/security` 0 OPEN, 1 WEP. `/-prefs/name`. XS10's state file also holds a bare `/-prefs/lan` (meaning unknown). | XS12 `[COMMUNITY]`; XS10 `device/configuration/networkLan.js`, `networkWifiClient.js`, `networkAccessPoint.js`, `device.js`, state file `[PERMISSIVE-CODE Apache-2.0]` |
| XA-F137 | **Secrets.** `/-prefs/is/key` and `/-prefs/ap/key` are the Wi-Fi passwords and answer reads (XS10 reads and writes them; XS12 lists them). mixcore never reads, logs or shows them, and never sends `/-action/updnet` (applies network settings), `/-action/wlanscan` (LAN mode only; results in `/-stat/networks/NN/ssid`, `/strength`, `/flags`) or `/-action/initall`. | XS10 `networkAccessPoint.js:52-54`, `networkWifiClient.js:73-75` `[PERMISSIVE-CODE Apache-2.0]`; XS12 `[COMMUNITY]` |
| XA-F138 | **Not reported.** No source has an address for the REMOTE switch position (which of LAN, Wi-Fi client, access point is active), for the mixer's own Wi-Fi signal strength or for the number of associated Wi-Fi clients `[TO-VERIFY]`, item 37. A client can infer AP mode from the mixer at `192.168.1.1` (XA-F81) and measure round trip and loss itself. | XS2, XS12 (absent) |

## 9. What the vendor apps do
| ID | Fact | Source |
|---|---|---|
| XA-F90 | Vendor apps: "X AIR EDIT for Windows, Mac or Linux", "X AIR for iPad", "X AIR for Android" (XS2, XS3), M AIR EDIT for Midas (XS13); MX-Q (Empower Tribe Innovations DE GmbH) is the vendor-side personal monitor app "for Midas M32, M-AIR, Behringer X32, and X-AIR audio mixers", with sends and pan "for all input channel sends to all aux-buses" and 4 app-local "MCA" groups. | XS2, XS3 `[VENDOR-DOC]`; XS13 `[COMMUNITY]`; XS18 MX-Q listing `[VENDOR-DOC]` |
| XA-F91 | On connect, X AIR Edit asks to "synchronize from mixer to PC"; it warns when the mixer firmware is not supported but lets the user continue. The Android app shows "Mix Access = All" in its connection info, which suggests a per-device restriction to one mix; whether the mixer enforces it or only the app is `[TO-VERIFY]` (likely app-only: nothing in XS1 limits a client). | XS2, XS3 `[VENDOR-DOC]` |
| XA-F92 | M AIR EDIT 1.8 and newer X AIR Edit show the hardware version (V1/V2) on connect. Mixing Station warned "update too new" after firmware 1.20 appeared. | XS13 `[COMMUNITY]` |
| XA-F93 | A lookup of the X AIR iPad app (id 896725230) in the App Store API returned no result in the US, DE and GB stores on 2026-10-12; the store page still shows in search engines. Whether the iPad app is withdrawn is `[TO-VERIFY]` (market-analyst). | XS18 |
| XA-F94 | No public capture of X AIR Edit traffic was found. What it sends on connect (sync order, which subscriptions, meter banks, renewal interval) is unknown; test item 17 is the capture procedure. | searched GitHub, forums, XS1-XS19 |

## 10. Known quirks and failure modes
| ID | Quirk | Impact on the driver | Source |
|---|---|---|---|
| XA-F95 | Firmware before 1.18: memory issue with long remote sessions; malformed meter requests handled badly. | Never send unusual `/meters` shapes; log firmware; product hint for < 1.18. | XS15 `[COMMUNITY]` `[TO-VERIFY]` |
| XA-F96 | V1/V2 hardware need different firmware files since 1.20; the OSC protocol is reported unchanged. | Accept `V2` model suffixes; no protocol branch. | XS13, XS19 `[COMMUNITY]` |
| XA-F97 | Power cycle rolls back to the last autosave (about 2 min) and may mute buses and main (`/-prefs/ponmute`). | After a reconnect that followed long silence, full re-read; never "restore" our last values (invariant 1). | XS12 `[COMMUNITY]` |
| XA-F98 | Access point: 4 devices, WEP only, 2.4 GHz, dropouts reported. | Stale/lost handling as X32; onboarding hint (section 8). | XS2 `[VENDOR-DOC]`; XS17 `[COMMUNITY]` |
| XA-F99 | Discovery socket reuse reportedly needed (XA-F15). | `driver/x32` probes from its own socket and opens new session sockets; test item 16 decides whether xair must reuse it. | XS10 `[PERMISSIVE-CODE Apache-2.0]` `[TO-VERIFY]` |
| XA-F100 | Snapshot 64 may hold Mixing Station's password (XA-F63). | Never touch slot 64 implicitly. | XS16 `[COMMUNITY]` |
| XA-F101 | Some clients avoid writing exactly 1.0 to non-fader parameters (section 3.4). | Irrelevant for v1; test item 7 before processing work. | XS10 `[PERMISSIVE-CODE Apache-2.0]` `[TO-VERIFY]` |

## 11. Same or different, section by section of `x32.md`
| `x32.md` section | X Air |
|---|---|
| Transport and encoding (X32-F01..F07) | Same OSC rules and pacing; port **10024**; vendor-documented type rules (XA-F01..F05). |
| Identity and discovery (X32-F10..F12) | Same `/xinfo` shape and broadcast; port 10024; device name is the network name, model strings XR/X/MR with possible V2 suffixes (XA-F10..F16). |
| Subscriptions, echo, liveness (X32-F20..F29) | `/xremote` 10 s same; **8 clients**; **echo to the sender** with `/xremote`, `/xremotenfb` variant; `/status` active/standby (XA-F16, XA-F20..F27). Two-socket design kept. |
| Sockets of a session | Same rules; one extra open question (XA-F15). |
| Client limit detection | Same evidence model, limit 8, plus `/status` if it signals standby. |
| Parameters (X32-F30..F39) | Same leaf names and polarities; 16 inputs, `/lr`, 4 mute groups, 4 DCAs, 4-bit bitmaps, **no `/lr/grp/mute`** (section 3). |
| Scene recall (X32-F60..F62) | `/-snap/index` names the recently loaded or saved snapshot (a hint); no `/-show` or `/-undo` markers; no surface (section 7). |
| Fader law (X32-F40..F43) | **Same law** and 1024 steps; sends 161 steps (XA-F40..F42). |
| Meters (X32-F50..F55) | **int16 1/256 dB**, bank 1 = inputs + main L/R, rate and lifetime undocumented (XA-F50..F57). |
| Echo and conflict handling | Same rules; echo matching unchanged (section 2 "Echo design"). |
| Background re-read | Same; valid under all echo hypotheses (H1-H3). |

## 12. What `driver/xair` can reuse from `driver/x32`
Read on `d26cf25`; the file references below are to that commit. Since branch `feat/core-xair` the reused code lives
in `driver/internal/xosc` (see "As implemented"). "Reuse" means the code is family-agnostic today; "parametrize" means it works after a constant or a
table becomes a per-family value; "replace" means X Air needs different code. A shared package (for example a family
profile with port, parameter table, meter decoder, scene markers and push-client limit) is one way to avoid copying;
the core-engineer decides.

| Area | Where in `driver/x32` | Verdict for X Air |
|---|---|---|
| OSC codec | `internal/osc/codec.go:25`, `:45` (`Append`, `AppendString`) | Reuse as is. |
| Two sockets, readers, stranger filter | `conn.go:53-74` (`openConn`), `:106-138` (`readLoop`), `:143-150` (`fromConsole`) | Reuse as is; the meter hook at `conn.go:128` must call the X Air decoder. |
| Long-lived link across reconnects | `link.go:36-129` | Reuse as is (X32-F28 holds by XA-F01). |
| Timing and defaults | `driver.go:60-83` | Reuse; 3 s renewal is safe for the 10 s TTL (XA-F20). |
| Liveness | `liveness.go:28-51` | Reuse; `/xinfo` behaves the same (XA-F10, XA-F25). |
| Connect, identify, windowed read, absent features | `connect.go:36-51`, `:86-115` (`identify`, `parseXinfo`), `:129-149` (`readAll`), `:151-161` (`absentFeatures`), `:177-193` (`retryReads`) | Reuse; `capabilities()` at `connect.go:54-66` must take counts and `MaxPushClients` 8 from the family profile. |
| Parameter table | `address.go:15-35` (constants), `:76-100` (`newParamTable`) | Replace the table: 16 inputs, `/lr` for `/main/st` (`address.go:88-89`), 4 mute groups, 4 DCAs, masks 15. **Drop `/main/st/grp/mute` (`address.go:90`)**: X Air has no `/lr/grp/mute` (section 3.2), and because that address belongs to `featureMuteGroups`, an unanswered read would switch mute groups off for the whole session (`connect.go:183-187`, `:151-161`). |
| Conversions | `convert.go:32-99` (fader law), `:103-115` (switch polarity), `:117-122` (mask), `:125-135` (name) | Reuse the fader law and switch polarity unchanged (XA-F40, section 3); mask max 15; name rule as is until test item 14. |
| Writes, echo matching | `session.go:107-136` (`Apply`), `:276-298` (`flushPending`), `:372-389` (`matchEcho`), `:391-401` (`sameWire`) | Reuse; `sameWire` tolerance 0.5/1023 (`session.go:398`) is right for faders, too tight for 161-step send levels when sends arrive (use 0.5/160 there). |
| Event loop | `session.go:189-235` (`Run`), `:333-370` (`decodeChange`) | Reuse; the scene-marker hook at `session.go:342` changes (below). |
| Background re-read | `reread.go:41-130` | Reuse as is; its safety argument holds under H1-H3 (section 2). |
| Client-limit detection | `limit.go:13-94`, `address.go:21` | Reuse the logic with limit 8; optional `/status` signal later. |
| Scene heuristic | `scene.go:14-22` (`/-undo/time`, `/-show/prepos/current`), `:44-79` | Reuse the burst detector (`onConsolePush`); replace the markers with `/-snap/index` (XA-F60..F62). |
| Meters | `meters.go:23-41`, `:69-97` (`handle`), `:123-158` (decoders) | Reuse the sink, prefix check and frame publishing; replace decoding: one bank `/meters/1`, int16 LE / 256 (XA-F51..F53), Main L/R at values 36, 37; no bank 2. Request shape changes (XA-F57) at `session.go:269-274`. |
| Discovery | `discovery.go:28` and `:69` hard-code `DefaultPort` 10023; `Probe` `:76-98`, `collectReplies` `:125-154` | Parametrize the port of `LimitedBroadcast` and `directedBroadcast`; `Probe` and `collectReplies` are reusable (targets carry their port; own broadcast is ignored, XA-F13). |
| Outside the driver | `api/discovery.go:92`, `:122`, `:164-166`, `:184-191`; `api/engine.go:85`, `:157-158`; `api/json.go:31`, `:102`; `contracts/core-api/discover.schema.json:11`, `config.schema.json:10`, `discovery.schema.json:21` (family enum `["x32"]`) | Contract first: add family `xair` (default port 10024) to the three schemas and to `meter-frame.md`, then route discovery and engine by family. |
| Simulator | `sim/x32/sim.go:40-70` (options incl. `EchoToSender` at `:51-54`, `SlotForAnySender`), `params.go:47-68` (tree), `handle.go:188-204` (`pushLocked`), `:239` (`appendMeterBlob`) | `sim/xair`: copy the structure, new tree (section 3), port 10024, limit 8, `EchoToSender` default **true** with H1 as an option (XA-F23), `ReplyToSet` option (H3), int16 meter blobs for bank 1 (40 values), `/status`, `/-snap/index`, `/-snap/load`. |
| Hardware probe | `cmd/mixprobe/main.go:38`, `:88`, `:169` (X32 only) | Add a `--family xair` mode for the automatable items of section 13. |

## Echo and conflict handling in mixcore (required by our safety rules)
Identical to `x32.md` "Echo and conflict handling": writes leave from the command socket and come back as pushes on the
subscription socket (section 2, H1-H3); the last 32 writes per parameter are matched for 2 s; an external change ends a
held gesture; going stale cancels every hold; nothing writes in response to a push. `EchoesOwnWrites` = true.

## Background re-read
Identical to `x32.md` "Background re-read" (every 15 s while live, paced 16 reads per second, from the command socket,
read-only; every v0.3 parameter each pass, then a rotating slice of 256 v0.4 ones; held parameters skipped from the
next batch on). Pass length: the 123 v0.3 reads (90 of the v1 table, 16 mic gains and phantoms, the aux gain) + 256 =
379 reads, about 24 s at 16 reads per 1 s step; a lost push of a fader, mute or Gain is recovered within about 40 s
(one pass plus the 15 s gap), a v0.4 value within ceil(488 / 256) = 2 passes, about 80 s.

## As implemented (driver/xair, sim/xair; branch feat/core-xair, 2026-10-12)
Design choices made when implementing this file. None is a new protocol fact; each `[TO-VERIFY]` item keeps the safe
behaviour above and has a test that pins it, so a hardware result changes one default.
- **Shared engine.** The section 12 "reuse" rows live in `driver/internal/xosc` (session, sockets, link, liveness,
  limit, re-read, scene heuristic, conversions, meter sink, discovery); `driver/x32` and `driver/xair` are profiles of
  it (port, address layout, meter codec, scene markers, push limit). The x32 wire trace is pinned byte for byte by
  `driver/x32/testdata/wire_trace.golden`, the xair one by `driver/xair/testdata/wire_trace.golden`.
- **Strips.** Inputs 1..12 must answer (every source agrees on them); inputs 13..16 are optional strips (XA-F72, test
  item 12). The first connect of a link reads everything else first, then one fader read per optional strip (5 tries,
  about 1.5 s when strips are missing), then the rest of the strips that answered. The link remembers the count: on
  every reconnect exactly those strips are required, so packet loss can delay a reconnect but never drop a real strip,
  and a 12-input mixer reconnects without dead reads. Missing strips are logged at Warn and listed in the snapshot
  (`absentInputs`) until the engine restarts. Group membership of a missing strip does not switch mute groups or DCAs
  off. Main is `/lr` without `grp/mute`; the model reports no main membership
  (capability `MainMuteGroups` false) and the core API omits the field. 90 reads per full pass (section 12 estimate).
- **Gain (core API v0.3, CR-23).** `/headamp/01..16/gain` with the mic law of section 4 (dB = -12 + 72 f, 145 steps
  of 0.5 dB), as headamp strips; each headamp is probed once per link like inputs 13..16, so an XR16 connects with 8
  and an XR12 with 4 (XA-F73). A value outside -12..+60 dB is refused, never clamped, and only +60 dB encodes to the
  wire maximum. Line inputs 17..24 (other law) and the channel-to-headamp routing (`insrc`, `[TO-VERIFY]`) are not
  modelled yet, so Gain is per headamp, not per channel.
- **Echo.** Plain `/xremote`, two sockets, unchanged from X32 (section 2 "Echo design"); tests run H1, H2 and H3.
- **Meters.** `/meters ,si "/meters/1" 0` (XA-F57) with every `/xremote` renewal (3 s). A bank 1 blob is accepted
  only if its count fits the blob and reaches Main R (value 37); values 0..15 and 36/37 are converted (v/256 dBFS,
  -32768 is `-inf`) into a frame of 18 levels.
- **Scene recall.** A burst is the only recall signal. `/-snap/index` pushed with an int 1..64 is a hint naming the
  loaded or saved snapshot, attached to a burst within 1 s; alone it raises nothing; any other argument is dropped. The driver never reads `/-snap/index` at connect and never sends any other `/-snap/`
  address, so snapshot 64 (XA-F63) is never touched.
- **Discovery.** `/xinfo` to 10024 (XA-F12). The core API classifies replies: port 10024 is xair unless the model is
  an X32 model; port 10023 is x32 unless the model starts with XR, X18 or MR; on other ports the model decides.
- **Buses and sends (core-api v0.4, branch feat/core-sends-buses, 2026-10-13).** Buses 1..6 are `/bus/1..6` (kind
  `mix`) and FX sends 1..4 are model buses 7..10 (`/fxsend/1..4`, kind `fx`), the section 3.1 proposal (lead default
  "yes"); each has name, colour, fader and mute (section 3.2). Every bus and FX send is probed once per link (its fader
  read), so a model with fewer buses (XA-F72, test item 12) connects without them. Sends from inputs 1..16 to slots
  01..10 (section 3.3): `level` with the XA-F40 law in 161 steps (k/160, XA-F41; echoes matched within 0.5/160), `pan`
  (-100 + 200 f, 101 steps, section 4) **on slots 01..06 only**: since the 2026-10-13 re-check (section 3.3, item 10)
  the driver neither reads nor writes `/mix/07..10/pan`, the state has no such field, a set is `ERR_UNSUPPORTED` and
  the snapshot's FX bus entries carry no `sendPan`; and `tap` (0..5, read-only, the console words `IN`, `PREEQ`,
  `POSTEQ`, `PRE`, `POST`, `GRP`) on every slot. Which send fields exist comes from this file's table (the family
  profile in `driver/xair/driver.go`), never from what the mixer answers; a field the table has but the mixer does not
  answer is absent too. Whether tap and pan are really per pair is test item 10 (if it is, the X32 pair rule of
  `x32.md` X32-F84/F85 applies). `Send.on` is absent (section 3.3), so a set or
  toggle of `sendOn` is `ERR_UNSUPPORTED`. `grpon`, sends from `/rtn/aux` and `/rtn/N`, and FX-return sends are not
  modelled. Bus links `/config/buslink/1-2`, `/3-4`, `/5-6` (section 3.2) are read-only, on the odd bus. DCA masters
  `/dca/N/fader`, `/dca/N/config/name`, `/dca/N/config/color` (section 3.2) and the colour of inputs, main, buses and
  DCAs (section 3.2 `config/color`) are read; DCA faders are written, colours never. Read count: 16 x (10 x 2 + 6) =
  416 send reads + 40 bus + 3 link + 12 DCA + 17 colour reads = 488 v0.4 reads next to the 123 of the v0.3 table (611).
- **Simulator defaults** (`sim/xair`, options for every `[TO-VERIFY]`): push to the sender with `/xremote` (H2,
  option `NoEchoToSender` for H1), no reply to a set (option `ReplyToSet` for H3), ninth client ignored
  (`EvictOldestWhenFull`), `/status` always "active" (`StandbyWhenNotServed`), `/-snap/index` not pushed on a load
  (`PushSnapIndex`), 16 inputs and 6 buses (`InputChannels`, `Buses`), names up to 12 characters (`NameMaxLen`),
  meters every 50 ms for 10 s.

## 13. To verify with mixprobe or hardware
Setup: an X Air or M Air on a wired switch with a laptop; amplifiers off; record `/xinfo` (model, firmware) and the
hardware version shown by X AIR Edit. Captures go to `research/captures/xair/<item>/` (pcap plus log), taken only on
hardware we legally use. "mixprobe" marks items a `--family xair` mode can automate; "manual" needs a person.

1. **Echo (XA-F22, XA-F23)**, mixprobe. Socket A sends `/xremote` every 3 s; socket B never does. (a) B writes
   `/ch/01/mix/fader` one step lower; log what A and B receive within 500 ms. (b) A writes the same parameter; log A.
   (c) Repeat (a) and (b) with `/xremotenfb` on A. (d) B writes a value equal to the current one. Pass: table of which
   socket got which echo; decides H1/H2/H3 and the simulator defaults.
2. **Client limit and N+1 (XA-F21, XA-F16)**, mixprobe. Open 9 sockets, each renewing `/xremote` every 3 s. Change a
   fader from socket 10 (no `/xremote`). Count which sockets get the push. On socket 9 also send a read, `/status`, and
   a meter request. Pass: number of served clients, which one loses, `/status` text on a served and an unserved socket.
3. **Slot of a write-only port (X32-F28 analogue)**, mixprobe. With 7 subscribers, add our two-socket session; then
   add one more subscriber. Pass: whether our command socket costs a slot.
4. **Access point mode (XA-F80, XA-F85)**, manual. Switch to AP; associate 5 devices; note whether the 5th is refused or
   drops another. With the laptop on the Ethernet port, send `/xinfo` in each REMOTE switch position. Pass: device
   limit behaviour; whether Ethernet answers in AP and Wi-Fi client positions.
5. **Meter request, rate, lifetime (XA-F50, XA-F56, XA-F57)**, mixprobe. Send `/meters ,si "/meters/1" 0` once; log
   inter-packet intervals and the time of the last packet without renewal. Repeat with `,s "/meters/1"` and with
   `,siii "/meters/1" 0 0 2`. Observe only (never send) `/-local/updrate`. Pass: interval (expect 50 ms), lifetime
   (expect about 10 s), accepted forms, effect of the trailing int.
6. **Meter values and taps (XA-F51..F54)**, manual. No signal: expect -32768 for ch 1. 1 kHz tone at -18 dBFS into ch 1:
   expect -4608 (`0xEE00`). Pull the ch 1 fader to -inf: the bank 1 value must not change (pre-fader). Add +6 dB EQ:
   note whether it changes (post-EQ). Pan ch 1 hard left with main up: Main L is value 36, Main R value 37. Pass:
   values, tap, positions.
7. **Fader quantization and 1.0 (XA-F41, XA-F42, XA-F101)**, mixprobe. Write 0.8250244 to `/ch/01/mix/fader`, read back
   (expect step 844, 0.8250244); write 0.0 and 0.0009775 (step 1), read back. Write 1.0 to `/ch/01/mix/pan` (amps off),
   read back. Pass: stored values.
8. **Send steps (XA-F41)**, mixprobe. Write 0.7531 to `/ch/01/mix/01/level`, read back (expect 0.75 if 161 steps).
9. **Group membership addresses (section 3.2)**, mixprobe. Read `/lr/grp/mute`, `/lr/grp/dca`, `/rtn/aux/grp/mute`,
   `/rtn/1/grp/dca`, `/fxsend/1/grp/mute`. Pass: which answer.
10. **Sends (section 3.3)**, mixprobe. Read `/ch/01/mix/01/on` (expect no reply), `/ch/01/mix/02/tap`,
    `/ch/01/mix/02/pan` with bus 1-2 linked and unlinked, `/ch/01/mix/07/pan`, `/ch/01/mix/07/tap`,
    `/rtn/1/mix/07/level`. With a tone on ch 1 sent to bus 1 and buses 1-2 unlinked, write `/ch/01/mix/01/pan` to 0.0
    and 1.0 and log the bus 1 and 2 meters (`/meters/1` values 26, 27). Set tap to GRP and toggle `grpon` from X AIR
    Edit; log. Pass: address set, whether send pan to FX sends exists, and whether unlinked send pan has an effect.
11. **Model and firmware strings (XA-F70, XA-F78)**, mixprobe. Record `/xinfo` and `/info` on every model available
    (especially V2 and "W" units). Pass: exact strings.
12. **DSP counts on small models (XA-F71, XA-F72)**, mixprobe. On an XR12 and an XR16 read `/ch/16/mix/fader`,
    `/bus/6/mix/fader`, `/fxsend/4/mix/fader`, `/rtn/4/mix/fader`. Pass: all answer (vendor tables) or not (XS6/XS11).
13. **Snapshot load (XA-F60..F62)**, mixprobe plus manual. Save snapshots 1 and 2 differing in 3 faders and 1 name
    (lab slots, never 64). Load 2 from X AIR Edit; log all pushes for 3 s. Read `/snap/index` and `/-snap/index`.
    Pass: whether `/-snap/index` is pushed, burst size and duration, whether `/snap/` answers.
14. **Name length (section 3.2)**, mixprobe. Write a 13-character and a 32-character name to `/ch/16/config/name`
    (restore after), read back. Pass: stored length.
15. **Reboot (XA-F26, XA-F97)**, manual. With a subscriber running, power-cycle; log the last and first packets; compare a
    full read before and after; check `/-prefs/ponmute`. Pass: subscription lost, rollback window, power-on mutes.
16. **Socket reuse after discovery (XA-F15)**, mixprobe. Probe with socket P, close P, connect with new sockets; repeat
    20 times, also within 1 s and after 15 s. Pass: every connect gets an `/xinfo` reply within 300 ms, or not.
17. **X AIR Edit traffic (XA-F94)**, manual. Mirror the mixer's switch port; start X AIR Edit, connect, sync mixer to PC,
    open the meters view, idle 60 s, load a snapshot, quit. Pass: pcap with the connect order, subscription and meter
    commands and their renewal intervals.
18. **Unknown address, wrong type, out-of-range (XA-F03)**, mixprobe. Read `/ch/17/mix/fader`; write `,i 1` to
    `/ch/01/mix/fader`; write 1.5 to `/ch/01/mix/pan` (amps off, restore after). Pass: replies or silence, stored values.
19. **Mute semantics (section 3.2)**, manual, as `x32.md` item 13 with `/-prefs/dcamute` and `/-prefs/hardmute`.
20. **Liveness on a quiet mixer (XA-F25)**, mixprobe. Idle 10 min with the session running. Pass: no stale event.

Headamp items (section 3.6). Safety for every write: nothing plugged into the input under test, phantom off unless the
item switches it, the channels using it muted, amplifiers off; restore every value afterwards.

21. **Headamp numbering (XA-F110)**, mixprobe plus manual. On an XR16 and an XR12 read `/headamp/04/gain`,
    `/headamp/05/gain`, `/headamp/09/gain`, `/headamp/12/gain`, `/headamp/16/gain`, `/headamp/17/gain`,
    `/headamp/24/gain`; then change the gain of line input 1 in X AIR Edit and log which `/headamp/NN/gain` is pushed.
    On an XR18 read `/headamp/17/gain` and `/headamp/18/gain` and change the aux input gain in X AIR Edit. Pass: the
    address set per model (XS5 or XS10 numbering) and whether aux has one headamp.
22. **Gain law, ends and rounding (XA-F111, XA-F112, XA-F116)**, mixprobe plus manual. On mic headamp `01` write 0.0,
    0.16666667 and 1.0, read back each and note the dB X AIR Edit shows (expect -12.0, 0.0, +60.0); write the off-grid
    0.17, read back and log the push (expect 0.16666667 if the mixer rounds). On a line or aux headamp write 0.375 and
    1.0 (expect 0.0 and +20.0 dB). Pass: ranges, k/144 and k/64 grids, 1.0 accepted or not.
23. **USB trim (XA-F114)**, mixprobe. On every model read `/ch/01/preamp/trim` (expect no reply),
    `/ch/01/preamp/rtntrim`, `/rtn/aux/preamp/rtntrim`; write `rtntrim` 0.6666667 (+6 dB) with `rtnsw` 0 and read back;
    restore 0.5. Pass: which addresses exist per model.
24. **Channel source (XA-F115)**, mixprobe plus manual. Read `/ch/01/config/insrc` and `/ch/01/preamp/rtnsw`; in X AIR
    Edit set ch 01 input to mic 2, then aux L, then USB 1, logging each push; restore. Pass: the `insrc` value per source
    and per model.
25. **Snapshot scope and gain (XA-F117)**, manual. In a lab slot (never 64) save a snapshot with mic 1 gain at +10 dB;
    set +20 dB; load the slot with the scope flag "input" on, then with only "source" on; log `/headamp/01/gain` pushes.
    Pass: which scope flag restores headamp gain and phantom.
26. **Mic gain law against XS12 (XA-F111)**, mixprobe reads plus manual; mixcore writes nothing. On an XR18 and an
    XR12, in X AIR Edit set mic 1 gain to +20.0, +20.5, +30.0 and +60.0 dB (the knob, not mixcore), and after each
    read `/headamp/01/gain` with mixprobe and log the float32 hex. Expect the mic law: +20.0 = k 64 (`3ee38e39`),
    +20.5 = k 65 (`3ee71c72`), +30.0 = k 84 (`3f155555`), +60.0 = k 144 (`3f800000`). XS12's -12..+20 would show +20.0 as `3f800000`
    and no value above it. Also step the knob by one detent from +30.0 and log the pushed value (expect k 85, 0.5 dB).
    Restore the gain you found. Pass: XA-F111 confirmed (keep mic writes) or XS12 confirmed (mic gain read-only, lead).

`cmd/mixprobe --lab -family xair` checks items 1, 2, 3, 5, 8, 9, 11, 12, 14 and 16, item 20 for 20 s (not 10 min), and
the read parts of 10, 13 and 18 in one guided session, plus fader quantization (7, without the pan write), mute-group polarity, a snapshot load
from another app (13), unplug and reboot (15). Read-only checks run first; every write is guarded (levels and sends
only down, mutes only on, one channel name) and restored. Items 4, 6, 17, 19, the pan part of 7, the link part of 10
and the write part of 18 need a person or a write mixprobe cannot bound; the report lists them as skipped. Items 21-26
are not in mixprobe yet: the read parts of 21, 23, 24 and 26 can be automated; the writes of 22 (+60 dB) and 25 and the
knob moves of 26 need a person.

Processing, solo, snapshot and network items (XA-F118..F138). Safety as for items 21-26; lab snapshot slots 60..63
only, never 64; "A" = a socket renewing `/xremote`, "B" = the write socket.
27. **Addresses per strip (XA-F118..F120, F124)**, mixprobe. Read `/ch/01/preamp/hpslope`, `/rtn/aux/preamp/invert`,
    `/rtn/aux/preamp/hpon`, `/bus/1/gate/on`, `/lr/dyn/keysrc`, `/ch/01/dyn/pos`, `/rtn/1/dyn/on`, `/fxsend/1/eq/on`,
    `/rtn/1/config/rtnsrc`. Pass: which answer (expect none but the last).
28. **Low-cut range (XA-F118)**, manual plus mixprobe read. In X AIR Edit set ch 1 low cut to 200 Hz, then to its
    maximum; read `/ch/01/preamp/hpf` after each. Pass: 0.77 and 1.0 with the maximum shown as 400 Hz (20..400 law), or
    1.0 at 200 Hz (XS12).
29. **EQ mode and GEQ (XA-F122, F123, D09)**, mixprobe plus manual. Read `/bus/1/eq/mode`, `/lr/eq/mode`,
    `/ch/01/eq/mode`, `/rtn/1/eq/mode`, `/bus/1/geq/1k`, `/bus/1/geq/0`, `/ch/01/geq/1k`, `/ch/01/geq/0`. In X AIR Edit
    switch bus 1 to GEQ, move 1 kHz by one step, log A; switch to TEQ, then back to PEQ, and compare `/bus/1/eq/1..6/*`
    before and after. Pass: address set, GEQ step, pushes, PEQ values kept or not.
30. **Gate mode order (XA-F125)**, manual. In X AIR Edit set ch 1 gate mode to Gate, Ducker, Exp 2:1; read
    `/ch/01/gate/mode` after each. Pass: 3, 4, 0 (code order) or 0, 4, 1 (XS12 order).
31. **Grids and pushes of processing (XA-F121, F124)**, mixprobe. From B write the off-grid values of `x32.md` item 35
    to the same X Air addresses; read back; log A. Pass: X32 grids hold on X Air; echo per H1-H3.
32. **GR meters (XA-F127)**, manual. 1 kHz tone at -10 dBFS on ch 1, compressor on (threshold -30 dB, ratio 4.0); send
    `/meters ,si "/meters/0" 0` and `/meters ,si "/meters/6" 0`; log values 2, 3 of bank 0 and 16 of bank 6. Pass: sign
    and zero point of GR (expect about -15 dB, -3840, if GR is negative dB; 0 without compression).
33. **Solo ids and pushes (XA-F128..F130)**, manual. From B write `/-stat/solosw/NN ,i 1` then `,i 0` for NN = 01, 17,
    18, 22, 40, 46, 50, 51; note which strip X AIR Edit shows soloed; log A. Send `/-action/clearsolo ,i 1`. Pass: id
    map (18-21 and 22-39 in particular) and pushes.
34. **Snapshot list and store (XA-F132, F135)**, mixprobe plus manual. Read all `/-snap/NN/name` and `/scope`. Send
    `/-snap/name ,s "mixcore test"` then `/-snap/save ,i 60`; log B and A; read `/-snap/60/name`, `/-snap/index`. Save slot
    61 with an empty `/-snap/name`, then load 61. `/-snap/delete ,i 60`; read its name. Pass: name source on save,
    replies, whether an unnamed slot holds data, delete clears the name.
35. **Scope order (XA-F133)**, manual. In slot 62, in X AIR Edit, clear only "Ch 2", then only "DCA 1", only "EQ", only
    "Bus send 1"; read `/-snap/62/scope` after each. Pass: the index of each flag (vendor or XS10 order).
36. **Recall (XA-F134)**, mixprobe plus manual. Load slots 62 and 63 from B with `/-snap/load ,i N`; log A and B for 3 s;
    then load a slot whose scope excludes EQ after changing ch 1 EQ. Pass: reply or none, burst size and duration,
    scope honoured.
37. **Network status (XA-F136..F138)**, mixprobe. In each REMOTE switch position read `/status`, `/-prefs/lan/mode`,
    `/-stat/netmode`, `/-prefs/lan`, `/-prefs/lan/addr/0..3`, `/-prefs/is/mode`, `/-prefs/is/ssid`,
    `/-prefs/is/security`, `/-prefs/ap/ssid`, `/-prefs/ap/channel`, `/-prefs/ap/security` (never a `/key`). Pass: an
    address whose value follows the switch, or none.
Items 27, 29 (reads), 31, 34 (lab slot 60), 36 (reads and lab loads) and 37 can be added to `cmd/mixprobe --family xair`;
28, 30, 32, 33 and 35 need a person with X AIR Edit or a tone source.

## Open questions for the lead (with recommended defaults)
- Warn on firmware older than 1.18 (XA-F76)? Default: one-time non-blocking hint.
- Access point onboarding hint at 4 devices (section 8)? Default: yes, in the connect flow.
- FX send masters as buses 7..10 in the unified model (section 3.1)? Default: yes, with a role flag for display.
  Implemented that way in core-api v0.4 (bus `kind` `fx`); the lead can still overturn it.

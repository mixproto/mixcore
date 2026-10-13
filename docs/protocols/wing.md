# Behringer WING network control protocol (as mixcore will implement it)

Scope: Behringer WING family consoles: WING (full size, also sold as WING BK), WING Rack, WING Compact. Compatibility
statement only; mixcore is not affiliated with or endorsed by any console maker. This file lists the protocol facts that
`driver/wing` and `sim/wing` will implement. As for X32 and X Air, the driver and the simulator are both written from
this file, never from each other, and when a real console disagrees with this file the console wins: record a
`[CAPTURE]` fact here first, then change the simulator, then the driver.

WING is **not** a member of the X32 OSC dialect. It has two protocols of its own: a binary token protocol over TCP
("native") and an OSC server with a different address tree, value model and subscription rule. Facts have IDs `WG-Fnn`.

Research date: 2026-10-13. No WING hardware was available: there are **no captures**, and every behaviour claim below
is from documents or code. Section 13 lists the exact tests for the hardware session.

## Tags
Tags as in `xair.md`, with the names used in the task brief and in the research protocol in brackets:
- `[VENDOR-DOC]` (`[VENDOR]`, `[OFFICIAL]`): a document published or authorised by the manufacturer (the WING Remote
  Protocols document, release notes, manuals), or a formal specification.
- `[PERMISSIVE-CODE <licence>]` (`[PERMISSIVE]`, `[CODE]`): behaviour read in MIT or Apache-2.0 source code or data.
  The licence is named at each use. Code shows what working clients do, not what the firmware guarantees.
- `[COMMUNITY]`: forum, blog, issue tracker, unofficial changelog.
- `[TO-VERIFY]` (`[UNVERIFIED]`): plausible but not confirmed by two independent sources, or sources contradict.
  Every `[TO-VERIFY]` item is in section 13 and becomes a simulator option.
- `[CAPTURE]`: our own capture of hardware we use. **None yet.**

Legal note (invariant 8): nothing here is copied from GPL code, unlicensed code or decompiled software; no firmware was
looked at. Code was read for behaviour and the facts are written in our own words. Byte strings in this file are
protocol data computed from the rules below (or quoted from the vendor document, where marked).

## Sources (accessed 2026-10-13)
- **WS1** P.-G. Maillot, "WING Remote Protocols - OSC remote control, MIDI SYSEX, Binary Interfaces, and wapi, an API
  for WING", **V3.0.6-30** ("Wing FW 3.0.6-27 and above"), PDF of 2025-09-15, 291 pages. The author writes: "I am
  authorized by Behringer to publish and maintain this "WING remote protocols" document; I am not a MusicTribe
  employee" (p.9), and on the native interface: "this data is coming from Behringer" (p.92). Vendor CDN copy:
  https://cdn.mediavalet.com/aunsw/musictribe/M7u1XZvRGkWxJAVwCKE5BA/7qQ-l43rn0SEnqyb5ZcETg/Original/WING%20Remote%20Protocols_V3.0.6-30.pdf
  (HTTP 500 on access); read from the identical copy attached to Companion issue #158:
  https://github.com/user-attachments/files/23841362/WING.Remote.Protocols_V3.0.6-30.pdf (SHA-256 `1a697166...`).
  The earlier **V3.0.6-27** (2025-06-19) was read from the vendor host
  https://cdn-media.empowertribe.com/f255e02a7d164692b3ac2aff862cfcad/WING%20Remote%20Protocols%20FW%203.0.6.pdf; its
  text differs from -30 only in layout, one URL and `/$ctl/$stat/sof`. Page numbers below are the printed page numbers
  of V3.0.6-30. `[VENDOR-DOC]`.
- **WS2** The same document, **V3.1.0** ("Wing FW 3.1.0 and above"), read only where it appears as the right-hand column
  of a side-by-side diff made by a Companion maintainer: https://github.com/user-attachments/files/23841476/diff.pdf
  (issue #158). Used only for the passages cited. `[VENDOR-DOC]` (through a community-made diff).
- **WS3** Behringer, "WING Firmware Update Version 3.1 (7 November 2025)", 2 pages, copy attached to Companion issue
  #158: https://github.com/user-attachments/files/23841294/Behringer_WING_Firmware_V3.1_2025-11-07.pdf. `[VENDOR-DOC]`.
- **WS4** P.-G. Maillot, "OSC Documentation for WING", V0.3.2, PDF of 2020-08-27 (firmware 1.x era):
  https://wing-docs.com/pdf/OSC_Documentation.pdf. Same author and authorisation statement as WS1. `[VENDOR-DOC]`,
  stale (2020).
- **WS5** Behringer, WING COMPACT quick start manual, read from a ManualsLib copy:
  https://www.adorama.com/col/productManuals/BEHWINGCM.pdf. `[VENDOR-DOC]`.
- **WS6** dannydulai/libwing (also published as dannyfiresnake/libwing), **Apache-2.0**, commit `74c2ff6` (2026-07-09):
  https://github.com/dannydulai/libwing. Read: `src/console.rs`, `src/node.rs`, `SPEC.md`, `ARCHITECTURE.md`,
  `Discovery.md`, `tools/wingmeters.rs`, `tools/wingschema.rs`, and `propmap.jsonl` (60 748 node definitions the tool
  `wingschema` read from a live WING Compact; `SPEC.md`: "Tested against firmware v3.0.5"). `[PERMISSIVE-CODE Apache-2.0]`.
- **WS7** bitfocus/companion-module-behringer-wing, **MIT**, commit `cc16453` (2026-10-06):
  https://github.com/bitfocus/companion-module-behringer-wing. Read: `src/handlers/connection-handler.ts`,
  `device-detector.ts`, `osc-forwarder.ts`, `transitions.ts`, `src/config.ts`, `src/models/*.ts`, `src/commands/*.ts`,
  `companion/HELP.md`. OSC client only. `[PERMISSIVE-CODE MIT]`.
- **WS8** wrodie/behringer-mixer, **MIT**, commit `32352e7` (2026-05-21): https://github.com/wrodie/behringer-mixer.
  Read: `behringer_mixer/mixers/mixer_type_wing.py` (its module docstring records "live tests against a real console
  fimware 3.1"), `mixer_base.py`, `utils.py`. OSC client. `[PERMISSIVE-CODE MIT]`.
- **WS9** matterlobby/behringer-wing-typescript (MIT, `618a7e7`) and dlucasluna/companion-module-behringer-wing-meter
  (MIT, `3c0640f`, package 1.1.0 `main.js`). Both follow WS6's wire patterns, so they are **not independent** of WS6;
  cited only as "another working client does the same". `[PERMISSIVE-CODE MIT]`.
- **WS10** Companion WING issue tracker, threads #124, #158, #162, #169, #212, #227:
  https://github.com/bitfocus/companion-module-behringer-wing/issues. `[COMMUNITY]`.
- **WS11** wing-docs.com, "Unofficial documentation and changelog": https://wing-docs.com/ and `/version/2.0`,
  `/version/2.1`, `/version/3.0`. `[COMMUNITY]`.
- **WS12** D. Brashler, "Behringer WING Firmware 3.1: All Dynamics Now Available in Both Slots" (2025):
  https://drewbrashler.com/2025/behringer-wing-firmware-3-1-dynamics-update/; "How to Upgrade the Behringer WING
  Firmware to Version 3.1" (2025-12-17): https://technicallychurch.com/2025/12/how-to-upgrade-the-behringer-wing-firmware-to-version-3-1-a-complete-walkthrough-new-features/.
  `[COMMUNITY]`.
- **WS13** Search-engine snippets only (pages returned HTTP 403): WING-EDIT V1.2 release notes on manuals.plus
  (https://manuals.plus/m/3aa11333b0783f2bcb918afb25ba184456b935ef9884f589464550839d35052b); behringer.world threads
  https://behringer.world/viewtopic.php?p=12397, ?p=14437, ?p=13291, ?p=14571. `[COMMUNITY]` `[TO-VERIFY]`.
- **S3 of x32.md**: OSC 1.0 specification. `[VENDOR-DOC]` (formal specification).

Pointers not used: pmaillot/wapi (no licence file: not read for facts), OpenMixerProject/OpenWING and
niklasarnitz/OpenWING (GPL-3.0 custom firmware: not read), xn--nding-jua/WingCard and AGoliath/wingview (GPL-3.0: not
read), summershrimp/behringer-wing-firmware-exploration (firmware analysis: excluded, firmware is never a source),
errere/behringer_wing_binary_data_interface_library (MIT; read, adds nothing beyond WS1).

## 1. Models, firmware, scope

### Models
| ID | Fact | Source |
|---|---|---|
| WG-F01 | Three formats share one protocol and one data tree: WING (full size, 24 motorised faders), WING Rack (4U, **no faders**, touch screen and encoders only), WING Compact (12 + 1 faders). "Starting with FW version 3.0 in fall 2024, WING firmware addresses more devices than the 'big' WING ... Note these will be disjoint firmware packages." | WS1 p.11-12 `[VENDOR-DOC]` |
| WG-F02 | DSP counts are the same on every model: channels `/ch/1..40`, aux channels `/aux/1..8`, buses `/bus/1..16`, mains `/main/1..4`, matrices `/mtx/1..8`, DCAs `/dca/1..16`, mute groups `/mgrp/1..8`, FX slots `/fx/1..16`. | WS1 p.47-66 (node ranges) `[VENDOR-DOC]`; WS6 `propmap.jsonl` (40/8/16/4/8/16/8/16 children) `[PERMISSIVE-CODE Apache-2.0]`; WS7 `src/models/*.ts` (same counts for Full, Compact, Rack) `[PERMISSIVE-CODE MIT]` |
| WG-F03 | Local mic preamps: **8 on WING, 24 on Rack and Compact**; WING also has 8 local aux (line) ins and outs that Rack and Compact lack. `/io/in/LCL/1..24`: "All 24 local inputs may not be available depending on the console model". WS7 models 8 local inputs on every model (contradicts the vendor text); WS8 models 8 (WING) and 24 (Rack, Compact). | WS1 p.10-12, p.40 fn. 17 `[VENDOR-DOC]`; WS8 `mixer_type_wing*.py` `[PERMISSIVE-CODE MIT]`; WS7 `src/models/*.ts` `[PERMISSIVE-CODE MIT]` |
| WG-F04 | Model strings in the discovery reply (WG-F11): `ngc-full` (vendor document: "standard Wing console"), `wing-compact` (seen by WS6's author), and, unconfirmed, `wing`, `wing-rack`, `wing-bk`. `/$syscfg/$cnsmdl` and `/$syscfg/$cnscfg` (read-only strings) report the same family of names ("ngc-full", "wing-bk", "wing-rack", "wing-compact"; config strings start with "wing", "wing-rack", "wing-compact"). Firmware 3.1 renamed the firmware file of WING/WING BK to "wing-fullsize"; whether the model string changed with it is `[TO-VERIFY]` (item 1). The driver must accept any model string and read counts from the console (absent nodes stay absent). | WS1 p.16, p.39 `[VENDOR-DOC]`; WS6 `Discovery.md` `[PERMISSIVE-CODE Apache-2.0]`; WS3 `[VENDOR-DOC]` |

### Firmware history (what changes for a remote client)
| ID | Version, date | Change that matters to us | Source |
|---|---|---|---|
| WG-F05 | 1.x (2019-12 to 2023-06; 1.12 = 2021-09-03, 1.13 = 2023-06-19) | OSC on UDP 2223 and native on TCP 2222 exist; OSC subscriptions `/*b` and `/*s` only (no `/*S`); a node definition response has **no** parent field (root definitions are 4 bytes shorter than in 3.x, WG-F33). 1.13 adds beta show control. | WS4 p.10, p.18, p.21 `[VENDOR-DOC]` (2020); WS11 `[COMMUNITY]` |
| WG-F06 | 2.0 (2023-09-12) | Full show control (`/$ctl/lib`). From 2.0 "OSC and native remote protocols form a single (this) document". A release note of WING-EDIT 1.2 says 2.0 "introduces changes to the remote-control API" and requires CoPilot 1.12 / WING-EDIT 1.2 (snippet only). Snapshots with the old dynamic EQ are not compatible. HA remote needs stage box firmware updates. | WS1 p.9 `[VENDOR-DOC]`; WS11 `/version/2.0` `[COMMUNITY]`; WS13 `[TO-VERIFY]` |
| WG-F07 | 2.1 (2024-05-17) | DCA spill; source box shows headamp gain. | WS11 `/version/2.1` `[COMMUNITY]` |
| WG-F08 | 3.0 (2024-10-17), 3.0.5, 3.0.6 (2025-04) | Rack and Compact supported (separate packages). Bus-to-bus sends (`/bus/N/send/M`) and channel/aux sends to all matrices. WING-EDIT 3.2 requires 3.0.6 (snippet). | WS1 p.11 `[VENDOR-DOC]`; WS11 `/version/3.0` `[COMMUNITY]`; WS13 `[TO-VERIFY]` |
| WG-F09 | 3.1 (2025-11-07), 3.1.1 (Rack, reported) | "Increased maximum remote client number from 16 to 24"; colours expanded (WS2: `col` range `1..18`, was `1..12`); "All dynamic processors are available in both the Gate and Compressor slots"; aux channels gain gate and compressor models (WS2 lists `/aux/1/gate/...` and `/aux/1/dyn/mdl`); "Console initialization now overwrites parameters selected in the Global Safe screen"; ALT source moved to the IN scope. Blogs: update the vendor apps with the firmware. A Companion maintainer reports that "the discovery logic has changes slightly" in 3.1 (not explained) `[TO-VERIFY]` (item 1). | WS3 `[VENDOR-DOC]`; WS2 p.16 and its I/O and aux tables `[VENDOR-DOC]`; WS12 `[COMMUNITY]`; WS10 #158 `[COMMUNITY]`; 3.1.1: WS13 `[TO-VERIFY]` |

Covered by this file: firmware 3.0.5 and later (the schema of WS6 and the documents WS1, WS2). The driver never refuses
a firmware version: it logs it, walks or verifies the schema (section 3.0) and treats every node it does not find as
absent. Firmware below 2.0 is out of scope (show control and node definition format differ, WG-F05, WG-F06); proposed
default: connect read-only and show "update the console firmware" (open question 1).

### Out of scope
MIDI and MIDI SysEx control, DAW mode, USB audio, WING-LIVE card file handling, firmware update, network settings
(`/$syscfg/ip*`, never written), library file management (save, delete, rename snapshots), custom controls, layers,
GPIO, every FX model parameter beyond on/off and model name, automix, RTA, Dante/WSG/AoIP modules. They are listed only
where they can surprise the driver (section 10).

## 2. Transport, discovery, session

### 2.1 Which transport, and why (decision for `driver/wing`)
**Use the native binary protocol on TCP 2222 for control, UDP to a port we choose for meters, and UDP 2222 for
discovery. Do not use OSC subscriptions at all.** Reasons, each from facts below:
1. OSC allows **one subscription in the whole console, owned by the last requester** (WG-F21). Two mixcore devices on
   OSC would steal it from each other at every renewal, and we would also break Companion, show controllers and other
   OSC tools on the same console (and they would break us). The native protocol pushes changes to every open TCP
   connection, up to 16 (24 from 3.1) connections (WG-F20, WG-F22).
2. Meters exist only on the native side (WG-F50); OSC has none.
3. OSC does not echo writes to the sender (WG-F23), so an OSC client must poll after every write (WS7 does exactly
   this). On TCP the echo is unknown (WG-F24), but a read-after-write costs one small request on a reliable stream.
4. TCP gives ordered, lossless delivery: no lost pushes, no read windows, no packet pacing for the initial read.
5. Native values are in engineering units on the wire (dB, Hz, ms, WG-F30), the same as our model; the OSC set
   accepts them too, but OSC reads return a three-value triplet that must be parsed (WG-F16).
OSC (UDP 2223) stays useful in two places: a read-only diagnostic (`/ch/1/fdr` with no argument returns the raw
0..1 fader position, which the native side never shows: item 9) and the second discovery method (WG-F12). mixcore
never sends `/*b`, `/*s` or `/*S` (WG-F21).

### 2.2 Ports and framing
| ID | Fact | Source |
|---|---|---|
| WG-F10 | "WING communicates via ports 2223 [UDP], and 2222 [UDP, TCP]". UDP 2222: discovery. TCP 2222: native control ("Applications can communicate with the console using TCP port 2222"). UDP 2223: OSC. Meters: UDP datagrams from the console to a port the client declares (WG-F50). | WS1 p.16, p.92 `[VENDOR-DOC]`; WS6 `console.rs` (TCP 2222, UDP 2222) `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F11 | **Discovery.** Send the 5 ASCII bytes `WING?` (`57 49 4E 47 3F`) to UDP 2222 (unicast or broadcast). The console answers to the requesting IP and port with one comma-separated ASCII datagram: `WING,<ip>,<name>,<model>,<serial>,<firmware>`. Examples: vendor `WING,192.168.1.71,PGM,ngc-full,NO_SERIAL,1.07.2-40-g1b1b292b:develop`; WS6 (anonymised by its author) `WING,192.168.1.19,WING-PP-93892216,wing-compact,01XXXUX06X3AEX,3.0.5-0-g0c2b9d4a:release`. WS6 broadcasts to 255.255.255.255:2222 and waits up to 10 x 500 ms. No mDNS is documented. | WS1 p.16, p.20 `[VENDOR-DOC]`; WS6 `Discovery.md`, `console.rs` `scan()` `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F12 | **Second discovery method (OSC).** OSC `/?` to UDP 2223 is answered with the same string as one `,s` argument. WS7 broadcasts `/?` to 255.255.255.255:2223 every 10 s and forgets a console not seen for 20 s. WS8: the reply's address is `/?` or `/*` "depending on firmware", and `/*` is also the address of generic `OK`/error strings, so only a string that starts with `WING,` is an identity. | WS1 p.20 `[VENDOR-DOC]`; WS7 `device-detector.ts` `[PERMISSIVE-CODE MIT]`; WS8 `mixer_base.py` `[PERMISSIVE-CODE MIT]` |
| WG-F13 | **Byte-stream framing (TCP).** The stream is multiplexed into 14 channels. `0xDF` is the escape byte. `DF D0`..`DF DD` selects the channel for the bytes that follow (vendor table: channel IDs 0..13). A literal `0xDF` inside data is sent as `DF DE`. On receive, `DF` followed by any other byte is a literal `DF` followed by that byte (vendor receive routine), so a sender may escape every data `0xDF` as `DF DE` and always be understood. Example from WS1: `D702DFDEAF0E02` decodes to `D702DFAF0E02`. | WS1 p.92-94 `[VENDOR-DOC]`; WS6 `console.rs` `decode_next`, `format_id` `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F14 | **Channels in use:** `DF D1` = audio engine and control requests (all parameter traffic); `DF D3` = meter requests. Contradictions in WS1: page 16 names "Control-engine ... channel #1, Audio-engine ... channel #2, Meters ... channel #3", the table on p.92 numbers them 2 (ChID 1) and 4 (ChID 3), and the sample transmit routine adds the channel number 1..14 to `0xD0` while the worked example sends channel 1 as `DF D0`. Working clients (WS6, WS9) send `DF D1` for data and `DF D3` for meters, and the vendor meter example on p.98 does the same. mixcore follows the bytes, never the channel numbers. | WS1 p.16, p.92-94, p.98 `[VENDOR-DOC]`; WS6 `console.rs` `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F15 | **Session start.** After the TCP connect the client sends `DF D1`; nothing else (no login, no version handshake) is documented or sent by WS6. There is **no authentication** on any WING protocol. The console has two remote locks instead (WG-F26, WG-F27). | WS6 `console.rs` `connect()` `[PERMISSIVE-CODE Apache-2.0]`; WS1 (no auth in any chapter) `[VENDOR-DOC]` |
| WG-F16 | **OSC grammar (for diagnostics and as a second view).** OSC 1.0 messages only, **no bundles**, max UDP packet 32 KB, replies to the requester's IP and port (or to `/%<port>/...`). Read = address without arguments; floats come back as `,sff` (text, raw 0..1, value), ints as `,sfi` (text, raw, value), strings and enums as `,s`. Example (vendor): `/ch/1/fdr` -> `/ch/1/fdr ,sff "-oo" 0.0 -144.0`. Set = one argument of type `s`, `i` or `f` (`/ch/2/fdr ,f -3.0` sets -3 dB); enum by string or index; `,i -1` toggles a 0/1 int. Any node or parameter may be addressed by hash: `/#f50f69f8` = `/ch/1/mute`. Node text set `/ ,s "/ch.1.fdr=-1,mute=0"` answers `/* ,s OK` or an error string (`NODE NOT FOUND`, `VALUE ERROR`, `BUFFER OVERFLOW`, `NODE IS NOT PAR`, `INCOMPLETE DATA`, `STACK EMPTY`). Large nodes cannot be returned over UDP (32 KB limit). | WS1 p.19-25 `[VENDOR-DOC]` |

### 2.3 Keep-alive, liveness, client limits
| ID | Fact | Source |
|---|---|---|
| WG-F20 | **Push on TCP.** "if a connection is kept active, changes made directly at the console (by moving a fader, or pressing buttons for example) will generate data the application will continuously receive." No subscribe command exists or is needed on TCP. Whether changes made by **other** TCP clients (WING Edit, CoPilot) and by OSC clients are also pushed to us is not stated `[TO-VERIFY]` (item 5). | WS1 p.102, p.112 `[VENDOR-DOC]`; WS6 `ARCHITECTURE.md` (no subscribe step) `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F21 | **OSC subscription.** `/*b` (native binary events in a blob), `/*s` (OSC triplets), `/*S` (single native-typed values that can be sent back unchanged). "A single OSC subscription is active at any time, provided to the last requestor. Subscriptions must be renewed every 10 seconds." `/%<port>/*S` redirects events to another port. The previous subscriber is not told it lost the subscription (no message is documented) `[TO-VERIFY]` (item 6). WS7 renews `/*S` every 9 s and offers "OSC forwarding" because "the WING series only supports one active subscription at a time, with the most recent one being the only receiver". | WS1 p.16, p.30 `[VENDOR-DOC]`; WS2 p.16 (unchanged in 3.1) `[VENDOR-DOC]`; WS7 `connection-handler.ts`, `config.ts`, `HELP.md` `[PERMISSIVE-CODE MIT]` |
| WG-F22 | **Client limit.** "WING can simultaneously communicate with up to 16 connected 'clients'; The console will reject further connection requests ... What we call 'clients' above refer to actual TCP ports ... Some applications may use several ports". Firmware 3.1 raised the limit to **24** ("Make sure your network bandwidth supports this"). UDP (OSC) is not counted. How the 17th (25th) connection is rejected (TCP RST, accept then close, or no SYN-ACK) is not documented `[TO-VERIFY]` (item 7). | WS1 p.16, p.92 `[VENDOR-DOC]`; WS2 p.16 `[VENDOR-DOC]`; WS3 `[VENDOR-DOC]` |
| WG-F23 | **OSC has no echo.** "WING does not echo data sent over UDP by the client application. The client application may nevertheless be notified with an OSC event in case of an error." WS7 therefore sends every OSC set followed by the same address without arguments (a read). | WS1 p.22 `[VENDOR-DOC]`; WS7 `connection-handler.ts` `sendCommand` `[PERMISSIVE-CODE MIT]` |
| WG-F24 | **Native echo of our own writes: unknown.** No source says whether a value we write over TCP is sent back to our connection. WS1 shows that a change of `/ch/1/fdr` or `/ch/1/mute` is followed by pushes of the derived read-only values `/ch/1/$fdr` and `/ch/1/$mute` (hashes `7e463474`, `26855cd3` in that capture). `[TO-VERIFY]` (item 4); the driver design in 2.4 works either way. | WS1 p.30 (`/*b` example) `[VENDOR-DOC]` |
| WG-F25 | **Keep-alive.** "Open connections will time out after 10 seconds of inactivity (on the receiving side). One way to keep a connection active is to request at regular intervals of less than 10 seconds some data from the console." WS6 sends `DF D1` every 7 s and the meter renewal every 3 s. WS10 #124 (OSC client): repeated "connection timed out" turned out to be the client's own watchdog, which fired when it had sent no request; a user's workaround was to poll `/$stat/time` every 1 s. Not a console timeout. | WS1 p.16, p.92 `[VENDOR-DOC]`; WS6 `console.rs` (`DATA_KEEP_ALIVE_SECONDS = 7`, `METERS_KEEP_ALIVE_SECONDS = 3`) `[PERMISSIVE-CODE Apache-2.0]`; WS10 `[COMMUNITY]` |
| WG-F26 | **TCP remote lock.** `/$syscfg/tcplock` (int 0..1, long name "TCP REMOTE LOCK"): "Prevent modifications from TCP input". It is not saved in snapshots and never reset by a console init. What the console does with a write while it is 1 (silently ignored, error, or a push of the unchanged value) is `[TO-VERIFY]` (item 8). | WS1 p.39, p.240-241 (not saved: p.239-240; init: p.241) `[VENDOR-DOC]`; WS6 `propmap.jsonl` `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F27 | **OSC read-only switch.** `/$ctl/OSC/ronly` (int 0..1): "Console OSC read only switch" (Setup > Remote > Remote Lock, OSC). It is in WS1 but not in WS6's 3.0.5 schema `[TO-VERIFY]`. It does not affect TCP. | WS1 p.88, p.240-241 `[VENDOR-DOC]` |
| WG-F28 | WS8 (field notes, firmware 3.1, OSC): "Gain writes work when Remote Lock is OFF and no competing OSC client is connected." The second half (an OSC write fails while another OSC client holds the subscription) has no other source `[TO-VERIFY]` (item 6). | WS8 `mixer_type_wing.py` docstring `[PERMISSIVE-CODE MIT]` |

### 2.4 Session design for `driver/wing` (design, built on the facts above)
- **One TCP connection per mixcore session**, opened once per link and kept across reconnects of the supervisor only
  while it is healthy; on loss it is closed with a FIN before a new one is opened, because each connection holds one of
  16 (24) slots (WG-F22) and a half-open one may hold it until the console's 10 s timeout (WG-F25, item 7).
- **Meters** on one UDP socket opened once per link; its local port goes into every meter request (WG-F50). The core
  API's `ListenPacket` hook covers it; the TCP dial needs a matching `DialContext` hook so that Android can bind it to
  the Wi-Fi network (follow-up for the core-engineer; `driver.Options` has only `ListenPacket` today).
- **Keep-alive and liveness:** a data request for one cheap leaf (`/$syscfg/$firmware`, hash `9e69815d`, section 3.5) every 1 s; any received
  byte proves the downlink; send the probe early after 400 ms of silence; the console is lost after 1.5 s without any
  byte (the X32 design targets SR-05/CR-06). TCP errors end the session at once. The probe also keeps the 10 s timeout
  away by a factor of 10.
- **Initial read:** request the data of each modelled node (`D7 <node hash> DC`, WG-F31); every leaf comes back as
  `D7 <hash> <value>`. Never request the root (`DA DC`): "The WING tree is more than 25000 elements" (WS1 p.14), and the full node
  data takes "2 or 3 seconds" even through wapi (WS1 p.116).
- **Writes:** absolute values only, never the toggle (`D8`) or step (`D9`) tokens, never OSC `,i -1`: a toggle applied
  to a stale state inverts the user's intent.
- **Echo (works under both outcomes of item 4):** the driver remembers each written value per hash for the echo window
  (2 s, the X32 rule). H1 (the console pushes our write back): the push inside the window with the same value is our
  echo. H2 (no echo): 150 ms after the last write to a hash the driver sends a data request for it (a read, never a
  write) and treats the reply like an echo. Under H1 the extra read is harmless. Values are compared with half a step
  of the parameter's law (section 4), never with float equality. `EchoesOwnWrites` = true.
- **Background re-read:** TCP does not lose pushes, so the X32 15 s re-read is not needed for loss; keep a slow pass
  (60 s, read-only, skips held and recently written parameters) only as a guard against pushes the console does not
  send (item 5).
- **Remote lock:** read `/$syscfg/tcplock` at connect and on every push; while it is 1 the session reports a read-only
  state (`ErrUnsupported` on `Apply`) and the UI says the console is locked for remote control.

## 3. Address map for the unified model

### 3.0 Addressing in the native protocol (hashes, schema)
| ID | Fact | Source |
|---|---|---|
| WG-F30 | **Tokens on channel 2** (`DF D1`): `00` false/off/0, `01` true/on/1, `02`..`3F` int 2..63, `40`..`7F` node index 1..64, `80`..`BF` string of 1..64 bytes (length = token - 0x7F), `C0`..`CF` node name of 1..16 bytes, `D0` empty string, `D1 <len-1>` string of 1..256 bytes, `D2 <u16>` node index 1..65536, `D3 <i16>` int16, `D4 <i32>` int32, `D5 <f32>` float32 (the value in its unit: dB, Hz, ms, %), `D6 <f32>` raw float 0.0..1.0, `D7 <u32>` node hash, `D8` click (toggle), `D9 <i8>` step (inc/dec), `DA` go to root, `DB` one level up, `DC` data request, `DD` request node definition, `DE` end of data/definition request, `DF <u16 len>` node definition response. Words and longs are big-endian. | WS1 p.95 `[VENDOR-DOC]`; WS6 `console.rs` `read()`, `set_*` `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F31 | **Grammar.** Select a node (`D7 <hash>`, or `DA` then names/indexes), then either a value token (write), `DC` (read: the console answers `D7 <hash> <value>` for the leaf, or for every leaf of a node), or `DD` (read the definitions of the node's children). A pushed change arrives as `D7 <hash> <value>`. WS6 treats `DE` as the end of a request's answer. Whether a `DC` on a node ends with `DE` is `[TO-VERIFY]` (item 3). | WS1 p.29, p.95, p.122 (`wGetBinaryNode`: node data "composed of many 0xd7<Wing data> parts") `[VENDOR-DOC]`; WS6 `console.rs` `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F32 | **Hashes are stable names.** Every node and parameter has a 32-bit hash; the same hash works in OSC as `/#<hex>`. The hashes of `/ch/1/mute` (`f50f69f8`), `/ch/1/fdr` (`38ae75c2`) and `/ch` (`70b10139`) are identical in the 2020 document (firmware 1.x), the 2025 document and WS6's 3.0.5 schema. The hash function is not documented: a client learns hashes from the console (`DD`) or from a table. | WS1 p.21, p.27, p.30 `[VENDOR-DOC]`; WS4 p.14, p.18 `[VENDOR-DOC]`; WS6 `propmap.jsonl` `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F33 | **Node definition response** (3.x): `DF len.w [len.l if len.w = 0] parent.l hash.l index.w namelen.b name longnamelen.b longname flags.w` then by type: lin float and log float `min.f max.f steps.l`; fader level: nothing; int `min.l max.l`; string enum `count.w` then per item `len.b item len.b longitem`; float enum `count.w` then per item `value.f len.b longitem`; string `maxlen.w`. Flags: bits 0-3 unit (0 none, 1 dB, 2 %, 3 ms, 4 Hz, 5 metres, 6 seconds, 7 octaves), bits 4-7 type (0 node, 1 lin float, 2 log float, 3 fader level, 4 int, 5 string enum, 6 float enum, 7 string), bit 9 read-only. Firmware 1.x responses had no `parent.l` (WG-F05). | WS1 p.28-29, p.95-96 `[VENDOR-DOC]`; WS4 p.18 `[VENDOR-DOC]`; WS6 `node.rs` `from_bytes` `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F34 | **Dynamic (model-dependent) parameters share hashes by slot.** EQ, gate, dynamics, filter and FX parameters depend on the loaded model (`mdl`); the OSC name of a slot changes with the model (`/ch/1/eq/1g` with the STD EQ), but its hash is the slot's: in WS6's schema `/ch/1/eq/STD/1g`, `/ch/1/eq/SOUL/lmq`, `/ch/1/eq/E88/lmf` ... are one hash, and `/ch/1/dyn/COMP/thr` shares its hash with 18 other models' parameters. wapi names these slots 1..40. **The meaning and the law of such a hash depend on the current `mdl`**: the driver must read `mdl` first, take the law from the definition of the current model, and re-read the slot definitions when `mdl` changes. | WS1 p.26, p.134 `[VENDOR-DOC]`; WS6 `propmap.jsonl`, `ARCHITECTURE.md` ("multiple names can map to the same ID (due to dynamic properties)") `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F35 | **Schema differs by firmware and model** (WG-F05..F09): 3.1 added aux gate/dyn models and 18 colours; WS1 documents `/aux/1/in/set/$g` and `$vph` but WS6's 3.0.5 Compact schema has no gain on aux channels; WS1 says DCA and mute-group names are "8 chars max" but WS6's schema says `maxstringlen` 16 (WS8 observed 8 on DCAs). The node definitions are the console's own statement and win over the document. | WS1 p.52, p.65 `[VENDOR-DOC]`; WS2 `[VENDOR-DOC]`; WS6 `propmap.jsonl` `[PERMISSIVE-CODE Apache-2.0]`; WS8 docstring `[PERMISSIVE-CODE MIT]` |

Design for the driver: ship a generated table (path -> hash, type, law) for the parameters we model, produced by a
**read-only** introspection of our own console (`DD` only; never set `mdl` to enumerate models, which is what WS6's
`wingschema` does destructively, see its own warning in `tools/wingschema.rs`). At connect, request the definitions of
the modelled nodes (`D7 <hash> DD`) and compare: a hash that is missing makes that feature absent; a law that differs
replaces the table's law for the session. This also covers 3.0 vs 3.1 differences without version branches. WS6's
`propmap.jsonl` (Apache-2.0) may seed tests and the simulator fixture if its notice is kept (lead decision).

### 3.1 Strips
WING numbers every strip from 1, without zero padding: `/ch/1`, not `/ch/01`.

| Unified strip | WING root | Count | Notes | Source |
|---|---|---|---|---|
| input 1..40 | `/ch/N` | 40 | mono, stereo or M/S by its source (`in/set/$mode`, read-only); no channel pairing | WS1 p.47-51 `[VENDOR-DOC]` |
| auxIn 1..8 | `/aux/N` | 8 | full input strips with fewer processing blocks (no gate, PSE/LA dynamics before 3.1) | WS1 p.52-54; WS2 `[VENDOR-DOC]` |
| fxReturn | **absent** | 0 | WING FX are inserts ("A maximum of 16 stereo inserts can be used for applying internal FX"); a "return" is an ordinary `/ch` or `/aux` with an FX insert or a bus source | WS1 p.10, p.50 (`preins`/`postins` `NONE, FX1..FX16`) `[VENDOR-DOC]` |
| bus 1..16 | `/bus/N` | 16 | stereo unless `busmono` = 1 | WS1 p.55-57 `[VENDOR-DOC]` |
| matrix 1..8 | `/mtx/N` | 8 | | WS1 p.62-64 `[VENDOR-DOC]` |
| main 1..4 | `/main/N` | 4 | four mains; `/cfg/mainlink` OFF, 2, 2-3, 2-4 links mains 2..4 to main 1 | WS1 p.34, p.59-61 `[VENDOR-DOC]` |
| dca 1..16 | `/dca/N` | 16 | | WS1 p.65 `[VENDOR-DOC]` |
| muteGroup 1..8 | `/mgrp/N` | 8 | have names (X32 groups have none) | WS1 p.65 `[VENDOR-DOC]` |

### 3.2 Parameters per strip
`<s>` is a strip root from 3.1. "v1" marks parity with `driver/x32`.

| Field | Address | Type, range | Strips | Source |
|---|---|---|---|---|
| name (v1) | write `<s>/name`; read `<s>/$name` | string, 16 chars (DCA, mgrp: 8 or 16, WG-F35). `$name` is read-only and "reflects linked source or current strip value": it is what the console shows | all; `$name` not on dca, mgrp | WS1 p.48 `[VENDOR-DOC]`; WS8 docstring item 1 `[PERMISSIVE-CODE MIT]` |
| colour | write `<s>/col`; read `<s>/$col` | int 1..12 (<= 3.0.6), 1..18 (3.1); 1-based, no "off" | ch, aux, bus, main, mtx, dca | WS1 p.48, p.254 `[VENDOR-DOC]`; WS2 `[VENDOR-DOC]`; WS8 item 2-3 `[PERMISSIVE-CODE MIT]` |
| icon | `<s>/icon` (`$icon` read-only) | int 0..999 | same | WS1 p.48 `[VENDOR-DOC]` |
| fader (v1) | `<s>/fdr` | fader level, dB, -144 (= -inf) .. +10 (WG-F60) | ch, aux, bus, main, mtx, dca | WS1 p.48 `[VENDOR-DOC]`; WS6 `[PERMISSIVE-CODE Apache-2.0]` |
| effective fader | `<s>/$fdr` | read-only, "fader level as affected by dca" | ch, aux, bus, main, mtx | WS1 p.51 `[VENDOR-DOC]` |
| mute (v1) | `<s>/mute` | int, **1 = muted, 0 = on** (opposite of X32's `mix/on`) | all, mgrp too | WS1 p.22 ("Will mute channel 1 it is was unmuted", sic), p.48, p.65 `[VENDOR-DOC]` |
| effective mute | `<s>/$mute` | read-only int 0..2; meaning of 2 `[TO-VERIFY]` (item 12) | ch, aux, bus, main, mtx | WS1 p.51 `[VENDOR-DOC]` |
| mute override | `<s>/$muteovr` | int 0..1; with `/$ctl/cfg/muteovr` ("MUTEGROUP/SIP OVERRIDE") the strip's own button can override a group | ch, aux, bus, main, mtx | WS1 p.51, p.72 (listed there as `/$globals/muteovr`, "Chan strip mute overrides mute group") `[VENDOR-DOC]`; WS6 (`/$ctl/cfg/muteovr`) `[PERMISSIVE-CODE Apache-2.0]` |
| pan | `<s>/pan` | lin -100..+100, 201 steps | ch, aux, bus, main, mtx | WS1 p.48 `[VENDOR-DOC]` |
| width | `<s>/wid` | lin -150..+150 %, 61 steps | ch, aux, bus, main, mtx | WS1 p.48 `[VENDOR-DOC]` |
| solo | `<s>/$solo` | int 0..1; console-wide `/$stat/solo` read-only | all but mgrp | WS1 p.32, p.48 `[VENDOR-DOC]` |
| DCA membership (v1) | `<s>/tags` | string, 80 chars; tags `#D1`..`#D16` "are 'reserved' for DCA1..16 assignment". Separator and order of several tags `[TO-VERIFY]` (item 11) | ch, aux, bus, main, mtx | WS1 p.51, p.54, p.57, p.61, p.64 `[VENDOR-DOC]`; WS5 ("a channel can be assigned to a DCA through HOME>TAGS") `[VENDOR-DOC]` |
| mute group membership (v1) | tags, exact token **unknown** | the manual: on a source "add mute group tags so that the source is muted when the corresponding mute group is active"; the snapshot scope "TAGS" covers "Custom Tags / DCA, Mute, Talk Tags". Whether channel strips carry mute tags too, and their spelling, are `[TO-VERIFY]` (item 11); a forum post says later firmware separated mute groups and DCAs from tags (snippet) | sources `/io/in/<grp>/N/tags`, maybe strips | WS5 `[VENDOR-DOC]`; WS1 p.238 `[VENDOR-DOC]`; WS13 `[COMMUNITY]` `[TO-VERIFY]` |
| DCA mutes channels | `/cfg/dcamgrp` | int 0..1: "DCA mutegroups (DCA mute mutes all channels assigned to DCA)" | global | WS1 p.34 `[VENDOR-DOC]` |
| mute group engaged (v1) | `/mgrp/N/mute` | int, 1 = group muting | mgrp | WS1 p.65 `[VENDOR-DOC]`; WS8 (`/mgrp/{n}/mute`) `[PERMISSIVE-CODE MIT]` |
| monitor bus | `<s>/mon` | enum A, B, A+B | all but mgrp | WS1 p.48 `[VENDOR-DOC]` |
| scribble light | `<s>/led` | int 0..1 | ch, aux, bus, main, mtx, dca | WS1 p.48 `[VENDOR-DOC]` |

Effective mute toward the mains (model): own `mute` OR any engaged member mute group OR (when `/cfg/dcamgrp` = 1) any
muted member DCA. Until item 12 shows what `$mute` = 2 means, mixcore shows `$mute` as the console's own answer and
computes nothing beyond it. WS10 #227 (a maintainer): "The mute state is an ever evolving thing on the Wing, because
the state is not internally consistent" `[COMMUNITY]`.

### 3.3 Sends (monitor mixes) and main assignment
| From | To | Address | Fields | Source |
|---|---|---|---|---|
| ch, aux | bus 1..16 | `<s>/send/B` | `on` (1 = on), `lvl` (fader level dB), `pon` ("pre always on"), `mode` (`PRE`, `POST`, `GRP`; long names TAP, POST, GROUP), `plink` (pan link, 0 = individual), `pan` (-100..100, 201 steps) | WS1 p.50-51, p.54 `[VENDOR-DOC]`; WS6 `[PERMISSIVE-CODE Apache-2.0]` |
| ch, aux | matrix 1..8 | `<s>/send/MX1`..`MX8` | same fields | WS1 p.51 `[VENDOR-DOC]` (ch to matrix since 3.0, WG-F08) |
| bus | bus 1..16 (3.0+) | `/bus/N/send/M` | `on`, `lvl`, `pre` (0/1). "Sending bus 'n' to bus 'n' is not possible ... will be ignored." | WS1 p.57 `[VENDOR-DOC]`; WS8 item 7 `[PERMISSIVE-CODE MIT]` |
| bus | matrix | `/bus/N/send/MXm` | `on`, `lvl`, `pre` | WS1 p.57 `[VENDOR-DOC]` |
| main | matrix | `/main/N/send/MXm` | `on`, `lvl`, `pre` | WS1 p.61 `[VENDOR-DOC]` |
| ch, aux, bus | main 1..4 | `<s>/main/M` | `on` (assigned), `lvl` (level into that main), `pre` | WS1 p.50, p.53, p.57 `[VENDOR-DOC]` |
| ch (send tap) | | `/ch/N/ptap` | where PRE sends tap: `IN`, `FILT`, `3`, `4`, `5`, `PFL` (pre fader), `AFL` (post fader), `POST` (post processing) | WS1 p.48 `[VENDOR-DOC]`; WS6 (long names) `[PERMISSIVE-CODE Apache-2.0]` |

Defaults in the vendor node dump of `/ch/1`: sends 1-8 `mode=PRE plink=0`, 9-10 `mode=GRP plink=1`, 11-16
`mode=POST plink=1`, matrices `PRE` (WS1 p.25). Model mapping: `Send.on` = `on`; `Send.tap` = pre for `PRE`, post for
`POST`; `GRP` ("GROUP") is shown with the console's word and mapped to neither until item 13 says what it follows.
`pon` = 1 keeps a pre send live when the channel is muted (our reading of "pre always on" `[TO-VERIFY]`, item 13).
`Send.pan` exists when `plink` = 0. WS1 footnote 26 (p.50) says the channel's main sends are `1..8` "on WING Rack model, used
for the 4 headphones stereo outputs"; WS6's Compact schema has 4 `[TO-VERIFY]` on a Rack (item 2).

### 3.4 Inputs, headamps and processing
| Field | Address | Encoding | Source |
|---|---|---|---|
| channel source | `/ch/N/in/conn/grp`, `/in` (and `altgrp`, `altin`; `in/set/altsrc` selects) | grp enum `OFF, LCL, AUX, A, B, C, SC, USB, CRD, MOD, PLAY, AES, USR, OSC, BUS, MAIN, MTX`; in 1..64 | WS1 p.47 `[VENDOR-DOC]` |
| source (headamp) gain | `/io/in/LCL/N/g`, `/io/in/A|B|C/N/g` | lin float dB, law per WG-F62 | WS1 p.40-41 `[VENDOR-DOC]`; WS6 `[PERMISSIVE-CODE Apache-2.0]` |
| phantom | `/io/in/LCL|A|B|C/N/vph` | int 0/1 | WS1 p.40 `[VENDOR-DOC]` |
| gain seen from the channel | `/ch/N/in/set/$g`, `$vph` | gain of whatever source the channel uses; range "depends on source type"; writable per WS1 (no RO mark), `$vph` write-tested by WS8, `$g` writes reported not to work through one client before its fix (WS10 #162) `[TO-VERIFY]` (item 14) | WS1 p.47 `[VENDOR-DOC]`; WS8 item 6 `[PERMISSIVE-CODE MIT]`; WS10 `[COMMUNITY]` |
| source mode, mute, polarity | `/io/in/<grp>/N/mode` (M, ST, M/S), `/mute`, `/pol` | | WS1 p.40-43 `[VENDOR-DOC]` |
| headamp type | `/io/in/LCL|A|B|C/N/$ha` | read-only int 0..5 (0..4 in WS6 and for C) | WS1 p.40-41 `[VENDOR-DOC]` |
| HA remote | `/io/in/<grp>/N/rmt` (OFF, AES A, AES B, AES C), `$ract`, `$rdest`; `/$globals/harmt/a|b|c` | which console controls the preamp over AES50 | WS1 p.40, p.90 `[VENDOR-DOC]` |
| channel trim, balance, polarity | `/ch/N/in/set/trim` (lin -18..18 dB, 361 steps), `bal` (-9..9, 181), `inv` | | WS1 p.47 `[VENDOR-DOC]` |
| input delay | `/ch/N/in/set/dlyon`, `dlymode` (M, FT, MS, SMP), `dly` | | WS1 p.47 `[VENDOR-DOC]` |
| filters | `/ch/N/flt/lc`, `lcf` (log 20..2000 Hz, 641), `lcs` (6, 12, 18, 24), `hc`, `hcf` (log 50..20000 Hz, 833), `hcs` (6, 12), `tf`, `mdl` (TILT, MAX, AP1, AP2) | | WS1 p.47 `[VENDOR-DOC]`; WS6 `[PERMISSIVE-CODE Apache-2.0]` |
| EQ | `<s>/eq/on`, `mdl`, `mix`; STD: `lg lf lq leq`, `1g 1f 1q` .. `4g 4f 4q` (ch, aux), `.. 6g 6f 6q` (bus, main, mtx), `hg hf hq heq` | gain lin -15..15 dB, 301; freq log 20..20000 Hz, 961 (low 20..2000, 641; high 50..20000, 833); Q log 0.44..10, 181 | WS1 p.49, p.55-56 `[VENDOR-DOC]`; WS6 `[PERMISSIVE-CODE Apache-2.0]` |
| pre-send EQ | `/ch/N/peq/on`, `1g 1f 1q`..`3g 3f 3q` | as EQ | WS1 p.48 `[VENDOR-DOC]` |
| gate | `/ch/N/gate/on`, `mdl`; GATE: `thr` (-80..0, 161), `range` (3..60, 115), `att` (0..120 ms, 121), `hld` (0..200 ms), `rel` (log 4..4000 ms, 130), `acc`, `ratio`; sidechain `/ch/N/gatesc/...` | dynamic slots, WG-F34 | WS1 p.48-49 `[VENDOR-DOC]` |
| compressor | `<s>/dyn/on`, `mdl`, `mix`, `gain` (-6..12, 37); COMP: `thr` (-60..0, 121), `ratio` (float enum 1.1 .. 100, 17 values), `knee` (0..5), `det`, `att`, `hld`, `rel`, `env`, `auto`; `dynxo`, `dynsc` | dynamic slots, WG-F34; aux before 3.1: PSE/LA combo fields `thr depth fast peak ingain cpeak cmode` | WS1 p.49-50, p.53 `[VENDOR-DOC]`; WS6 `[PERMISSIVE-CODE Apache-2.0]` |
| inserts | `<s>/preins/on`, `ins` (NONE, FX1..FX16), `$stat`; `<s>/postins/...` (+ `mode` FX, AUTO_X, AUTO_Y on channels) | | WS1 p.50-51 `[VENDOR-DOC]` |
| process order | `/ch/N/proc` (24 orders of G, E, D, I) | | WS1 p.48 `[VENDOR-DOC]` |
| FX slots | `/fx/N/mdl` (1..8 all models, 9..16 no reverbs), `fxmix`, `$a_chn`, `$a_pos`, then up to 40 model parameters | | WS1 p.66, p.134 `[VENDOR-DOC]` |

**Headamp ownership and scope.** Gain and phantom belong to the **source**, not to the channel ("Sources ... own
headamp parameters like gain and phantom power", WS1 p.13), so one gain change reaches every channel, recorder output and
second console fed by that source. AES50 sources may be owned by another console through HA remote (rows above). As for
X32 (x32.md X32-F76), what a write to a source this console does not own does is unknown `[TO-VERIFY]` (item 15).
Proposed default: Gain is writable for `LCL` sources only; AES50 (`A`, `B`, `C`) gain is shown read-only until item 15.
WS8: "AES50 inputs return default values even with no stagebox connected" `[PERMISSIVE-CODE MIT]`, so a value does not
prove a box is there; `$ha` and `/$stat/A|B|C/stat` (`-, OK, ERR, UPD`) do (WS1 p.32).

### 3.5 Console-wide items
| Item | Address | Notes | Source |
|---|---|---|---|
| identity | `/$syscfg/consolename` (16), `$firmware` (64), `$serial`, `$cnsmdl`, `$cnscfg`, `$chwversion` | read-only except the name | WS1 p.39 `[VENDOR-DOC]` |
| time (cheap probe) | `/$stat/time`, `/$stat/date` | read-only strings | WS1 p.32 `[VENDOR-DOC]` |
| solo config | `/cfg/solo/mode` (LIVE, STUDIO, SIP), `chtap`/`bustap`/`maintap`/`mtxtap` (PFL, AFL) | | WS1 p.35 `[VENDOR-DOC]` |
| monitor A/B (phones, speakers) | `/cfg/mon/1|2/$lvl` (read-only on the full-size console: the knob owns it), `src`, `srclvl`, `dim`, `pfldim` | | WS1 p.34-35 `[VENDOR-DOC]` |
| talkback | `/cfg/talk/A|B/$on`, `mode` (AUTO, PUSH, LATCH), `B1..B16`, `MX1..MX8`, `M1..M4`; `/cfg/talk/assign` (OFF, CH40, AUX8) | | WS1 p.36-38 `[VENDOR-DOC]` |
| oscillator | `/io/in/OSC/1|2/osc/lvl` (-40..6 dB), `mode` (SINE, PINK, WHITE), `f` | | WS1 p.43 `[VENDOR-DOC]` |
| startup mute | `/$globals/startmute` ("Mute outputs on startup") | | WS1 p.90 `[VENDOR-DOC]` |
| global safes | `/$ctl/safes/ch` (40 chars of `+` or space), `aux`, `bus`, `main`, `mtx`, `dca`, `mute`, `fx`, `source/<grp>`, `output/<grp>`, ... | protect sections from scene loads | WS1 p.86-87, p.242 `[VENDOR-DOC]` |
| USB player, recorder | `/play/$action` (IDLE, STOP, PLAY, PAUSE, NEXT, PREV, PLAYFILE), `/rec/$action` (STOP, REC, PAUSE, NEWFILE) | valid only with an open playlist | WS1 p.70 `[VENDOR-DOC]`; WS8 items 9-10 `[PERMISSIVE-CODE MIT]` |

### 3.6 Examples (native, channel 2 selected with `DF D1`)
Computed from WG-F13 and WG-F30; hashes from WS1 (`/ch/1/fdr`, `/ch/1/mute`) and WS6 (the others).

| What | Bytes (hex) |
|---|---|
| discovery request (UDP 2222) | `57 49 4E 47 3F` |
| select the audio engine channel after connect | `DF D1` |
| read ch 1 fader | `D7 38AE75C2 DC` |
| reply / push: ch 1 fader at -inf (vendor `/*b` capture) | `D7 38AE75C2 D5 C3100000` |
| set ch 1 fader to -10.0 dB | `D7 38AE75C2 D5 C1200000` |
| set ch 1 mute on (1 = muted) | `D7 F50F69F8 01` |
| set ch 1 mute off | `D7 F50F69F8 00` |
| read the definitions of the root's children | `DA DD` |
| read `/$ctl/lib/$active` (hash `df6a06e2` contains `DF`, escaped) | `D7 DF DE 6A 06 E2 DC` |
| set ch 1 name to "Kick" (4-byte string: token 0x80 + 4 - 1 = 0x83) | `D7 C6AD548D 83 4B 69 63 6B` |
| set an EQ frequency of 1790.5 Hz (float bytes contain `DF D0`, escaped) | `D7 <hash> D5 44 DF DE D0 00` |

WS6 lists `/ch/1/name` as id -961719155 = `0xC6AD548D` and `/$ctl/lib/$active` as -546699550 = `0xDF6A06E2`. Every
hash in this file was checked against WS6's table; the driver's table test must pin these rows.

## 4. Value encodings
| ID | Fact | Source |
|---|---|---|
| WG-F60 | **Faders are written and read in dB** as float32 (`D5`) on the native side: the console sends `D5 C3100000` (-144.0) for a fader at -oo, WS6 and WS9 write `D5 <dB>`. -144 dB is the console's "-oo". Range -144..+10 dB, "-oo..10 in 1024 steps" for every fader and send level. mixcore maps any value <= -144 to -inf and writes -inf as -144.0. | WS1 p.30, p.48 `[VENDOR-DOC]`; WS6 `console.rs` `set_float` `[PERMISSIVE-CODE Apache-2.0]`; WS9 README (`setFloat(nodeId, -10.0)`) `[PERMISSIVE-CODE MIT]` |
| WG-F61 | **Raw fader position** (OSC triplet, native `D6`; mixcore never writes it): vendor examples -oo = 0.0, -3 dB = 0.675, -2 = 0.700, -1 = 0.725, 0 = 0.750, +3 = 0.825, +4 = 0.850: all on dB = 40 x raw - 30, the top segment of the X32 law. Below -3 dB no point is published; whether the -60..-144 part matches X32's segments (X32's raw 0 is -90 dB, WING's is -144) is `[TO-VERIFY]` (item 9). Writes of -3.0 and "-2" read back as exactly -3.0 and -2.0 with raw 0.675 and 0.700, which are not on a k/1023 grid, so the "1024 steps" are not X32-style raw quantization; what rounding the console applies to dB writes is `[TO-VERIFY]` (item 10). WS7 uses the X32 four-segment law only to interpolate its own fades. | WS1 p.21-24 `[VENDOR-DOC]`; WS7 `transitions.ts` `[PERMISSIVE-CODE MIT]` |
| WG-F62 | **Headamp gain law contradiction.** WS1 lists `/io/in/LCL/N/g` and AES50 `g` as lin -3..+45.5 dB, 98 steps (0.5 dB), but on the channel view `$g` as "-2.5..45 20 steps (LCL)" and "-3.0..45.5 98 steps (AES)". The console's own definition in WS6 (Compact, 3.0.5): `/io/in/LCL/1/g` lin **-2.5..+45 dB, 20 steps (2.5 dB)**, `/io/in/A/1/g` -3..+45.5, 98 steps; `/ch/1/in/set/$g` -3..+45.5, 98 steps. WS8 (3.1): "LCL gain step size is 2.5 dB (matches UI), not 0.5 dB as docs suggest". WS2 still prints -3..45.5 for LCL. mixcore takes the law from the definition the console returns at connect (section 3.0); on the full-size WING (other preamps, WG-F03) the local law is `[TO-VERIFY]` (item 14). | WS1 p.40, p.47 `[VENDOR-DOC]`; WS2 `[VENDOR-DOC]`; WS6 `propmap.jsonl` `[PERMISSIVE-CODE Apache-2.0]`; WS8 `utils.py` `wing_headamp_gain_db_to_float`, docstring item 5 `[PERMISSIVE-CODE MIT]` |
| WG-F63 | **Linear law** (node type lin float, min, max, steps): value = min + (max - min) k / (steps - 1), k = 0..steps-1; raw = k / (steps - 1). Steps follow from the tables: trim 0.1 dB, EQ gain 0.1 dB, pan 1, width 5 %, gate threshold 0.5 dB, compressor threshold 0.5 dB, compressor gain 0.5 dB, attack 1 ms. | WS1 p.47-50 (ranges and steps) `[VENDOR-DOC]`; formula: derived, consistent with WG-F64 checks |
| WG-F64 | **Log law** (log float): value = min x (max / min)^(k / (steps - 1)). Checked against the console's own defaults in the vendor node dump of `/ch/1`: `lcf=100.2` (20..2000, 641 steps, k = 224 gives 100.24), `eq lf=80.2` (k = 193: 80.18), `1f=200.0` (20..20000, 961, k = 320: 200.0), `2f=601.4` (k = 473), `3f=1k50` (k = 600: 1499.8), `4f=3k99` (k = 736: 3990.5), `hf=12k00` (50..20000, 833, k = 761: 11994), `hcf=10k02` (k = 736: 10018), gate `rel=199` (4..4000, 130, k = 73: 199.4), dyn `rel=153` (k = 68: 152.6), Q `1.00` (0.442..10, 181, k = 47: 0.998). | WS1 p.25 (dump), p.47-50 (ranges) `[VENDOR-DOC]`; WS6 (`minfloat` 0.442 for Q) `[PERMISSIVE-CODE Apache-2.0]`; arithmetic ours |
| WG-F65 | **Enums** may be written as string (`D1`/`80..BF`) or as index; ints 0..63 travel as one token byte, larger as `D3`/`D4`. `ratio` of the COMP model is a float enum: write one of the listed floats. Colours are written 1-based (WS8 reads the 0-based int of the OSC triplet and writes `idx + 1`). | WS1 p.22-23, p.95 `[VENDOR-DOC]`; WS6 `set_int`, `propmap.jsonl` `[PERMISSIVE-CODE Apache-2.0]`; WS8 `utils.py` `[PERMISSIVE-CODE MIT]` |

Reference points (the driver's table tests must reproduce them; float32 big-endian):

| Fader dB | `D5` bytes | raw (OSC, vendor) | LCL gain dB (k of 19) | `D5` bytes | AES gain dB (k of 97) | `D5` bytes |
|---|---|---|---|---|---|---|
| -inf (-144) | `C3100000` | 0.000 | -2.5 (k 0, min) | `C0200000` | -3.0 (k 0, min) | `C0400000` |
| -60 | `C2700000` | `[TO-VERIFY]` | 0.0 (k 1) | `00000000` | 0.0 (k 6) | `00000000` |
| -30 | `C1F00000` | `[TO-VERIFY]` | +10.0 (k 5) | `41200000` | +10.0 (k 26) | `41200000` |
| -10 | `C1200000` | `[TO-VERIFY]` (0.5 if the X32 top segment continues) | +20.0 (k 9) | `41A00000` | +20.0 (k 46) | `41A00000` |
| -3 | `C0400000` | 0.675 | +30.0 (k 13) | `41F00000` | +30.0 (k 66) | `41F00000` |
| 0 | `00000000` | 0.750 | +45.0 (k 19, max) | `42340000` | +45.0 (k 96) | `42340000` |
| +3 | `40400000` | 0.825 | | | +45.5 (k 97, max) | `42360000` |
| +10 (max) | `41200000` | 1.000 `[TO-VERIFY]` | | | | |

Model consequences: the core's Gain bounds come from the law of the source's headamp kind (2.5 dB per step on LCL means
a "+3 dB" gesture cap is one step and a "6 dB" bound is two); the driver always sends a grid value and matches echoes
within half a step. As for X32 and X Air, the wire maximum of gain is not written until item 14 shows it is safe.

## 5. Echo, push and client limits (summary)
| Question | X32 (baseline) | WING native (TCP 2222) | WING OSC (UDP 2223) |
|---|---|---|---|
| Changes pushed | `/xremote`, 10 s, renew | every open connection, no subscribe (WG-F20) | `/*S`/`/*s`/`/*b`, 10 s, **one subscriber, last requester wins** (WG-F21) |
| Pushes from other clients | yes | `[TO-VERIFY]` (item 5) | yes to the one subscriber `[TO-VERIFY]` |
| Our own write pushed back | no (X32-F23) | `[TO-VERIFY]` (WG-F24, item 4) | no (WG-F23) |
| Derived values pushed | n/a | `$fdr`, `$mute` after a fader or mute change (WG-F24) | same in `/*s` example |
| Client limit | 4 push clients | 16 TCP connections, 24 from 3.1; extra connections rejected (WG-F22) | unlimited senders, 1 subscriber |
| N+1st client | reads and writes, no pushes, no signal | connection refused, exact form `[TO-VERIFY]` (item 7) | takes the subscription; the previous subscriber silently stops receiving `[TO-VERIFY]` (item 6) |
| Liveness | `/xinfo` probe | TCP state + data request probe (2.4) | `/?` or any read |
| Remote lock | none | `/$syscfg/tcplock` (WG-F26) | `/$ctl/OSC/ronly` (WG-F27) |

Product consequence (for the lead): one WING serves at most 16 (24) TCP connections in total, shared with WING Edit,
CoPilot, WING-Q and other apps, each of which may use several (WS1 p.16). mixcore uses one per device; a hub (MixLink)
serving many musician phones uses one for all of them. The UI should explain a refused connection as "the console has
no free remote slots" (item 7 tells how to recognise it).

## 6. Meters
| ID | Fact | Source |
|---|---|---|
| WG-F50 | **Request (TCP, channel `DF D3`):** `D3 <u16 UDP port>` ("set at least once"), `D4 <u32 report id>`, then `DC`, a list of type bytes each followed by one or more index bytes, and `DE`. Types: `A0` channel 1..40, `A1` aux 1..8, `A2` bus 1..16, `A3` main 1..4, `A4` matrix 1..8, `A5` dca 1..8 (sic, WG-F55), `A6` fx 1..16, `A7` source device 1..16, `A8` output device 1..11, `A9` monitor (no index), `AA` RTA (no index), `AB`..`AF` "V2" channel, aux, bus, main, matrix. Index bytes `00`..`7F` = 1..128 (0-based on the wire). Vendor example `DC A0 00 01 08 A6 04 DE` = channels 1, 2, 9 and fx 5. Switch back with `DF D1` afterwards (vendor and WS6 do). | WS1 p.97-98 `[VENDOR-DOC]`; WS6 `console.rs` `request_meter`, `tools/wingmeters.rs` (indexes 0..15 for channels 1..16) `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F51 | **Delivery:** "The data is sent back to the client IP to the specified port", "every approximately every 50ms", for **5 s** ("Meter data times out after 5 seconds"). Renew by sending `D4 <same report id>` on channel `DF D3`. Vendor sequence: `DFD3 D33737 DFD1` (port 0x3737), `DFD3 D400000002 DCA001DE DFD1` (id 2, channel 2), `DFD3 D400000002 DFD1` (renew). WS6 renews every 3 s. | WS1 p.97-98, p.123 `[VENDOR-DOC]`; WS6 `console.rs` `_keep_alive_meters` `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F52 | **Packet:** one UDP datagram = 4-byte report id, then signed 16-bit **big-endian** words, the requested blocks in request order. Levels in **1/256 dB** (dB = int16 / 256). Vendor example (channel 1, fader +3 dB, 1 kHz oscillator at -6 dB): `00000002 f9fb f9fb fcfb fcfb f9fb 0000 ee01 0000` = in -6, -6, out -3, -3, gate key -6, gate gain 0, dyn key -17, dyn gain 0. With no input the inputs read `9bb2` (-100.3 dB) and outputs `8000` (-128). | WS1 p.97, p.124-125 `[VENDOR-DOC]` |
| WG-F53 | **Block layouts:** channel, aux, bus, main, matrix = 8 words: input L, input R, output L, output R, gate key, gate gain, dyn key, dyn gain. The example shows output = input + fader (post-fader). V2 blocks = 11 words: the same plus gate LED (0/1), dyn state, automix gain, in the order input L, input R, output L, output R, gate key, gate gain, gate LED, dyn key, dyn gain, dyn state, automix gain. DCA = 4 words: pre-fader L, R, post-fader L, R. FX = 4 levels + 6 state meters, "value in dB = return value * 6.0 / 2048". Source/output device = the group's levels (e.g. 8 for local ins). Monitor = 6 words (solo L/R, mon 1 L/R, mon 2 L/R). RTA = 120 words. Gain-reduction words: "For most of them 1.0 (256) maps to 20 dB gain reduction. Standard Wing gate is 60 dB." | WS1 p.97-98, p.123-125 `[VENDOR-DOC]` |
| WG-F54 | WS6 builds the report id as (sequence << 16) or port and reads only the first 2 bytes back; any 32-bit id works per WS1. The console sends meters to the IP of the TCP connection that asked (our reading of "client IP") `[TO-VERIFY]` (item 16). | WS6 `console.rs` `[PERMISSIVE-CODE Apache-2.0]`; WS1 p.97 `[VENDOR-DOC]` |
| WG-F55 | WS1's meter tables allow DCA 1..8 only (token table and wapi bit map) although there are 16 DCAs `[TO-VERIFY]` (item 16). | WS1 p.97, p.123 `[VENDOR-DOC]` |

mixcore meter plan (design): request `A0 00..27` (40 channels) and `A3 00..03` (4 mains) in one report id; per channel
use input L/R (pre-fader, for gain staging) and per main output L/R; renew every 1.5 s (at most a third of the 5 s
lifetime, the X32 rule); drop a packet whose length is not 4 + 2 x 8 x 44 bytes. -32768 (`8000`) maps to -inf.
ОЦЕНКА of traffic: (40 + 4) blocks x 8 words x 2 bytes + 4 = 708 bytes per packet; at 20 packets/s = 14.2 KB/s, about
14.8 KB/s with UDP/IP headers (28 B each); 7-22 KB/s if the real rate is 10-30 packets/s. A phone that shows 16
channels can request 16 + 4 blocks (324 bytes, about 7 KB/s).

## 7. Scenes, shows, snapshots
| ID | Fact | Source |
|---|---|---|
| WG-F70 | A **show** references up to 1000 **scenes**; a scene is a snap, snippet, preset, audio clip or a combination. Only one show is active. Remote control works only "after having pressed on the 'OPEN SHOW' icon" (a show must be open). | WS1 p.88, p.233 `[VENDOR-DOC]` |
| WG-F71 | `/$ctl/lib/$scenes` (read-only list; a plain read returns only the first name, the full list needs the node definition request), `$actidx` (read-only, index of the loaded scene, 0 = none), `$active` (read-only path, e.g. `I:SHOW2/scene_1.snap`), `$actshow` (read-only), `$action` (IDLE, GOPREV, GONEXT, GO, PREV, NEXT, GOTAG), `$actionidx` (0..16384). Recall = set `$actionidx` then `$action` = `GO` (or `GOTAG` with a scene tag number). WS8 recalls the same way. | WS1 p.88 `[VENDOR-DOC]`; WS8 (`cmd_scene_load`, `cmd_scene_execute`) `[PERMISSIVE-CODE MIT]` |
| WG-F72 | No remote command to **store** or update a scene is documented (searched WS1 chapters on OSC, native, `$ctl`). `/$ctl/$globals/cf_load` and `cf_upd` ("Confirm Snapshot Load/Update") exist; whether a remote GO waits for a confirmation on the console screen when `cf_load` = 1 is `[TO-VERIFY]` (item 17). | WS1 p.89 `[VENDOR-DOC]` |
| WG-F73 | Scene loads respect **scopes** stored with the snap and **global safes** (`/$ctl/safes`, section 3.5); RESTORE and console INITIALIZE ignore safes; from 3.1 initialization overwrites safe-protected parameters. Not saved in snapshots: monitor DIM/MONO, talk buttons, console name, clock, network, "Remote Lock (OSC / TCP)", HA remote. | WS1 p.237-242 `[VENDOR-DOC]`; WS3 `[VENDOR-DOC]` |
| WG-F74 | Whether a scene load pushes `$actidx`/`$active` to TCP clients, and how long the burst of parameter pushes lasts, is `[TO-VERIFY]` (item 17). | none |

mixcore recall heuristic (design): the X32 burst rule (at least 8 different parameters pushed within 250 ms) plus a
push of `/$ctl/lib/$actidx` or `$active` as a strong marker if item 17 shows they are pushed (then confidence `high`
for burst + marker, `medium` for the marker alone). A recall from mixcore needs the user's confirmation (invariant) and
is the two writes of WG-F71; it is offered only while `$actshow` is not empty.

## 8. Network notes
| ID | Fact | Source |
|---|---|---|
| WG-F80 | Two 1 Gbit/s Ethernet ports, "internally switched"; "A pair of Ethernet ports allow a network to be set up via router for wired or wireless control". No built-in Wi-Fi access point on any model (none in the specifications). IP mode DHCP or static (`/$syscfg/ipmode`, never written by mixcore). | WS5 (specifications, rear panel) `[VENDOR-DOC]`; WS1 p.39 `[VENDOR-DOC]` |
| WG-F81 | Burst reads over UDP get lost on slow links: WS10 #169 (Compact, 3.1) lost "the second half of the ~5,000 messages" of a Companion startup read; the maintainers propose pacing. Native TCP avoids this. | WS10 `[COMMUNITY]` |

## 9. What the vendor apps do
| ID | Fact | Source |
|---|---|---|
| WG-F90 | Vendor apps: WING Edit (desktop), WING CoPilot (tablet/phone), WING-Q (personal monitor). A community post says the native TCP interface "is what's used by many apps such as WEdit or MS" (Mixing Station). WS6's author says the native protocol is "used by all official Behringer Wing apps". Neither is a vendor statement `[TO-VERIFY]` (item 18). | WS13 (behringer.world p=12397, snippet) `[COMMUNITY]`; WS6 `ARCHITECTURE.md` `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F91 | App and firmware versions are coupled: "make sure your WING CoPilot app is updated to match your firmware version"; "Older firmware will throw connection errors with newer app versions". mixcore must not depend on a version check; it relies on the schema (section 3.0). | WS12 `[COMMUNITY]` |
| WG-F92 | No public capture of WING Edit or CoPilot traffic was found. What they send at connect (schema walk or table, data requests, meter blocks, keep-alive interval, number of TCP connections) is unknown; item 18 is the capture procedure. | searched GitHub, WS10, WS11, WS13 |

## 10. Known quirks and failure modes
| ID | Quirk | Impact on the driver | Source |
|---|---|---|---|
| WG-F100 | One OSC subscription per console, last requester wins. | Never subscribe over OSC; tell users that Companion or show tools on OSC are not affected by mixcore. | WS1 p.30 `[VENDOR-DOC]` |
| WG-F101 | Dynamic parameter hashes change meaning with `mdl` (WG-F34). | Read `mdl` before slot values; re-read slot definitions and values on every `mdl` push; refuse a slot write whose model changed since the gesture began. | WS1, WS6 |
| WG-F102 | Schema differs by firmware and model (WG-F35); 3.1 adds colours, aux dynamics, 24 clients. | Schema check at connect; absent stays absent. | WS2, WS3, WS6 |
| WG-F103 | WS6 escapes `0xDF` only inside hashes, not inside float or string payloads. Some legal values contain it: 1790.5 Hz is `44 DF D0 00`, and an unescaped `DF D0` is read as "switch to channel 0" (WG-F13). | Escape every `0xDF` byte of every payload as `DF DE` (WG-F13); fuzz the encoder against the decoder. | WS6 `console.rs` `set_float`, `set_string` (comment "do we need this escaping?") `[PERMISSIVE-CODE Apache-2.0]` |
| WG-F104 | Channel numbering of the mux is inconsistent in WS1 (WG-F14). | Use the bytes `DF D1`, `DF D3`. | WS1 |
| WG-F105 | `/$ctl/$globals/$savenow` "should not be used as part of a program loop as it will eventually wear the flash memory". | Never sent. | WS1 p.89 `[VENDOR-DOC]` |
| WG-F106 | The real-time clock loses time after about two weeks without mains power. | `/$stat/time` is not a time reference. | WS1 p.13 `[VENDOR-DOC]` |
| WG-F107 | `$name`, `$col`, `$icon` are read-only effective values; writes go to `name`, `col`, `icon`. A channel follows its source's name unless renamed. | Read `$`, write plain (WS8). | WS1 p.48 `[VENDOR-DOC]`; WS8 item 1 `[PERMISSIVE-CODE MIT]` |
| WG-F108 | WING Rack: no faders, so all changes come from its screen, apps or MIDI/GPIO; Compact: faders and main fader assignable (3.1). | No behaviour difference in the driver; recall heuristic must not assume a motor-fader surface. | WS1 p.11-12 `[VENDOR-DOC]`; WS3 |
| WG-F109 | A user reported name and colour writes not working on a Rack (3.1) through Companion; not reproduced on Full and Compact. | Read back every name/colour write (2.4 echo rule covers it). | WS10 #212 `[COMMUNITY]` `[TO-VERIFY]` |
| WG-F110 | Snapshots saved on one WING model keep USER layers and custom controls that the other models ignore. | Irrelevant to the driver; no scene file handling. | WS1 p.234 `[VENDOR-DOC]` |
| WG-F111 | Two consoles: Companion could not open a second WING connection because of a module bug, fixed in 2.3.0. | No protocol limit on several consoles per client. | WS10 #213 `[COMMUNITY]` |

## 11. Can `driver/internal/xosc` serve WING? (recommendation)
**No. WING needs its own driver, `driver/wing`, with its own codec package.** `xosc` is built around facts that do not
hold for WING:

| `xosc` assumption | WING |
|---|---|
| UDP, two sockets, client identity by source port (`conn.go`, `link.go`) | TCP stream with a 14-channel mux and an escape byte (WG-F13) |
| `/xinfo` identity and liveness, `/xremote` per-client 10 s subscription, 4/8 push slots (`table.go:16-18`) | `WING?` discovery, no subscribe on TCP, 16/24 connections; OSC has one subscriber in total |
| OSC addresses with two-digit indices, `mix/fader`, `mix/on` (1 = on) (`table.go` `Layout`) | hashes; `/ch/1/fdr`, `mute` (1 = muted) |
| values 0..1 with X32 laws (`convert.go`) | engineering units on the wire (WG-F60..F65), laws from the console's own definitions |
| meter blobs over OSC, LE floats or int16 (`sink.go`) | separate UDP stream, BE int16 1/256 dB, report ids (WG-F50..F53) |
| lost UDP pushes, 15 s re-read, client-limit inference from missing echoes (`reread.go`, `limit.go`) | reliable TCP; the limit is an explicit refusal |

Reusable without change: `driver` (interface, `Options`, `Origin`, events), `model`, `meters` (slot and frame
publishing), `internal/backoff`, `internal/safe`, `internal/testkit`. Worth extracting from `xosc` into a shared package
when `driver/wing` is written (the core-engineer decides): the echo-window bookkeeping (`session.go` `matchEcho`,
32 values per parameter, 2 s) and the scene burst detector (`scene.go`), both protocol-neutral.

Proposed layout: `driver/wing/internal/wingproto` (mux framing and escaping, token encoder/decoder, node definition
parser, fuzz-tested), `driver/wing` (session, schema check, table, laws, meters, scene heuristic), a generated
`driver/wing/schema` table, and `cmd/mixcapture` support (below).

Contract and API follow-ups (contract first): add family `wing` (discovery port 2222, UDP probe `WING?`) to
`contracts/core-api/*.schema.json` and the meter frame spec; add a TCP dial hook next to `ListenPacket` in
`driver.Options`; the model needs four mains, 16 DCAs, 8 named mute groups, per-send `pon` and the `GRP` tap.

## 12. What `sim/wing` needs
- **Discovery:** UDP 2222 answers `WING?` with `WING,<ip>,<name>,<model>,<serial>,<fw>`; option for the 3.1 model
  string (item 1). Optional OSC `/?` on 2223 (address `/?` or `/*` option, WG-F12).
- **TCP 2222 server:** mux framing with `DF Dn` and `DF DE` (WG-F13, both directions, fuzz tests); max connections 16
  or 24 (option), the extra one refused in a configurable way (RST, accept-and-close; item 7); a 10 s inactivity timeout
  per connection (WG-F25).
- **Tokens:** `D7` select, `DA`/`DB` navigation, `DC` data request on a leaf and on a node (all leaves, then `DE`:
  option until item 3), `DD` definitions in the 3.x format (WG-F33), every value token of WG-F30 on input, the console's
  own choice on output (`D5` for floats, inline/`D3`/`D4` ints, strings).
- **Tree fixture:** ch 1..40, aux 1..8, bus 1..16, main 1..4, mtx 1..8, dca 1..16, mgrp 1..8, `/io/in/LCL|A|B|C`,
  `/$syscfg` (`$firmware`, `$cnsmdl`, `tcplock`), `/$stat/time`, `/$ctl/lib`, `/cfg/dcamgrp`, with real hashes (from our
  own introspection; WS6's table only with its notice) and EQ/dynamics slots for two models so WG-F34 is testable.
- **Laws:** quantize lin and log floats to their steps (WG-F63, WG-F64); fader dB kept as written (option: round to a
  grid once item 10 is known); LCL gain 2.5 dB or 0.5 dB (option, item 14).
- **Push rules (options for every unknown):** a desk change is pushed to all connections with the derived `$fdr`/`$mute`;
  `EchoToWriter` (item 4), `PushOtherClients` (item 5), `TcpLockIgnoresWrites` / `TcpLockAnswersOld` (item 8).
- **Meters:** channel `DF D3` requests, UDP to the declared port, 50 ms period, 5 s lifetime renewed by `D4`, 8-word
  and V2 11-word blocks, fixture levels (WG-F50..F53).
- **Scenes:** `$actionidx` + `$action GO` apply a fixture scene as a burst of pushes; option to push `$actidx`/`$active`
  (item 17).
- **Faults:** drop the TCP connection, stall it (no RST), reboot (all connections and meter subscriptions gone),
  half-open client slot held until the 10 s timeout.
- Tests need a stream network: `internal/memnet` is UDP-only today; extend it or use loopback TCP in tests.

## 13. To verify on hardware (exact tests)
Setup for all items: a WING (any model; note which), firmware recorded from `/$syscfg/$firmware`, on a wired switch with
a Mac; amplifiers off or mains down; a lab show and lab snapshot slots only. Captures go to
`~/Desktop/dybyskz-mix/research/captures/wing/<date>/<item>/` (never into mixcore). Recording: the simplest setup is
WING Edit running **on the capturing Mac**, wired to the console, captured on the Mac's own Ethernet interface:
`mixcapture live -i <en> -console <ip> -out ...`; for CoPilot on a tablet use setup A or C of `docs/tools/mixcapture.md`. The relay (setup B) is **not suitable**:
meters go by UDP to a port the app picks (WG-F50), which the relay does not forward. mixcapture's `binary` decoder cannot
key WING's stream (no length prefix); add a `wing` decoder (`DF` mux, `D7 <hash>` key, value token) and a read-only
`mixcapture introspect -family wing` (sends only `DA`, `D7 <hash> DD`, `D7 <hash> DC`; never a value token, `D8`, `D9`)
before the session (core-engineer). Until then a pcap is decoded offline. Probe writes below are made by a person in
WING Edit or on the console unless an item says "probe", which means a one-off read/write script on a muted lab channel.

1. **Identity strings (WG-F04, WG-F09, WG-F11, WG-F12).** Send `WING?` to the console's IP and to the broadcast address
   on UDP 2222; send OSC `/?` to UDP 2223. Record both replies byte for byte, the reply addresses (`/?` or `/*`), and
   `/$syscfg/$cnsmdl`, `$cnscfg`, `$firmware`. Pass: exact model and firmware strings per model and firmware (3.0.6 and
   3.1.x if possible).
2. **Strip and send counts per model (WG-F02, WG-F03, WG-F35, 3.3 footnote).** Introspect (`DD`) `/ch/1/main`,
   `/aux/1/in/set`, `/io/in/LCL`, `/dca/1/name`, `/mgrp/1/name`, `/aux/1/dyn`. Pass: the counts, the `$g` presence on
   aux, the name lengths, and whether a Rack has 8 main sends.
3. **Node data request (WG-F31).** Probe: `D7 <hash of /ch/1> DC`; log every token until silence for 1 s. Pass: the
   reply is `D7 <hash> <value>` per leaf; whether it ends with `DE`; its byte size and duration; same for `/ch` (40
   strips).
4. **Echo of our own writes (WG-F24).** Probe with two TCP connections A and B. From A write `/ch/40/fdr` -20.0 dB
   (ch 40 muted, no source). Log what A and B receive within 500 ms. Repeat for `/ch/40/mute` 1 then 0 and for
   `/ch/40/name`. Then write the value the console already has. Pass: table of echoes on A and B (decides H1/H2 of 2.4
   and `EchoToWriter`).
5. **Pushes from other clients (WG-F20).** Connection A idle; a person moves ch 40 fader in WING Edit, then in CoPilot,
   then on the surface (not Rack); an OSC client sets `/ch/40/fdr ,f -30`. Pass: which of the four reach A, and the
   delay.
6. **OSC subscription stealing (WG-F21, WG-F28).** OSC client X sends `/*S` every 5 s; client Y sends `/*S` once. Move a
   fader at the console; log X and Y for 15 s. Then let X write a gain while Y holds the subscription. Pass: X stops
   receiving (and when it gets them back), no message tells X; whether X's write applies (WS8's claim).
7. **Client limit and N+1 (WG-F22).** Probe opens TCP connections to 2222 one by one, each sending `DF D1` and a data
   request every 2 s, until one fails; record the failure (RST, timeout, accept-then-close), the count, and whether WING
   Edit and CoPilot (already connected) were counted. Then kill one connection without FIN (pull the Mac's cable for
   15 s) and measure how long its slot stays taken. Pass: 16 or 24 per firmware, failure form, slot release time.
8. **TCP remote lock (WG-F26).** A person sets Setup > Remote > Remote Lock TCP on. Probe writes `/ch/40/fdr` -25.0 and
   reads it back; log any pushed value or error. Turn it off. Pass: write ignored or applied; what is sent back;
   whether `tcplock` changes are pushed.
9. **Raw fader law (WG-F61).** Probe writes `/ch/40/fdr` (muted) to -144, -90, -80, -70, -60, -50, -40, -30, -20, -10,
   -5, 0, +5, +10 dB over TCP and after each reads OSC `/ch/40/fdr` (UDP 2223, no subscription). Pass: the raw values;
   fit the segments with `mixcapture law`.
10. **dB rounding (WG-F61).** Probe writes -3.33, -0.07, -47.71, +9.99 dB and reads back over TCP and OSC. Pass: stored
    values (unchanged, rounded to 0.1 dB, or to a 1024-point grid).
11. **DCA and mute group membership (3.2).** In WING Edit assign ch 40 to DCA 3 and DCA 12, and (where the UI offers
    it) to mute group 2 on the channel and on its source; harvest before and after (`:s before`, `:s after`). Pass: the
    exact `tags` strings on `/ch/40/tags` and `/io/in/<grp>/<n>/tags`, the separator, and any other changed address.
12. **Mute semantics (WG-F27 area, 3.2).** Ch 40 member of DCA 3 and mute group 2. Log `/ch/40/mute`, `$mute`,
    `$muteovr`, `$fdr` while (a) muting the channel, (b) engaging mute group 2, (c) muting DCA 3 with `/cfg/dcamgrp` 0
    and 1, (d) moving DCA 3's fader. Pass: what `$mute` 0, 1, 2 mean; whether the group rewrites the channel's `mute`.
13. **Send taps (3.3).** On ch 40 send 9 (default `GRP`), log what changes when a person switches bus 9 between pre and
    post in the bus setup, and what `pon` does when the channel is muted (listen on bus 9 with a tone source). Pass:
    meaning of `GRP` and `pon`.
14. **Gain law and channel gain (WG-F62).** Introspect `/io/in/LCL/1/g` and `/ch/1/in/set/$g` with ch 1 on LCL 1, then
    with ch 1 on A 1. With nothing plugged into LCL 1, phantom off, ch 1 muted: probe writes `/io/in/LCL/1/g` 0.0, 1.0,
    +44.9 and reads back; writes `/ch/1/in/set/$g` +10.0 and reads `/io/in/LCL/1/g`. Pass: law per model (2.5 or
    0.5 dB), rounding, whether `$g` is writable. Never write the maximum.
15. **AES50 ownership (3.4).** Two consoles share one stage box (an X32 or S16), HA remote on one side. From a mixcore
    probe on the non-owner write one gain step down on `/io/in/A/1/g`; log both consoles and the box. Pass: ignored,
    applied or refused.
16. **Meters (WG-F50..F55).** Probe requests `A0 00..27`, `A3 00..03`, `A5 00..0F` with report id 7 to its UDP port;
    log intervals and the time of the last packet without renewal; send a 1 kHz tone at -18 dBFS into ch 1 and pull
    ch 1's fader to -inf. Pass: period (expect 50 ms), lifetime (expect 5 s), block sizes, DCA 9..16 accepted or not,
    pre/post taps, destination IP when the request came over a routed path.
17. **Scene recall (WG-F70..F74).** Open a lab show with scenes 1 and 2 differing in 3 faders and 1 name. Load scene 2
    from the console, then from WING Edit, then by probe (`$actionidx` 2, `$action` GO) with `cf_load` 0 and 1. Log
    every push for 3 s. Pass: burst size and duration, whether `$actidx`/`$active` are pushed, confirmation dialog.
18. **Vendor app traffic (WG-F90..F92).** Capture WING Edit connecting, syncing, opening the meters page, idling 60 s,
    loading a scene, quitting; the same for CoPilot (mirror port). Pass: protocol(s), number of TCP connections,
    keep-alive interval, meter blocks requested, whether they walk the schema.
19. **Liveness on a quiet console (2.4).** Session idle 10 min with the 1 s probe. Pass: no stale event, no disconnect.
20. **Reboot (fault).** Power-cycle with a session and meters running; log the last and first packets; compare a full
    read before and after. Pass: connections and meter subscriptions are gone; `/$globals/startmute` effect.

`cmd/mixprobe --family wing` (to be written) can automate items 1-4, 7 (without the cable pull), 9, 10, 14 (reads and
the 0 dB write), 16, 19 and the probe part of 17; items 5, 6, 8, 11-13, 15, 18 and 20 need a person, a second console or
a tone source.

## 14. Implementation status (driver/wing, sim/wing; 2026-10-13)
Both are written from this file; none of the rows below is a new protocol fact, they are the design choices the code
makes where this file leaves a gap. Family status: in development until the hardware session of section 13.

| Area | What the code does | Facts |
|---|---|---|
| Hashes | No hash table ships. At the first connect of a link the driver walks the nodes it models with `DA DD` and `D7 <hash> DD` only and maps paths to hashes from the definitions' names; the walk is cached per link and repeated only when `$firmware` changes. Open question 5 is therefore moot for the driver; the simulator uses the eight hashes of this file and placeholders for the rest. | WG-F31..F33, WG-F35 |
| Definition answers | A definition belongs to the requested node whose hash is its parent; a level ends after one `DE` per request, or after 1 s of silence once every node that answered has its `DE`. Data requests are never in flight while a walk level runs (a `DE` after data, item 3). | WG-F30, WG-F31, WG-F33 |
| Undefined bytes | A token starting with `E0`..`FF` (not in WG-F30) ends the connection (stale, reconnect, full re-read) instead of a guessed length that could misread payload bytes as a hash and a value. The simulator ignores such bytes. | WG-F30 |
| Receive channel | Bytes before the console's first `DF Dn` count as data-channel bytes. | WG-F13, WG-F14 |
| Echo | Section 2.4 as written: echo window 2 s, read-back 150 ms after the last write, `EchoesOwnWrites` true. Fader values match within half of a 1024-step display position or 0.05 dB (item 10: a console that rounds to 0.1 dB). | WG-F24, WG-F60, WG-F61 |
| Fader law (UI) | The X32 four-segment law, which contains every raw point of WG-F61; it only draws the scale (the wire carries dB). | WG-F60, WG-F61 |
| Gain | Law from the console's definition of `/io/in/<grp>/<n>/g`; writable on `LCL` only, never the wire maximum; AES50 A and B read-only (open question 2 default); AES50 C not modelled yet. Local count: 24 when the model string contains `rack` or `compact`, else 8. | WG-F03, WG-F62, 3.4 |
| Membership | DCA membership from `#D1`..`#D16` in `tags`, any non-digit ends a tag (item 11); reported empty while `/cfg/dcamgrp` = 0. Mute-group membership of strips is not read (item 11). | 3.2 |
| Mains | `/main/1` is the model's main (open question 4 default); mains 2..4 are not modelled yet. | 3.1 |
| Remote lock | `/$syscfg/tcplock` = 1: every write is refused in the driver (`ErrUnsupported`). | WG-F26 |
| Scene recall | Burst rule only (low confidence); `$actidx` and `$active` are not used until item 17. | section 7 |
| Firmware | Firmware below 2.0 is not special-cased: its definitions lack the parent field (WG-F05), so the walk fails and the connect error says so; open question 1 stays open. | WG-F05, WG-F06 |

## Open questions for the lead (with recommended defaults)
1. Firmware below 2.0 (WG-F05, WG-F06): connect read-only with an "update the console" hint? Default: yes.
2. Gain on AES50 sources before item 15: Default: read-only (same rule as X32).
3. Model mapping of `Send.tap` `GRP` and `pon`: Default: show the console word, no pre/post claim, until item 13.
4. Four mains in the unified model: Default: main 1 is the "main" strip of v1; mains 2..4 appear as extra main strips.
5. Using WS6's Apache-2.0 `propmap.jsonl` in tests and the simulator fixture: Default: no; generate our own table from
   a read-only introspection of our console (item 2), and keep WS6 only as a cross-check.

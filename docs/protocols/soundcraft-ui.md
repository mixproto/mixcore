# Soundcraft Ui12 / Ui16 / Ui24R network control protocol (as mixcore will implement it)

Scope: Soundcraft Ui12, Ui16 and Ui24R digital mixers (compatibility statement only; mixcore is not affiliated with
or endorsed by any console maker). This file lists the protocol facts that `driver/ui` and `sim/ui` will implement.
The driver and the simulator are both written from this file, never from each other. When a real mixer disagrees with
this file, the mixer wins: record a `[CAPTURE]` fact here first, then change the simulator, then the driver.

The vendor publishes **no protocol document**. The mixer serves its own browser app, and that app talks to the mixer
over a text protocol on a WebSocket. Everything below about the wire comes from permissively licensed client libraries
that speak this protocol, from a state dump published with one of them, and from the vendor's user manual for
behaviour (limits, ranges, features). Research date: 2026-10-13. No Ui hardware was available: **no captures**.
Section 7 lists the exact tests.

Facts have IDs `UI-Fnn`. Baselines for shared design rules: `x32.md` (echo window, stale rules) and `xair.md`.

## Tags
Tag names as in `xair.md`; the task names are in brackets:
- `[VENDOR-DOC]` (= `[VENDOR]`, `[OFFICIAL]`): a document published by the manufacturer, or a formal specification.
- `[PERMISSIVE-CODE <licence>]` (= `[PERMISSIVE]`, `[CODE]`): behaviour read in MIT, BSD or Apache-2.0 code or data
  files. The licence is named at each use. Code is evidence of what working clients do, not of what firmware promises.
- `[COMMUNITY]`: forum, retailer page, review, search-engine snippet.
- `[TO-VERIFY]` (= `[UNVERIFIED]`): plausible, not confirmed by two independent sources, or sources contradict. Every
  such item is in section 7 and becomes a simulator option.
- `[CAPTURE]`: our own capture of hardware we use. **None yet.**
- "derived": arithmetic on tagged facts, shown in full; it inherits the weakest tag of its inputs.

Legal note (CLAUDE.md invariant 8): nothing here is copied from GPL code, vendor code or decompiled software. The
vendor's browser app (JavaScript served by the mixer) was **not** read; the laws below come from MIT/Apache code and
are to be confirmed by our own captures. GPL projects are listed under "Pointers not used".

## Sources (accessed 2026-10-13)
- **US1** Soundcraft (Harman), "Ui24R User Manual", User Guide v1.0, 121 pages. Read from the copy hosted at
  https://www.markertek.com/Attachments/Manuals/Soundcraft/5076585-Manual.pdf (vendor product page did not offer a
  direct link). Page numbers are PDF pages. Sections cited: features (p.4), specifications (p.9), 2.4 Getting
  connected, 3.0.1 Updates and requirements (p.19), 4.1 Gain page (p.49), 4.2.2 VCA groups, 6.1 View and mute
  groups, 8.0 Shows and snapshots, 10.0 Settings (Sync ID, Monitor access limit), 10.1.3 LAN, 10.1.4 Master
  password. `[VENDOR-DOC]`.
- **US2** fmalcher/soundcraft-ui (npm `soundcraft-ui-connection`), **MIT**, commit `fee842d` (2026-10-11):
  https://github.com/fmalcher/soundcraft-ui. Files: `packages/mixer-connection/src/lib/mixer-connection.ts`,
  `state/mixer-store.ts`, `state/state-selectors.ts`, `state/resource-lists.ts`, `facade/*.ts`,
  `utils/value-converters/value-converters.ts`, `utils/value-converters/benchmarks-calculations/db-lut.ts`,
  `vu/vu.utils.ts`, `vu/vu-test-messages.ts`, `device-capabilities.ts`, `utils/channel-sync-mapping.ts`, and
  `docs/docs/more/howitworks.md`, `docs/docs/more/stereolink.md`, `docs/docs/features/*.md`, `docs/docs/buses/*.md`.
  `[PERMISSIVE-CODE MIT]`.
- **US3** jskeet/DemoCode, directory `DigiMixer/DigiMixer.UiHttp`, **Apache-2.0**, commit `f885f88` (2026-09-03):
  https://github.com/jskeet/DemoCode/tree/main/DigiMixer/DigiMixer.UiHttp. Files: `UiHttpMixerApi.cs`,
  `UiMessage.cs`, `UiAddresses.cs`, `UiMeters.cs`, `UiStreamClient.cs`, and the data files
  `formatted-data-config.json.txt` (6 665-key state dump of a Ui24R), `formatted-net-config.json.txt`,
  `formatted-cur-setup.json.txt`, `parameters.txt`. `[PERMISSIVE-CODE Apache-2.0]`.
- **US4** jonathanslenders/ui24r-client, **BSD-3-Clause**, commit `1b01a68` (2022-09-02, stale: 4 years):
  https://github.com/jonathanslenders/ui24r-client (`src/ui24r_client/client.py`). `[PERMISSIVE-CODE BSD-3-Clause]`.
- **US5** bitfocus/companion-module-soundcraft-ui, **MIT**, commit `f1bb13b` (2026-09-09), depends on US2
  `^7.0.2`: https://github.com/bitfocus/companion-module-soundcraft-ui (`companion/HELP.md`, `src/actions.ts`).
  `[PERMISSIVE-CODE MIT]`.
- **US6** LearnBoost/socket.io-spec, "Socket.IO protocol" version 1 (the 0.9-era protocol), revision `13381ec`
  (2014-01-05), `README.md`: https://github.com/LearnBoost/socket.io-spec (no licence file; read for facts only).
  Formal specification, `[VENDOR-DOC]` in the sense of `xair.md` (a protocol spec, not the mixer vendor's).
- **US7** Retailer and review statements of the 10-device limit: Soundhouse product page
  https://www.soundhouse.co.jp/en/products/detail/item/248866/, Sound On Sound review
  https://www.soundonsound.com/node/4917128 (search-engine snippets). `[COMMUNITY]`.
- **US8** Ui12/Ui16 gain range, three disagreeing statements (search-engine snippets only): a Ui12/Ui16 manual copy
  at https://www.woodbrass.com/images/woodbrass/SOUNDCRAFT+CONSOLE+NUM+UI+12.PDF ("-30dB to 60dB in 0.1dB steps";
  the URL returned an HTML page to us), the IDJ spec page https://www.idjnow.com/soundcraft-ui16-mixer.html ("-40dB
  to +50dB"), a Harman help page https://harman.helpjuice.com/sc-ui-series/audio-input-gain-range (HTTP 404 on access;
  snippet "-40dB to +60dB"). `[COMMUNITY]` `[TO-VERIFY]`.
- **US9** Firmware history, forum and press snippets: Ui24R 3.0 (NAMM 2019, cascade, cue recall)
  https://www.soundtech.co.uk/harman-professional-solutions-releases-soundcraft-ui24r-30-firmw/ ; 3.3.8293 and
  3.4.8318 / 3.4.8320 (2022) https://proforums.harman.com/soundcraft/discussion/comment/199415 ,
  https://proforums.harman.com/soundcraft/discussion/comment/199329/ . `[COMMUNITY]`.
- **US10** Wi-Fi field reports: https://proforums.harman.com/soundcraft/discussion/comment/196329 ,
  https://proforums.harman.com/home/discussion/125020/ui24r (snippets). `[COMMUNITY]`.

Pointers not used: `stefets/osc-soundcraft-bridge` (GPL-3.0), the vendor's own web app JavaScript (vendor code, not
read), the Mixing Station documentation (no Ui page exists in its public docs repository).

## 1. Models, firmware, scope

| ID | Fact | Source |
|---|---|---|
| UI-F01 | Three models share one protocol. The model is a state key `model` with the values `ui12`, `ui16`, `ui24` (Ui24R reports `ui24`). | US2 `types.ts` (`MixerModel`), `facade/device-info.ts` `[PERMISSIVE-CODE MIT]`; US3 `formatted-net-config.json.txt` (`"model": "ui24"`), `UiAddresses.cs` (`Model = "model"`) `[PERMISSIVE-CODE Apache-2.0]` |
| UI-F02 | Firmware is a state key `firmware`, e.g. `3.4.8318-ui24` (version, build, `-` model). | US3 `formatted-net-config.json.txt`, `formatted-data-config.json.txt` `[PERMISSIVE-CODE Apache-2.0]` |
| UI-F03 | Strip counts per model (US2's capability table): Ui12 = 8 inputs, 2 line in, 2 player, 4 FX, 4 subgroups, 4 aux, 0 VCA; Ui16 = 12, 2, 2, 4, 4, 6, 0; Ui24 = 24, 2, 2, 4, 6, 10, 6. Multitrack and master dim only on Ui24. The Ui24R state dump has `i.0`..`i.23`, `s.0`..`s.5`, `a.0`..`a.9`, `v.0`..`v.5`, `f.0`..`f.3`, and 20 headamps `hw.0`..`hw.19` (`curSetup.phantom: 20`). **Contradiction:** US1's feature list says "4 x Subgroups, 6 x VCA's" for the Ui24R, while US2, US3 and the meter header (UI-F91) show 6 subgroups. mixcore reads counts from the state the mixer sends (UI-F20), never from the model string. | US2 `device-capabilities.ts` `[PERMISSIVE-CODE MIT]`; US3 data files `[PERMISSIVE-CODE Apache-2.0]`; US1 p.4 `[VENDOR-DOC]`; count conflict `[TO-VERIFY]` (item 3) |
| UI-F04 | On the Ui24R headamps (`hw.N`) are separate from input channels: each channel has a source key `i.N.src` (`hw.N` by default) and gain and phantom live on `hw.N`. On Ui12 and Ui16 gain and phantom live on the input channel `i.N`. | US2 `facade/hw-channel.ts`, `docs/docs/buses/hwchannel.md` `[PERMISSIVE-CODE MIT]`; US3 dump (`i.0.src = hw.0`) `[PERMISSIVE-CODE Apache-2.0]` |
| UI-F05 | Firmware history (Ui24R): 3.0 (2019; cascade, cue recall, UDP lighting messages), 3.3.8293, 3.4.8318 and 3.4.8320 (2022, media playback fixes). No Ui24R release after 2022 and no Ui12/Ui16 release notes were found. The libraries do not branch on firmware. | US9 `[COMMUNITY]`; US2 (no firmware checks) `[PERMISSIVE-CODE MIT]` |

Covered: Ui12, Ui16, Ui24R, firmware 3.x on Ui24R (the US3 dump is 3.4.8318); Ui12/Ui16 firmware versions unknown.
The driver never refuses a firmware version; it logs it and probes features from the state it receives.
Out of scope for driver v1: cascade (`settings.cascade.*`), the media player and recorders (`MEDIA_*`, `MTK_*`,
`RECTOGGLE`), AFS2 (`*.afs.*`), automix writes, DigiTech amp models, network and admin settings, `iosys*`, USB
patching writes, the HDMI output. They are listed in section 3.6 so the simulator can carry them as plain keys.

## 2. Transport, discovery, session, keep-alive

### Transport
| ID | Fact | Source |
|---|---|---|
| UI-F10 | The mixer's browser app runs from a web server inside the mixer (TCP 80) and talks to it over a WebSocket. Clients open `ws://<ip>/` (US2) or `ws://<ip>/socket.io/1/websocket` (US4); both are reported to work. Neither performs the Socket.IO HTTP handshake (`/socket.io/1/` returning a session id) that the Socket.IO spec describes. | US1 3.0.1 ("runs from a virtual web server in the main Ui unit") `[VENDOR-DOC]`; US2 `mixer-connection.ts:85` `[PERMISSIVE-CODE MIT]`; US4 `client.py:192` `[PERMISSIVE-CODE BSD-3-Clause]`; US6 (handshake) `[VENDOR-DOC]`; which path the vendor app uses `[TO-VERIFY]` (item 1) |
| UI-F11 | Framing is Socket.IO 0.9 style on top of WebSocket text frames: a data message is `3:::<payload>` (type 3 = message, empty id and endpoint). Clients send every command as `3:::<command>` and only use received frames that start with `3:::`. One frame may carry several commands separated by `\n`. | US2 `mixer-connection.ts:87,161-177` `[PERMISSIVE-CODE MIT]`; US4 `client.py:204,217-221` `[PERMISSIVE-CODE BSD-3-Clause]`; US6 section "(3) Message" `[VENDOR-DOC]` |
| UI-F12 | Other Socket.IO frames: the mixer sends `2::` (heartbeat); US4 echoes every `2::` back unchanged, US2 does not (the echo is commented out) and still works. US2's comments also show a `1::` (connect) frame from the mixer. Per the spec a heartbeat must reach the server within the negotiated timeout. | US4 `client.py:212-215` `[PERMISSIVE-CODE BSD-3-Clause]`; US2 `mixer-connection.ts:182-202` `[PERMISSIVE-CODE MIT]`; US6 "(2) Heartbeat" `[VENDOR-DOC]`; whether the mixer drops a client that never echoes `2::` `[TO-VERIFY]` (item 2) |
| UI-F13 | **Alternative raw transport:** open TCP 80, send `GET /raw HTTP1.1\n\n`, read and discard the HTTP response headers (up to `\r\n\r\n`), then exchange the same commands as plain `\n`-terminated ASCII lines without the `3:::` prefix. US3 uses this for both of its connections and reads at most 256 header bytes. | US3 `UiHttpMixerApi.cs:18,122-163`, `UiMessage.cs`, `UiStreamClient.cs` `[PERMISSIVE-CODE Apache-2.0]`; one source only `[TO-VERIFY]` (item 1) |
| UI-F14 | There is **no authentication** step in the control protocol: a client that opens the socket receives the full state and can write. The optional "master password" lock is described in UI-F86. | US2, US3, US4 (no login step) `[PERMISSIVE-CODE MIT, Apache-2.0, BSD-3-Clause]`; US1 10.1.4 `[VENDOR-DOC]` |

### Discovery and addressing
| ID | Fact | Source |
|---|---|---|
| UI-F15 | No discovery protocol is known: every library takes a manual IP. Defaults: hotspot (the mixer's own Wi-Fi) at **10.10.1.1** (URL `ui.io` resolves there on the hotspot), wired LAN **10.10.2.1/24**; LAN can be DHCP or manual; a LAN change needs a power cycle. Default hotspot SSID "Soundcraft Ui24", default key `scuiwlan` (US1 says the factory unit "will not have a password"; contradictory). | US1 2.4, 10.1.3 `[VENDOR-DOC]`; US3 `formatted-net-config.json.txt` (`eth_ip 10.10.2.1`, `ap_ssid "Soundcraft Ui24"`) `[PERMISSIVE-CODE Apache-2.0]`; whether the mixer announces itself (mDNS, SSDP, broadcast) `[TO-VERIFY]` (item 4) |
| UI-F16 | The page `/js/initparams.js` served by the mixer contains `curSetup`, the full `dataValue` state and `netConfig`; US3's copy of `netConfig` has an `adminPassword` field (redacted by US3) and Wi-Fi keys. **mixcore never fetches this file** (credentials; see "Safety and legal" below). | US3 `parameters.txt`, `formatted-net-config.json.txt` `[PERMISSIVE-CODE Apache-2.0]` |

### Session
| ID | Fact | Source |
|---|---|---|
| UI-F20 | Over the WebSocket the mixer sends its **entire state** right after the connection opens, as individual `SETD`/`SETS` lines, with no request. US2 considers the initial state complete when nothing new arrived for 25 ms, or after 250 ms at most. | US2 `docs/docs/more/howitworks.md`, `utils.ts` `waitForInitParams` `[PERMISSIVE-CODE MIT]` |
| UI-F21 | Over the raw transport US3 sends `INIT` (a bare line) to request all data and waits 500 ms ("Normally we get all the information in 100ms ... the lines are unordered, so we can't really spot the end"). | US3 `UiHttpMixerApi.cs:69-97,165-173` `[PERMISSIVE-CODE Apache-2.0]`; whether `INIT` is needed or accepted on the WebSocket `[TO-VERIFY]` (item 1) |
| UI-F22 | The full state is large: the US3 Ui24R dump has 6 665 keys. Shows, snapshot lists, cue lists and playlists are **not** part of it; they are sent per client on request (UI-F60). | US3 dump (key count computed by us) `[PERMISSIVE-CODE Apache-2.0]`; US2 `docs/docs/features/shows.md` `[PERMISSIVE-CODE MIT]` |

### Keep-alive and liveness
| ID | Fact | Source |
|---|---|---|
| UI-F25 | Clients send `ALIVE` (bare command, `3:::ALIVE` on the WebSocket) periodically: every **1 s** (US2, US4), every **3 s** on each of two connections (US3). What the mixer does when `ALIVE` stops (timeout, close) is not documented. | US2 `mixer-connection.ts:25-26,104-112` `[PERMISSIVE-CODE MIT]`; US4 `client.py:202-205` `[PERMISSIVE-CODE BSD-3-Clause]`; US3 `UiHttpMixerApi.cs:175-216` `[PERMISSIVE-CODE Apache-2.0]`; timeout `[TO-VERIFY]` (item 2) |
| UI-F26 | The mixer streams meter frames (`VU2`, UI-F90) continuously to every client, so a healthy connection is never quiet. mixcore uses this: any received frame proves the downlink; 1.5 s without any frame = stale (design rule, as X32-F25). Writes travel on TCP, so a dead uplink shows as a socket error or as missing echoes on the second connection (UI-F80). | US2 `vu/vu-processor.ts` (frames arrive without a request) `[PERMISSIVE-CODE MIT]`; meter rate and "no request needed" `[TO-VERIFY]` (item 9); thresholds are our design |
| UI-F27 | US2 reconnects 2 s after an error; US4 loops forever. Nothing indicates that a reconnect must wait for a server-side timeout. | US2 `mixer-connection.ts:23,154-160` `[PERMISSIVE-CODE MIT]`; US4 `client.py:191-200` `[PERMISSIVE-CODE BSD-3-Clause]` |

## 3. Message grammar and the address map

### 3.1 Grammar
Lines are ASCII, fields separated by `^`, terminated by `\n` on the raw transport (on the WebSocket each frame is
`3:::` plus one or more `\n`-separated lines).

| ID | Form | Meaning | Source |
|---|---|---|---|
| UI-F30 | `SETD^<path>^<number>` | set or report a numeric parameter. Booleans are `0`/`1`. The number is a decimal with `.` (US3 writes up to 17 decimals, US2 JavaScript's shortest form). | US2 `mixer-connection.ts:254-266`, `mixer-store.ts` `[PERMISSIVE-CODE MIT]`; US3 `UiMessage.cs:81-85` `[PERMISSIVE-CODE Apache-2.0]` |
| UI-F31 | `SETS^<path>^<text>` | set or report a string (names, patch sources). The value is everything after the second `^`. US2 strips `^`, cuts names to 20 characters and upper-cases them before sending. | US2 `mixer-connection.ts:274-276`, `mixer-store.ts`, `utils.ts` `sanitizeChannelName` `[PERMISSIVE-CODE MIT]`; max length and allowed characters `[TO-VERIFY]` (item 12) |
| UI-F32 | `VU2^<base64>` | meter frame (UI-F90). | US2 `vu/vu-processor.ts` `[PERMISSIVE-CODE MIT]` |
| UI-F33 | `RTA^...`, `VUA^...` | spectrum and other visualisation data; ignored by US3 and US4. Format unknown. | US3 `UiMessage.cs` `[PERMISSIVE-CODE Apache-2.0]`; US4 `client.py:240-242` `[PERMISSIVE-CODE BSD-3-Clause]` |
| UI-F34 | `BMSG^SYNC^<syncId>^<index>` | "Sync ID" selected-channel sharing between clients; `index` per model in US2 `channel-sync-mapping.ts` (Ui24: 0..23 inputs, 24-25 line, 26-27 player, 28-31 FX, 32-37 subs, 38-47 aux, 48-53 VCA, -1 master). UI only, no audio effect. | US2 `facade/channel-sync.ts`, `utils/channel-sync-mapping.ts` `[PERMISSIVE-CODE MIT]`; US1 10.0 "SYNC ID" `[VENDOR-DOC]` |
| UI-F35 | Bare commands: `ALIVE`, `INIT` (UI-F21, UI-F25); show control and lists (UI-F60); media (`MEDIA_PLAY`, `MTK_PLAY`, `RECTOGGLE`, ...; out of scope). | US2, US3 `[PERMISSIVE-CODE MIT, Apache-2.0]` |
| UI-F36 | There is **no read request** for a single parameter in any library: the state is pushed at connect and every change is pushed. A re-read means reconnecting (or `INIT` on the raw transport). | US2, US3, US4 (no such command) `[PERMISSIVE-CODE MIT, Apache-2.0, BSD-3-Clause]`; whether a read command exists `[TO-VERIFY]` (item 1) |

Paths are dotted: `<kind>.<index>[.<sub>...]`, indices **0-based** (input 1 = `i.0`). Kinds: `i` input, `l` line in,
`p` player, `f` FX, `s` subgroup, `a` aux (or matrix, UI-F45), `v` VCA, `m` master, `hw` headamp (Ui24R), `mg` mute
group, `vg` VCA group, `settings`, `var` (runtime), `automix`.

### 3.2 Strips (unified model, `mix-domain-knowledge` section 4)
All keys `[PERMISSIVE-CODE MIT]` from US2 selectors/facades and `[PERMISSIVE-CODE Apache-2.0]` from the US3 dump
unless marked otherwise.

| Unified strip | Root | Fader | Mute | Pan | Name | Other keys seen |
|---|---|---|---|---|---|---|
| input 1..N | `i.0`..`i.23` | `i.N.mix` | `i.N.mute` | `i.N.pan` | `i.N.name` | `color`, `solo`, `stereoIndex`, `mgmask`, `vca`, `subgroup`, `invert`, `delay`, `phantom`, `gain`, `src`, `safe`, `forceunmute`, `amix`, `amixgroup`, `mtkrec` |
| auxIn: line in L/R | `l.0`, `l.1` | `l.N.mix` | `l.N.mute` | `l.N.pan` | `l.N.name` | `gain`, `stereoIndex`, `src` (`li.0`) |
| auxIn: player L/R | `p.0`, `p.1` | `p.N.mix` | `p.N.mute` | `p.N.pan` | `p.N.name` | `stereoIndex` |
| fxReturn 1..4 | `f.0`..`f.3` | `f.N.mix` | `f.N.mute` | `f.N.pan` | `f.N.name` | `fxtype`, `par1`..`par6`, `bpm`, `smix`, `span` |
| bus: subgroup 1..6 | `s.0`..`s.5` | `s.N.mix` | `s.N.mute` | `s.N.pan` | `s.N.name` | `ducker`, `exclude` |
| bus: aux 1..10 | `a.0`..`a.9` | `a.N.mix` | `a.N.mute` | `a.N.pan` | `a.N.name` | `stereoIndex`, `delay`, `matrix`, `link2master`, `safe`, 31-band GEQ `a.N.eq.peak.0..30` |
| matrix (Ui24R) | an `a.N` slot with `a.N.matrix = 1` | `a.N.mix` | `a.N.mute` | `a.N.pan` | `a.N.name` | sources `a.N.mtx.M`, `s.N.mtx.M`, `m.mtx.M` (UI-F45) |
| dca: VCA 1..6 (Ui24R) | `v.0`..`v.5` | `v.N.mix` | `v.N.mute` | none | `v.N.name` | `solo`, `mgmask` |
| main LR | `m` | `m.mix` | **none** (no `m.mute` key in the dump) | `m.pan` | `m.name` (`MASTER`) | `dim`, `delayL`, `delayR`, `l.invert`, `r.invert`, GEQ `m.eq.peak.l.0..30` / `.r.` |
| muteGroup 1..6 | bit in `mgmask` (UI-F44) | n/a | `mgmask` bits 0..5 | n/a | `mg.N.name` | MUTE FX bit 22, MUTE ALL bit 23 |

| ID | Fact | Source |
|---|---|---|
| UI-F40 | Mute polarity: `mute` 1 = muted, 0 = not muted (the opposite of X32 `mix/on`). | US2 `selectMute`, `Channel.setMute` `[PERMISSIVE-CODE MIT]`; US3 `ReceiveMuteStatus(.., Value == "1")` `[PERMISSIVE-CODE Apache-2.0]` |
| UI-F41 | **Stereo links are done by the client.** `stereoIndex` is -1 (mono), 0 (left of a pair; partner is index+1) or 1 (right; partner is index-1) on `i`, `l`, `p`, `a`. The vendor app, and US2 to match it, writes `mix`, `mute`, `solo`, sends and `postproc` to **both** members of a linked pair; a linked aux pair also receives the mirrored send. Pan of a send is mirrored only across the linked aux, not across the linked channel. The mixer is not documented to mirror anything itself. | US2 `docs/docs/more/stereolink.md` ("This behavior matches the way the original web app handles stereo-linking"), `utils.ts` `getLinkedChannelNumber`, `facade/aux-channel.ts` `[PERMISSIVE-CODE MIT]`; US3 `UiHttpMixerApi.cs:111-113` `[PERMISSIVE-CODE Apache-2.0]`; mixer-side mirroring `[TO-VERIFY]` (item 7) |
| UI-F42 | Master has no mute key; "MUTE ALL" is mute group bit 23 (UI-F44). Muting the main mix from mixcore = setting that bit. | US3 dump (no `m.mute`) `[PERMISSIVE-CODE Apache-2.0]`; US2 `facade/mute-group.ts` `[PERMISSIVE-CODE MIT]`; `[TO-VERIFY]` (item 8) |
| UI-F43 | Solo: `<strip>.solo` 0/1 on inputs, line, player, FX, subs, aux, VCA. Solo and headphone levels: `settings.solovol`, `settings.hpvol.0`, `settings.hpvol.1` (fader law UI-F70). | US2 `selectSolo`, `facade/volume-bus.ts` `[PERMISSIVE-CODE MIT]`; US3 dump `[PERMISSIVE-CODE Apache-2.0]` |
| UI-F44 | **Mute groups are one global bitmask** `mgmask` (integer): bit k-1 = mute group k engaged (k = 1..6), bit 22 = MUTE FX, bit 23 = MUTE ALL. There is no per-group key, so engaging a group is a read-modify-write of the whole mask. Membership of a strip: `<strip>.mgmask` (same bit layout assumed). Group names `mg.0.name`..`mg.5.name`. Six mute groups on the Ui24R. | US2 `facade/mute-group.ts`, `soundcraft-ui.ts` (`clearMuteGroups` writes `mgmask 0`) `[PERMISSIVE-CODE MIT]`; US3 dump (`mgmask`, `i.N.mgmask`, `mg.N.name`) `[PERMISSIVE-CODE Apache-2.0]`; US1 6.1 ("six Mute Groups") `[VENDOR-DOC]`; per-strip bit layout `[TO-VERIFY]` (item 8) |
| UI-F45 | Matrix (Ui24R): an aux slot becomes a matrix with `a.N.matrix = 1` (both members of a linked pair are switched). Sources into matrix N: `a.M.mtx.N.*`, `s.M.mtx.N.*`, `m.mtx.N.*` with `value`, `mute`, `pan`, `postproc`. | US2 `facade/matrix-utils.ts`, `facade/mtx-*.ts`, `docs/docs/buses/matrix.md` `[PERMISSIVE-CODE MIT]`; US3 dump `[PERMISSIVE-CODE Apache-2.0]` |
| UI-F46 | VCA membership: `<strip>.vca` (value -1 in the dump) and `vg.N` (value `[]`), group names `vg.N.name`; the encoding (single index, bitmask or list) is unknown. US1: "When mute is placed on a VCA master, all member channels are also muted". | US3 dump `[PERMISSIVE-CODE Apache-2.0]`; US1 4.2.2 `[VENDOR-DOC]`; encoding `[TO-VERIFY]` (item 8) |
| UI-F47 | Subgroup assignment `<strip>.subgroup` (-1 = none in the dump); encoding beyond -1 unknown. | US3 dump `[PERMISSIVE-CODE Apache-2.0]`; `[TO-VERIFY]` (item 8) |
| UI-F48 | Colour `i.N.color` (integers 0 and 6 seen; palette unknown). | US3 dump `[PERMISSIVE-CODE Apache-2.0]`; `[TO-VERIFY]` (item 12) |

### 3.3 Sends
| ID | Fact | Source |
|---|---|---|
| UI-F50 | Aux send from source X (`i`, `l`, `p`, `f`) to aux M: `X.N.aux.M.value` (level, fader law UI-F70), `.mute` (1 = send off), `.pan` (0..1), `.post` (1 = post-fader, 0 = pre-fader), `.postproc` (1 = after EQ/dynamics; Ui24R). Default in the dump: `post` 0, `postproc` 1. A global default exists (`settings.auxsendpoint`, US1 "GLOBAL AUX SEND POINT"). | US2 `facade/send-channel.ts`, `facade/aux-channel.ts`, `state-selectors.ts` `[PERMISSIVE-CODE MIT]`; US3 dump `[PERMISSIVE-CODE Apache-2.0]`; US1 10.0 `[VENDOR-DOC]`; `post` polarity `[TO-VERIFY]` (item 6) |
| UI-F51 | FX send from source X (`i`, `l`, `p`, `s`) to FX M: `X.N.fx.M.value`, `.mute`, `.post` (dump default 1). No pan. | US2 `facade/fx-bus.ts`, `facade/fx-channel.ts` `[PERMISSIVE-CODE MIT]`; US3 dump `[PERMISSIVE-CODE Apache-2.0]` |
| UI-F52 | FX return into aux: `f.N.aux.M.value/.mute/.pan/.post/.postproc`. | US3 dump `[PERMISSIVE-CODE Apache-2.0]` |
| UI-F53 | Send level, mute, pan for a name: the name exists only on the source (`i.N.name`); `i.N.aux.M.name` does not exist. | US2 `facade/channel.ts` comment `[PERMISSIVE-CODE MIT]` |
| UI-F54 | "Monitor access limit" and "local aux control" in the vendor app restrict a browser to chosen auxes; US1: "This feature is not password protected, it is intended as a safety measure". It is client-side UI behaviour, not a protocol rule; mixcore's own roles (CLAUDE.md invariant 4) replace it. | US1 10.0 `[VENDOR-DOC]` |

### 3.4 Processing (for later phases; keys only, laws to capture)
All keys from the US3 dump `[PERMISSIVE-CODE Apache-2.0]`; ranges from US1 specifications `[VENDOR-DOC]`; every
normalized-to-engineering law is `[TO-VERIFY]` (item 11) except where a source gives it.

| Area | Keys | Vendor range (US1) | Law known? |
|---|---|---|---|
| Headamp (Ui24R) | `hw.N.gain`, `hw.N.phantom`, `hw.N.hiz` (ch 1-2), `hw.N.disablegain` | gain "-6dB to +58dB" (spec table) and "-10dB up to +60dB" (gain page text) | UI-F72 |
| Headamp (Ui12/16) | `i.N.gain`, `i.N.phantom`, `i.N.hiz` | see UI-F72 | UI-F72 |
| Line in gain | `l.N.gain` (0.4 in the dump) | unknown | no |
| Polarity | `i.N.invert`, `a.N.invert`, `m.l.invert`, `m.r.invert` | on/off | yes (0/1) |
| Delay | `i.N.delay`, `l.N.delay`, `a.N.delay`, `m.delayL`, `m.delayR` (seconds) | input 1-250 ms | UI-F73 |
| HPF / LPF | `i.N.eq.hpf.freq`, `.hpf.slope`, `.lpf.freq`, `.lpf.slope` | HPF 20 Hz-1 kHz, LPF 22 kHz-1 kHz | no |
| PEQ | `i.N.eq.b1..b4.freq/gain/q` (also `b5` in the dump), `eq.bypass`, `eq.easy` | 4 bands, 20 Hz-22 kHz, Q 0.05-15, gain -20..+20 dB | no (gain 0.5 = 0 dB likely) |
| Gate | `gate.thresh/attack/release/hold/depth/enabled/bypass` | threshold -inf..+6 dB, attack 1-400 ms, release 5-2000 ms, hold 1-2000 ms, depth -inf..0 dB | no |
| Compressor | `dyn.threshold/ratio/attack/release/gain/outgain/softknee/bypass` | threshold -90..+6 dB, ratio 1:1-50:1, attack 1-400 ms, release 10-2000 ms, makeup -24..+48 dB | no |
| De-esser | `i.N.deesser.enabled/freq/ratio/threshold` | unknown | no |
| Bus GEQ | `a.N.eq.peak.0..30`, `m.eq.peak.l/r.0..30` (0.5 = flat) | 31 bands, +-15 dB | no |
| FX | `f.N.fxtype` (-1 none, 0 reverb, 1 delay, 2 chorus, 3 room), `par1..par6` (0..1), `bpm` (20..400) | n/a | type yes (US2 `FxType`), params no |
| Automix (Ui24R) | `i.N.amixgroup` (-1, 0 = A, 1 = B), `i.N.amix` weight, `automix.a.on`, `automix.b.on`, `automix.time` | weight -12..+12 dB, time 20-4000 ms | US2: weight linear -12..12; time = 20 + 3980 x v^3.0517 ms |
| Patch (Ui24R) | `i.N.src` (`hw.N`, USB), `hwoutaux.N.src`, `hwoutm.N.src`, `usbdaw.N.src` (SETS) | n/a | names only |
| Safes | `<strip>.safe` (channel safes, "Isolate") | n/a | 0/1 |

### 3.5 Scenes (shows, snapshots, cues)
| ID | Fact | Source |
|---|---|---|
| UI-F60 | Recall and store are by **name**: `LOADSHOW^<show>`, `LOADSNAPSHOT^<show>^<snapshot>`, `LOADCUE^<show>^<cue>`, `SAVESNAPSHOT^<show>^<snapshot>`, `SAVECUE^<show>^<cue>` (save overwrites without confirmation). Lists: send `SHOWLIST`, reply `SHOWLIST^<name>^<name>...`; `SNAPSHOTLIST^<show>` and `CUELIST^<show>` reply in the same form keyed by show; an empty list ends with a trailing `^`. Lists are sent only to the requesting client. | US2 `facade/show-controller.ts`, `state/resource-lists.ts`, `docs/docs/features/shows.md` `[PERMISSIVE-CODE MIT]` |
| UI-F61 | Current show, snapshot and cue are state keys `var.currentShow`, `var.currentSnapshot`, `var.currentCue` (dump: `Default`, `* Init *`, empty). Unlike X32 (X32-F60), a recall therefore has a **direct signal**: a push of `var.currentSnapshot` or `var.currentCue`. | US2 `facade/show-controller.ts` `[PERMISSIVE-CODE MIT]`; US3 dump `[PERMISSIVE-CODE Apache-2.0]`; pushed on recall from another client, and before or after the parameter burst `[TO-VERIFY]` (item 10) |
| UI-F62 | A snapshot is "a record of every setting on the console"; channel safes (`<strip>.safe`) keep a channel from changing on recall; AFS2 settings are not in snapshots. The factory state is show `Default`, snapshot `* Init *`. | US1 8.0, 4.1 "ISOLATE" `[VENDOR-DOC]` |
| UI-F63 | Phantom on a channel momentarily mutes its output "to avoid transients". | US1 4.1 `[VENDOR-DOC]` |

### 3.6 Settings and runtime keys (read only in mixcore)
`settings.*` (global preferences, incl. `settings.block.*` and `settings.block.pass`, UI-F86; `settings.maxconn`
= 48 in the dump, meaning unknown; `settings.multiplesolo`, `settings.soloMode`, `settings.auxmutelink`,
`settings.cascade.*`), `var.*` (runtime: player, recorder, `var.unsaved.*`), `iso.*`, `casc.N.src`, `afs.enabled`.
Source: US3 dump `[PERMISSIVE-CODE Apache-2.0]`. mixcore never writes `settings.*` or `var.*`.

### 3.7 Value encodings and reference points
| ID | Fact | Source |
|---|---|---|
| UI-F70 | **Fader law** (`mix`, send `value`, `settings.solovol`, `settings.hpvol.N`): position v in 0..1 maps to linear amplitude A(v) = s(v) x exp(P(v)) x 2.676529517952372e-4 with P(v) = v x (23.90844819639692 + v x (-26.23877598214595 + v x (12.195249692570245 - 0.4878099877028098 v))) and s(v) = sin(28.559933214452666 v) below v = 0.055, else 1; dB = 20 log10 A(v). v = 0 is -inf, v = 1 is +10 dB. The inverse has no closed form (US2 uses Newton's method; bisection also works, A is monotonic). | US2 `utils/value-converters/value-converters.ts`, `db-lut.ts` `[PERMISSIVE-CODE MIT]` |
| UI-F71 | **Cross-check:** US3 recorded positions with the dB shown by the vendor app: 0.7631767231 = 0 dB, 0.6328006996 = -5, 0.5278639002 = -10, 0.3656888465 = -20, 0.1812544717 = -40, 0.0540583512 = -60, 1.0 = +10. UI-F70 gives -0.06, -5.14, -10.08, -20.88, -40.67, -60.88, +10.00 dB for them: within 0.15 dB above -15 dB, up to 1.0 dB low below. The US3 dump stores unity defaults as **0.7647058824 (= 13/17)**, which UI-F70 maps to exactly 0 dB. mixcore implements UI-F70 and confirms with a law fit (item 5). | US3 `UiHttpMixerApi.cs:177-207` comment, dump (`f.N.mix`, `s.N.mix`, `v.N.mix`) `[PERMISSIVE-CODE Apache-2.0]`; comparison derived; `[TO-VERIFY]` below -15 dB (item 5) |
| UI-F72 | **Gain law:** linear in dB. Ui24R `hw.N.gain`: dB = 63 v - 6 (range -6..+57 dB); Ui12/Ui16 `i.N.gain`: dB = 90 v - 40 (-40..+50 dB). US2 notes "the value conversion for gain dB values in the original Web UI is not exact". Vendor statements disagree (Ui24R "-6dB to +58dB" and "-10dB up to +60dB"; Ui12/16 "-30..60", "-40..+50", "-40..+60"). The dump's 0.09523809524 = 6/63 is 0 dB under the US2 law. | US2 `facade/hw-channel.ts`, `value-converters.spec.ts`, `docs/docs/buses/hwchannel.md` `[PERMISSIVE-CODE MIT]`; US1 spec table, 4.1 `[VENDOR-DOC]`; US8 `[COMMUNITY]`; `[TO-VERIFY]` (item 5) |
| UI-F73 | Delay keys are **seconds**: input and line max 0.25 s (US1: "1-250ms"), aux max 0.5 s, master `delayL`/`delayR` max 0.5 s. | US2 `facade/delayable-master-channel.ts`, `facade/master-bus.ts` `[PERMISSIVE-CODE MIT]`; US1 4.1 `[VENDOR-DOC]` |
| UI-F74 | Pan (`pan`, send `pan`): 0..1, 0.5 = centre; 0 = left, 1 = right (a stereo line-in pair stores 0 and 1). | US2 `setPan` (clamp 0..1) `[PERMISSIVE-CODE MIT]`; US3 dump (`l.0.pan` 0, `l.1.pan` 1) `[PERMISSIVE-CODE Apache-2.0]`; direction `[TO-VERIFY]` (item 6) |

Fader reference points (computed from UI-F70 with bisection; the driver's table test must reproduce them to 1e-6):

| dB | v | dB | v | dB | v |
|---|---|---|---|---|---|
| -inf | 0 | -40 | 0.1862301653 | -6 | 0.6127284975 |
| -100 | 0.0012694148 | -30 | 0.2693554198 | -3 | 0.6855889220 |
| -90 | 0.0037875145 | -20 | 0.3765058366 | 0 | 0.7647058824 (13/17) |
| -80 | 0.0103858440 | -10 | 0.5294117647 (9/17) | +3 | 0.8437562720 |
| -60 | 0.0588235294 (1/17) | | | +6 | 0.9165390820 |
| -50 | 0.1176470588 (2/17) | | | +10 | 1.0 |

Gain reference points (UI-F72, US2 law): Ui24R 0 dB = 0.0952380952, +20 dB = 0.4126984127, +36 dB = 0.6666666667,
+57 dB = 1.0; Ui12/16 0 dB = 0.4444444444, +20 dB = 0.6666666667, +50 dB = 1.0.

Example lines (WebSocket form; the raw transport drops `3:::`):

| What | Line |
|---|---|
| Input 1 fader to 0 dB | `3:::SETD^i.0.mix^0.7647058824` |
| Input 3 mute on | `3:::SETD^i.2.mute^1` |
| Input 1 send to aux 2 at -10 dB | `3:::SETD^i.0.aux.1.value^0.5294117647` |
| Engage mute groups 1 and 3 when the mask was 0 | `3:::SETD^mgmask^5` |
| Name input 5 | `3:::SETS^i.4.name^VOX` |
| Recall a snapshot | `3:::LOADSNAPSHOT^Sunday^Band` |
| Keep-alive | `3:::ALIVE` |

## 4. Echo, push vs poll, client limits

| ID | Fact | Source |
|---|---|---|
| UI-F80 | **The mixer does not send a client's own change back to it.** US2: a change is received by "all other connected clients"; US2 therefore updates its local state from its own outgoing messages. US3 opens **two** connections, one to send and one to receive, "so that we have our own changes reflected back to us, just like with other protocols". | US2 `docs/docs/more/howitworks.md`, `mixer-store.ts` (state from inbound and outbound) `[PERMISSIVE-CODE MIT]`; US3 `UiHttpMixerApi.cs:58-62` `[PERMISSIVE-CODE Apache-2.0]`; `[TO-VERIFY]` (item 6) |
| UI-F81 | Push, not poll: every change made by any client (or a snapshot recall, footswitch, MIDI-less mixer) reaches every other client as `SETD`/`SETS`. There is no subscription to renew. | US2 howitworks `[PERMISSIVE-CODE MIT]` |
| UI-F82 | Client limit: "Up to 10 control devices can be used simultaneously. (Depending on available bandwidth)". What happens to the 11th (refused, accepted without updates, or accepted) is not documented. A forum user asked for a list of connected devices, which the vendor app lacks. | US1 p.4, 3.0.1 p.19 `[VENDOR-DOC]`; US7 `[COMMUNITY]`; https://proforums.harman.com/soundcraft/discussion/125411/ui24-control-see-who-is-connected (snippet) `[COMMUNITY]`; 11th client `[TO-VERIFY]` (item 3) |
| UI-F83 | Whether a WebSocket and a raw connection count alike against the limit, and whether a mixcore session (two connections, UI-F80) takes two of the ten, is unknown. | none; `[TO-VERIFY]` (item 3) |
| UI-F84 | Writes the mixer cannot apply (unknown path, out of range) get no error message in any library; behaviour unknown. mixcore never sends either (paths from the received state only, values clamped). | none; `[TO-VERIFY]` (item 12) |
| UI-F85 | Concurrency hazard: `mgmask` is one integer for all groups (UI-F44); two clients toggling different groups at the same moment can overwrite each other. mixcore writes the mask computed from the newest pushed value and re-checks the echo on the other connection. | derived from UI-F44, UI-F80 |
| UI-F86 | **Master password lock** ("HARD LOCK (ADMIN/PASS) ACCESS LIMITATIONS"): a logged-in user can restrict non-logged-in clients to some of AUX LEVELS, AUX PROCESSING, MIX LEVELS, MIX PROCESSING, MIX GAIN, PLAYER, MASTER LEVEL, MASTER PROCESSING, SHOWS, GLOBAL SETTINGS. The state carries `settings.block.auxlvl`, `.auxproc`, `.mixlvl`, `.mixproc`, `.mixgain`, `.player`, `.mlvl`, `.mproc`, `.shows`, `.gsettings`, `.loadshows`, `.presets` and `settings.block.pass`. Whether the mixer rejects a blocked write or only the browser app hides it, and how the app logs in, is unknown. | US1 10.1.4 `[VENDOR-DOC]`; US3 dump `[PERMISSIVE-CODE Apache-2.0]`; enforcement `[TO-VERIFY]` (item 13) |

### Echo and conflict design for `driver/ui` (from UI-F80, same pattern as X32-F23)
- Two TCP connections per engine session, opened once and kept across reconnects: the **state connection** receives
  the full state, every push and the meters and never writes; the **write connection** sends writes and `ALIVE` and
  its inbound frames are drained and ignored except as liveness proof. Under UI-F80 every write we make arrives on the
  state connection as a change "from another client": that is our echo, matched against the last 32 written values
  per key inside a 2 s window, exactly as `x32.md` "Echo and conflict handling". `EchoesOwnWrites` = true.
- If item 6 shows that the mixer **does** echo to the sender, nothing breaks: the write connection's inbound is
  ignored, and the state connection still sees the change.
- The cost is two of the ten client slots (UI-F82/F83). Musicians' devices reach the console through MixLink, so a
  venue normally runs one mixcore session plus the vendor app on one or two devices.
- Stereo links (UI-F41): a gesture on a linked strip produces writes to both keys; both echoes are expected; a push
  of only one member's key is an external change.
- `mgmask` writes count as echoed only when the pushed mask equals the written mask.
- Background re-read: there is no read command (UI-F36), so the X32 15 s re-read pass does not exist; TCP does not
  drop single pushes. A full re-read is a reconnect of the state connection only (the write connection keeps its
  slot), run when the user asks or after the stale timer fired.

## 5. Meters

| ID | Fact | Source |
|---|---|---|
| UI-F90 | Meters arrive as `VU2^<base64>` lines without any request, on every connection. Rate not documented. | US2 `vu/vu-processor.ts` `[PERMISSIVE-CODE MIT]`; US3 `UiHttpMixerApi.cs:260-264` `[PERMISSIVE-CODE Apache-2.0]`; rate and "no request" `[TO-VERIFY]` (item 9) |
| UI-F91 | Decoded payload: an 8-byte header with counts `[inputs, players, subgroups, fx, aux, masters, line-ins, 0]`, then blocks in that order. Block sizes: input, player, line-in = 6 bytes `[pre, post, postFader, gateIn, compOut, compMeterAndGated]`; subgroup and FX = 7 bytes `[postL, postR, postFaderL, postFaderR, gateIn, compOut, compMeterAndGated]`; aux and master = 5 bytes `[post, postFader, gateIn, compOut, compMeterAndGated]`. The two masters are L and R. US3 rejects a frame whose length is not header + the sum of blocks. Field names after `postFader` are US3's; US2 skips them. | US2 `vu/vu.utils.ts` `[PERMISSIVE-CODE MIT]`; US3 `UiMeters.cs`, `UiHttpMixerApi.cs:276-335` `[PERMISSIVE-CODE Apache-2.0]` |
| UI-F92 | Worked check (US2 test vector `vuMsg24_1`, a Ui24): header `18 02 06 04 0a 02 02 00` = 24 inputs, 2 players, 6 subs, 4 FX, 10 aux, 2 masters, 2 line-ins; total length 8 + 28x6 + 10x7 + 12x5 = 306 bytes; every block ends with the byte `f7` in the vectors (`0000000000f7` for a silent input). The Ui12 vector `vuMsg12_1` has header `08 02 04 04 04 02 02 00` and 166 bytes. | US2 `vu/vu-test-messages.ts` `[PERMISSIVE-CODE MIT]`; decoding and arithmetic by us; meaning of the last byte `[TO-VERIFY]` (item 9) |
| UI-F93 | Scale: a byte b maps to linear 0..1 as b x 0.004167508166392142 (about b/240); US3 clamps at 240 ("240 is actually the highest value we see"). US2 turns 0..1 into dB linearly over -80..0 dB, i.e. **dBFS = b/3 - 80** (b = 240 is 0 dBFS, b = 0 is -80 or below). | US2 `vu/vu.utils.ts` `[PERMISSIVE-CODE MIT]`; US3 `UiHttpMixerApi.cs:329-334` `[PERMISSIVE-CODE Apache-2.0]`; dB mapping and peak vs RMS `[TO-VERIFY]` (item 9) |
| UI-F94 | Meters exist only for strips on the master bus: no per-send meters. | US2 `docs/docs/features/vu.md` `[PERMISSIVE-CODE MIT]` |

mixcore meter mapping: input strip meter = `postFader` (fall back to `pre` for an input-gain view), aux/sub/FX/main =
`postFader`; meters are display-only and never feed echo or stale logic beyond "a frame arrived".

## 6. Known quirks and failure modes
| ID | Fact | Source |
|---|---|---|
| UI-F96 | The hotspot is short-range and crowded 2.4 GHz fails first; users report drops at 5-6 m and advise 5 GHz or an external router, and a wired connection for the operator. | US10 `[COMMUNITY]` |
| UI-F97 | A LAN address change needs a power cycle; DHCP is off by default (static 10.10.2.1). Phones joined to the mixer's hotspot have no internet and may leave it (domain section 6). | US1 10.1.3 `[VENDOR-DOC]` |
| UI-F98 | `SAVESNAPSHOT` and `SAVECUE` overwrite without confirmation; the US2 docs warn about it. mixcore treats them as confirmed actions (two-step UI), never as side effects. | US2 `docs/docs/features/shows.md` `[PERMISSIVE-CODE MIT]` |
| UI-F99 | Reboot: the mixer reloads its last state; all client connections drop. Whether unsaved changes survive a power cut is unknown. | none; `[TO-VERIFY]` (item 14) |

## Safety and legal rules for this family (decided by the protocol researcher; lead to confirm)
- Never fetch `/js/initparams.js` or any page of the vendor web app; never read, store or show `adminPassword`,
  Wi-Fi keys or `settings.block.pass`. State comes only from the control socket.
- Honour the master-password lock (UI-F86) instead of probing it: while any `settings.block.*` flag is set, the matching
  areas are read-only in mixcore and the UI says the console is locked. mixcore never sends a login and never writes
  `settings.block.*`. (Bypassing a password is excluded by invariant 8.)
- Phantom writes need the confirmation flow of the other families (gain/phantom rules of CR-23), and UI-F63 applies.

## Driver shape and simulator (recommendation for core-engineer)
- `driver/ui`: one Go package; transport interface with two implementations, `wsTransport` (WebSocket text frames,
  `3:::` framing, answers `2::` with `2::`) as default, `rawTransport` (`GET /raw`) behind an option until item 1
  settles which is more robust. A shared line codec (`SETD`, `SETS`, `VU2`, lists) with fuzz tests. The state is a map
  of path to value built from the initial burst; model mapping is a table from path patterns to `model.ParamRef`
  generated from section 3.2, filtered by the strips present in the state (UI-F03).
- Contract need: `driver.Options` has a `ListenPacketFunc` for UDP; TCP families need a `DialFunc` (Android network
  binding). Propose it in `contracts/` before implementing.
- Connect: open state connection, collect state until 250 ms without a new key (UI-F20), then open the write
  connection, then report `Snapshot`. Send `ALIVE` every 1 s on both. Stale after 1.5 s without any inbound frame on
  the state connection.
- Echo, stereo links, `mgmask`, scenes: section 4 design; `SceneRecalled` from a `var.currentSnapshot` /
  `var.currentCue` push with confidence `high`, else the X32 burst heuristic as `low`.
- `sim/ui`: a TCP server on a configurable port serving both a minimal WebSocket upgrade and `GET /raw`; a state store
  seeded per model (counts of UI-F03; a Ui24R seed can be generated from the published US3 key list, values ours);
  sends the full state on connect, broadcasts changes to every **other** client (option: echo to sender too), sends
  `VU2` frames at a configurable rate with the UI-F91 layout, sends `2::` heartbeats, answers `SHOWLIST`,
  `SNAPSHOTLIST`, `CUELIST` per client and applies `LOADSNAPSHOT` as a burst plus `var.currentSnapshot`. Options for
  every `[TO-VERIFY]` behaviour: client limit 10 with refuse / accept-silent policy for the 11th, heartbeat timeout,
  ALIVE timeout, mixer-side link mirroring, blocked writes under `settings.block.*`.

## 7. To verify on hardware (open list)
Setup for all items: a Ui24R (and a Ui12 or Ui16 for items 3, 5, 9) on a dedicated router; a Mac on the same network;
amplifiers off. Captures go to `~/Desktop/dybyskz-mix/research/captures/ui24r/<date>/`.

**Capture method.** `mixcapture` decodes OSC, MIDI, text lines and JSON, but **not WebSocket frames** (browser frames
are masked, RFC 6455), and its `text` decoder keys on tokens containing `/` or `:`, which `SETD^i.0.mix^0.5` has
not. Until a `ws`+`caret` decoder exists (follow-up for core-engineer), record the vendor app in Chrome: DevTools >
Network > filter WS > select the socket > Messages; at the end "Save all as HAR with content" (Chrome stores the
WebSocket messages in the HAR). Observing our own browser's traffic is black-box capture under invariant 8. In parallel
run `mixcapture live -i en0 -console <ui-ip> -decoder 80=text -out ...` to keep the pcap and the timing.

1. UI-F10/F13/F21/F36 transports. In Chrome open the vendor app, note the WebSocket URL and the first 20 frames in
   each direction. Then from the Mac: `websocat ws://<ip>/` (or any WS client) and log the first 2 s; then
   `printf 'GET /raw HTTP1.1\n\n' | nc <ip> 80` and log 2 s; then the same with `INIT\n` sent after the headers. Pass:
   which paths answer, whether the full state comes without `INIT`, whether any per-key read command is visible.
2. UI-F12/F25 keep-alives. With the WS client: (a) send `3:::ALIVE` every 1 s and echo `2::` for 5 min; (b) stop
   `ALIVE`, keep echoing; (c) send `ALIVE`, never echo `2::`. Log the time to close in (b) and (c) and the `2::`
   interval. Pass: the timeouts, so the driver's intervals have margin.
3. UI-F03/F82/F83 counts and client limit. Read the counts from the state on Ui24R and Ui12/16 (subgroups 4 or 6).
   Open WS clients one by one up to 12 (script), each logging frames; then mix raw and WS clients. Pass: what the 11th
   gets (refused, state but no pushes, full service) and whether connections of both kinds count.
4. UI-F15 discovery. `tcpdump -i en0 -n host <ui-ip> and not tcp` for 2 min after the mixer boots and while idle;
   also `dns-sd -B _services._dns-sd._udp` and an SSDP `M-SEARCH`. Pass: any announcement (protocol, port, payload).
5. UI-F70/F71/F72 laws. In the vendor app set input 1 fader to -inf, -60, -40, -30, -20, -10, -5, 0, +5, +10 by typing
   values where the app allows, else by dragging and reading the shown dB; log the pushed `i.0.mix` for each. Same for
   `hw.0.gain` (Ui24R) / `i.0.gain` (Ui12/16) at min, 0, +20, +40, max. Feed the pairs to `mixcapture law` (or a
   spreadsheet). Pass: UI-F70 within 0.1 dB above -60 dB; gain law and true range per model.
6. UI-F80/F50/F74 echo and polarities. Two WS clients A and B. From A write `SETD^i.0.mix^0.5`; log A and B for 2 s.
   From A write `i.0.aux.0.post` 1 then 0 and check the vendor app's PRE/POST button; write `i.0.pan` 0 and check the
   pan display. Pass: A receives nothing (or its echo), B receives the change; `post` 1 = POST; pan 0 = left.
7. UI-F41 links. Link inputs 1-2 in the vendor app. From a WS client write only `i.0.mix`; log whether `i.1.mix` is
   pushed or changes. Pass: whether the mixer mirrors links itself.
8. UI-F42/F44/F46/F47 groups. In the vendor app assign input 1 to mute group 2, VCA 3 and subgroup 4; log the pushed
   `i.0.mgmask`, `i.0.vca`, `vg.*`, `i.0.subgroup`. Engage MUTE ALL and MUTE FX; log `mgmask`. Mute VCA 3 and check
   input 1 audio and whether `i.0.mute` changes. Pass: encodings and the effective-mute rule.
9. UI-F90..F93 meters. Feed a 1 kHz sine at -20 dBFS (check on the vendor meter) into input 1, fader 0 dB; log 10 s of
   `VU2`. Then -40 dBFS. Pass: frame rate, byte value of `post` (expect 180 at -20 dBFS if dBFS = b/3 - 80), the last
   byte of each block, peak or RMS (compare with a square wave).
10. UI-F61 scene recall. From the vendor app on device 2 load a snapshot while a WS client logs. Pass: whether
    `var.currentSnapshot` is pushed, and its timing relative to the parameter burst; burst size and duration.
11. Processing laws (section 3.4): for each listed key, three marked points in the vendor app (min, middle, max) plus
    one known value; record with the HAR method. Pass: one law per key, added here as `[CAPTURE]` facts.
12. UI-F31/F48/F84 names, colours, bad writes. Write names of 20 and 24 characters, lower case, non-ASCII; write
    `i.0.color` 0..15 and note the colours; write an unknown path and an out-of-range `i.0.mix 1.5`. Pass: what is
    stored and pushed; no error messages expected.
13. UI-F86 lock. Enable master password in the vendor app (device 1), block MIX LEVELS. From a WS client write
    `i.0.mix`; log whether it applies and whether anything is sent back. **Do not attempt to log in or to guess the
    password.** Pass: whether the mixer enforces the lock.
14. UI-F99 reboot. Change input 1 fader, wait 5 s, power-cycle; read the state after boot. Pass: whether the change
    survived; time from power-on to accepted connection.

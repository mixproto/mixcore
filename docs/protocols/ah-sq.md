# Allen & Heath SQ / SQ+ network control protocol (as mixcore will implement it)

Scope: Allen & Heath SQ-5, SQ-6, SQ-7, SQ-Rack (firmware V1.x) and SQ5+, SQ6+, SQ7+ (firmware V2.x), compatibility
statement only; mixcore is not affiliated with or endorsed by any console maker. This file lists the protocol facts
that `driver/ahsq` and `sim/ahsq` will implement. The driver and the simulator are both written from this file, never
from each other. When a real console disagrees with this file, the console wins: record a `[CAPTURE]` fact here first,
then change the simulator, then the driver.

The vendor publishes one third-party protocol: **MIDI over TCP on port 51325** (NRPN). It covers levels, mutes,
pan/balance, routing assignments, scene recall and SoftKeys. It does **not** cover meters, names, preamps or any
processing; those exist only in the vendor's own app protocol (SQ-MixPad), which is undocumented. Section 3.6 lists the
gaps and section 7 the black-box capture tasks that close them. Research date: 2026-10-13. No SQ hardware was
available: **no captures**.

Facts have IDs `SQ-Fnn`. Shared design rules (echo window, stale timers, hold rules) are those of `x32.md`.

## Tags
Tag names as in `xair.md`; the task names are in brackets:
- `[VENDOR-DOC]` (= `[VENDOR]`, `[OFFICIAL]`): a document published by Allen & Heath (PDF, help-centre article,
  release notes).
- `[PERMISSIVE-CODE <licence>]` (= `[PERMISSIVE]`, `[CODE]`): behaviour read in MIT/BSD/Apache code; licence named.
- `[COMMUNITY]`: forum post, third-party documentation, search-engine snippet.
- `[TO-VERIFY]` (= `[UNVERIFIED]`): plausible, not confirmed by two independent sources, or sources contradict. Every
  such item is in section 7 and becomes a simulator option.
- `[CAPTURE]`: our own capture of hardware we use. **None yet.**
- "derived": arithmetic on tagged facts, shown in full; it inherits the weakest tag of its inputs.

Legal note (CLAUDE.md invariant 8): every fact about the MIDI protocol comes from the vendor's documents and MIT code.
Facts about the proprietary MixPad protocol found in earlier research (`dybyskz-mix/docs/research/protocols/
ah-digico-ssl-waves.md` section 1.4) are **not carried over**: their sources are an MIT library whose own comments say
parts were taken from a decompiled vendor app, an AGPL-3.0 tool and a GPL-3.0 project built from decompilation. The
MixPad protocol will be learned only from our own captures (section 7, C-items).

## Sources (accessed 2026-10-13)
Help-centre articles were read through the help centre's public article API
(`https://support.allen-heath.com/api/v2/help_center/en-gb/articles/<id>.json`), because the HTML pages return 403 to
non-browser clients; the `html_url` is given for each. Reference tables in AS2 and AS3 are images; they were read
visually and transcribed by us.
- **AS1** Allen & Heath, "MIDI Protocol, Issue 5, SQ Firmware V1.5.0 or later", 31 pages:
  https://www.allen-heath.com/content/uploads/2023/11/SQ-MIDI-Protocol-Issue5.pdf. Page numbers are the printed ones.
  `[VENDOR-DOC]`.
- **AS2** "SQ. MIDI Protocol - Firmware V1.6", article 47793519540497, updated 2026-07-07:
  https://support.allen-heath.com/hc/en-gb/articles/47793519540497-SQ-MIDI-Protocol-Firmware-V1-6 . Tables: mute
  (image 47833372111121), mix sends and outputs (47833372120593). `[VENDOR-DOC]`.
- **AS3** "SQ+. MIDI Protocol - Firmware V2.0.3", article 47793537575953, updated 2026-07-07:
  https://support.allen-heath.com/hc/en-gb/articles/47793537575953-SQ-MIDI-Protocol-Firmware-V2-0-3 . Tables: mute
  (47833322570129), inputs to LR/aux (47826405246737), groups to LR/aux (47833301752849), FX sends (47833301758097),
  mix sends (48079317116305), outputs (48079317118865), mix-send balance (48079309677841), output balance
  (48079317122705), matrix-send assignment (48079317123217), RackUltra FX levels (48074268508561, 48237742575377),
  fader-law figure (47860314766225). `[VENDOR-DOC]`.
- **AS4** "SQ Firmware Release Notes", article 43757630905361, updated 2026-08-02 (current V1.6.3, February 2026):
  https://support.allen-heath.com/hc/en-gb/articles/43757630905361-SQ-Firmware-Release-Notes . `[VENDOR-DOC]`.
- **AS5** "SQ+ Firmware Release Notes", article 46809870100753, updated 2026-10-07 (current V2.0.3, June 2026):
  https://support.allen-heath.com/hc/en-gb/articles/46809870100753-SQ-Firmware-Release-Notes . `[VENDOR-DOC]`.
- **AS6** "SQ. Setup" (45947065818769) and "SQ+. Setup" (46600653572625), updated 2026-07-06, sections Network Setup,
  User Permissions, I/O Port: https://support.allen-heath.com/hc/en-gb/articles/45947065818769 and
  https://support.allen-heath.com/hc/en-gb/articles/46600653572625 . `[VENDOR-DOC]`.
- **AS7** "Allen & Heath for IT managers", article 37287399691409, updated 2026-09-04 (covers AHM, dLive, Avantis,
  not SQ): https://support.allen-heath.com/hc/en-gb/articles/37287399691409-Allen-Heath-for-IT-managers .
  `[VENDOR-DOC]` for those products; applicability to SQ `[TO-VERIFY]`.
- **AS8** "Allen & Heath MIDI Control Help" (41264039501969, updated 2025-11-25), "SQ. MIDI and DAW Control"
  (45947110744465, 2026-07-06), "General. Qu. SQ. Sending CC MIDI messages from an app over TCP/IP" (4411155610641,
  2026-03-26). `[VENDOR-DOC]`.
- **AS9** bitfocus/companion-module-allenheath-sq, **MIT**, commit `ec5c2ad` (2026-09-29):
  https://github.com/bitfocus/companion-module-allenheath-sq (`src/mixer/mixer.ts`, `src/mixer/level.ts`,
  `src/mixer/models.ts`, `src/mixer/nrpn/source-to-sink.ts`, `src/midi/parse/channel-parser.ts`,
  `src/midi/tokenize/tokenizer.ts`, `src/config.ts`, `companion/HELP.md`). `[PERMISSIVE-CODE MIT]`.
- **AS10** magichao326/allen-heath-sq-midi-control, **MIT**, pushed 2026-07-01, `PROTOCOL.md` (claims "Verified on
  real SQ-6 hardware"; its fader address disagrees with AS1, so only its connection note is used):
  https://github.com/magichao326/allen-heath-sq-midi-control . `[PERMISSIVE-CODE MIT]`.
- **AS11** Allen & Heath forum, "Connecting a 9th app, is it possible?", 2025-08-14, post by "MarkF" (role not shown):
  https://forums.allen-heath.com/t/connecting-a-9th-app-is-it-possible/29212 . `[COMMUNITY]`.
- **AS12** Mixing Station documentation, "SQ Series" (`docs/mixers/ah/sq.md` in
  https://github.com/davidgiga1993/mixing-station-docs, no licence file, facts only). `[COMMUNITY]`.

Pointers not used: hrueger/allen-heath-sq-tools (MIT, but states that parts come from a decompiled vendor app),
tschiemer/sqmix-mitm (AGPL-3.0), OpenMix (GPL-3.0, built from decompilation), stoatworks-labs/songbook (untested).

## 1. Models, firmware, scope

| ID | Fact | Source |
|---|---|---|
| SQ-F01 | SQ-5, SQ-6, SQ-7 and (from firmware V1.6.0) SQ-Rack run SQ firmware V1.x; current V1.6.3 (February 2026). NRPN control of levels, pan/balance, mutes, assignments and SoftKeys was added in **V1.4.0**; Audio Taper fader law in V1.5.0; AS1 covers V1.5.0 and later. | AS4 (V1.4.0, V1.5.0, V1.6.0, V1.6.3 entries) `[VENDOR-DOC]`; AS1 cover `[VENDOR-DOC]` |
| SQ-F02 | SQ5+, SQ6+, SQ7+ run firmware V2.x; "Added MIDI over USB and MIDI/TCP control protocol" in **V2.0.2** (May 2026); current V2.0.3 (June 2026) adds MIDI control of RackUltra FX sends and returns. Before V2.0.2 an SQ+ has no MIDI/TCP. | AS5 `[VENDOR-DOC]` |
| SQ-F03 | Channel counts used by the protocol tables: 48 inputs (`Ip1`..`Ip48`), 12 groups, 12 aux mixes, 8 FX returns, 4 FX sends, LR, 8 DCAs, 8 mute groups; matrices: 3 on SQ V1.5 (AS1), 3 stereo splittable into 6 mono from V1.6.0 (mutes and output levels of Mtx4-6 exist, sends to Mtx4-6 do not, SQ-F49), 6 on SQ+. Scenes 1..300. SQ-5 has 8 SoftKeys, SQ-6/SQ-7 16; SQ-6 4 and SQ-7 8 Soft Rotaries. | AS1 tables, 3.1 `[VENDOR-DOC]`; AS2, AS3 tables `[VENDOR-DOC]`; AS4 V1.6.0 `[VENDOR-DOC]`; AS9 `models.ts` `[PERMISSIVE-CODE MIT]` |
| SQ-F04 | Mixes are configurable on the console ("Mixer Config": mono/stereo mixes, group count); the NRPN tables do not change with it. What a stereo aux pair or a stereo input does with a write to its second member is not documented. | AS4 known issue "Incorrect sync with MixPad when Mixer Config is changed" `[VENDOR-DOC]`; AS1 (silent); `[TO-VERIFY]` (item 7) |
| SQ-F05 | **SQ and SQ+ use different parameter numbers** for matrix sends, output masters and DCA masters (SQ-F44..F47). A driver that sends SQ numbers to an SQ+ moves a different fader. | AS2 vs AS3 tables `[VENDOR-DOC]` |
| SQ-F06 | Apps and firmware must match in their first two version numbers (e.g. SQ firmware V1.5.0 with SQ-MixPad V1.5.2). This applies to the vendor apps, and therefore to the capture sessions of section 7. | AS4, AS5 notes `[VENDOR-DOC]` |

Covered: SQ V1.5.0..V1.6.3 (AS1, AS2) and SQ+ V2.0.2..V2.0.3 (AS3). SQ V1.4.x has NRPN but no Audio Taper and no
document in hand; the driver accepts it with the AS1 tables and logs a warning. The driver never refuses a version.
Out of scope for driver v1: MIDI fader strips and DAW control (CC 0..31 and notes on the DAW channel), MMC, MIDI Show
Control, Soft Rotary MIDI, RackUltra FX parameter CCs and Vocal Tuner notes, MIDI over USB.

## 2. Transport, discovery, session, keep-alive

| ID | Fact | Source |
|---|---|---|
| SQ-F10 | Transport: TCP, console port **51325**. "All other clients used for network communication should be configured to send messages to the SQ's IP address and use port 51325." The stream is raw MIDI bytes; there is no framing, length or checksum. | AS1 1.1 p.3 `[VENDOR-DOC]`; AS3 "Connection" `[VENDOR-DOC]`; AS7 ("TCP encapsulated MIDI messages") `[VENDOR-DOC]`; AS9 `mixer.ts:99` (`SQMidiPort = 51325`) `[PERMISSIVE-CODE MIT]` |
| SQ-F11 | No authentication on 51325 in any document. A&H documents a TLS port 51327 for AHM, dLive and Avantis, and A&H MIDI Control offers "Secure TCP/IP"; nothing documents TLS for SQ. SQ has an Admin plus up to 10 users with passwords and permissions (console-side); whether the MIDI port obeys them is not documented. | AS1, AS3 (no login) `[VENDOR-DOC]`; AS7 `[VENDOR-DOC]`; AS8 MIDI Control Help `[VENDOR-DOC]`; AS6 "User Permissions" `[VENDOR-DOC]`; TLS on SQ and permissions on MIDI `[TO-VERIFY]` (item 11) |
| SQ-F12 | Network: DHCP by default; without a DHCP server the console assigns itself an address ("auto-IP", shown with `*`); static setting available; "Unit Name" is the name other devices and apps see. On SQ+ with an option card and "Control Network Bridge" on, MIDI over TCP also works through the card's network. | AS6 "Network Setup", "I/O Port" `[VENDOR-DOC]`; AS3 "Connection" `[VENDOR-DOC]` |
| SQ-F13 | Discovery: the vendor documents only that the console "will be seen by any apps running on devices connected to the same network", and A&H MIDI Control lists mixers in a drop-down. For AHM/dLive/Avantis A&H states that "Each unit on the network broadcasts a UDP 'find' message once per second" (port not given there). The SQ discovery port and payload are **not in any document we may use**. | AS6, AS8 `[VENDOR-DOC]`; AS7 `[VENDOR-DOC]` (other products); SQ port/payload `[TO-VERIFY]` (item C1) |
| SQ-F14 | The console MIDI channel N (1..16) is set on the console (Utility > General > MIDI); the DAW-control channel is always N+1. Default N = 1 (MIDI strips default to channel 2). Every NRPN, scene and SoftKey message uses channel N in its status byte (`Bn`, `Cn`, `9n`, `8n` with n = N-1). There is no documented way to ask the console for N; A&H MIDI Control "automatically" sets its channel "to match the MIDI Channel selected at the mixer". | AS1 1.2 p.4 `[VENDOR-DOC]`; AS8 ("By default, these messages are transmitted on MIDI Channel 2"; CC article) `[VENDOR-DOC]`; how MIDI Control learns N `[TO-VERIFY]` (item C2) |
| SQ-F15 | **SQ+ RackUltra FX use their own MIDI channels** (up to 4, set on the console), and most of their NRPN numbers are the same as the main channel's "to LR" numbers: e.g. `40 00` on a RackUltra channel is "Ip1 to that unit's send", `4F 50` is the unit's send output, `4F 51` its return to LR. A message on the wrong channel changes a different control. | AS3 "RackUltra FX" ("it is very important that RackUltra FX units all have different MIDI channels assigned"), images 48074268508561, 48237742575377 `[VENDOR-DOC]` |
| SQ-F16 | Connect behaviour: AS10 reports that right after accepting the connection the console "pushes a burst of its current surface state", and that a client which sends NRPN before reading that burst gets the connection reset ("Broken Pipe"); its client sleeps 0.5 s and drains. AS9 does not rely on any burst: it sends a get for every mute, then for levels, "around 26KB of data ... briefly clogging the connection in both directions", with a "delayed" option that sends 20 gets then sleeps 300 ms. | AS10 `PROTOCOL.md` `[PERMISSIVE-CODE MIT]`; AS9 `mixer.ts:60-96,388-460` `[PERMISSIVE-CODE MIT]`; burst content and reset `[TO-VERIFY]` (item 2) |
| SQ-F17 | Keep-alive: none documented for SQ. Firmware V1.6.0 adds "Support for Active Sense MIDI" (`FE`) without describing it. Precedent in another A&H product (legacy Qu, MIDI Protocol V1.9): the Qu sends `FE` about every 300 ms when idle and, once a client has sent `FE`, closes the connection if no data arrives for 12 s. AS9 uses plain TCP with no keep-alive. | AS4 V1.6.0 `[VENDOR-DOC]`; Qu precedent as summarised in `dybyskz-mix/docs/research/protocols/ah-digico-ssl-waves.md` 1.2 (Qu MIDI Protocol V1.9 pp.3, 10) `[VENDOR-DOC]` (other product); AS9 `[PERMISSIVE-CODE MIT]`; SQ behaviour `[TO-VERIFY]` (item 3) |
| SQ-F18 | V1.6.0 fixed "MIDI Control Port not correctly released after Network Change" (ID-1877); older firmware may refuse new MIDI connections after a network change until reboot. V2.0.3 (SQ+) fixed a crash when "Performing Firmware update while sending MIDI messages" (ID-1935). | AS4, AS5 `[VENDOR-DOC]` |

**mixcore liveness design** (from SQ-F16/F17): connect, read without sending for 500 ms or until 200 ms of silence
after the last byte (whichever is later), then start. Never send `FE` until item 3 shows what the console does with
it. Liveness probe: a get of LR mute (`00 44`, SQ-F40) every 1 s and after 400 ms without inbound bytes; a reply proves
both directions. Stale after 1.5 s without any inbound byte or 3 s without a reply to a probe (numbers of X32-F25).
A get is a read; it never changes the console (OriginSystem).

## 3. Message grammar and the parameter map

### 3.1 Grammar (n = console MIDI channel - 1; hex bytes)
| ID | Message | Bytes | Source |
|---|---|---|---|
| SQ-F20 | NRPN absolute value | `Bn 63 MB  Bn 62 LB  Bn 06 VC  Bn 26 VF` | AS1 3.3-3.6 pp.11-17 `[VENDOR-DOC]` |
| SQ-F21 | Increment (mute and assignment: toggle; level: +1 dB; pan: one step right) | `Bn 63 MB  Bn 62 LB  Bn 60 00` | AS1 3.3-3.6 `[VENDOR-DOC]` |
| SQ-F22 | Decrement (mute and assignment: toggle; level: -1 dB; pan: one step left) | `Bn 63 MB  Bn 62 LB  Bn 61 00` | AS1 3.3-3.6 `[VENDOR-DOC]` |
| SQ-F23 | **Get** the current value of any mute, level, pan/balance or assignment parameter; the console answers with SQ-F20 for that parameter. | `Bn 63 MB  Bn 62 LB  Bn 60 7F` | AS1 3.7 p.18 `[VENDOR-DOC]` |
| SQ-F24 | Scene recall: bank then program. Scenes 1-128 = bank `00`, 129-256 = `01`, 257-300 = `02`; program = (scene - 1) mod 128. "When recalling a Scene on the SQ, the same message is sent." Blank scenes cannot be recalled. | `Bn 00 BK  Cn PG` | AS1 3.1 p.9 `[VENDOR-DOC]`; AS3 "Scene change (All Scenes)" `[VENDOR-DOC]` |
| SQ-F25 | Cue-list recall (V1.6+ and SQ+): banks `0A`, `0B`, `0C` select cue positions 1-128, 129-256, 257-300. MSC GO / TIMED GO / LOAD / RESET are also accepted. | `Bn 00 0A..0C  Cn PG` | AS2, AS3 "Scene change (Cue List)" `[VENDOR-DOC]` |
| SQ-F26 | SoftKey press / release: note `30` + (key - 1). The console answers both note-off forms (`8n` and `9n` with velocity 0). It sends notes for a SoftKey only if that key is set to a MIDI note function. | `9n SK 7F` / `8n SK 00` | AS1 3.2 p.10 `[VENDOR-DOC]` |
| SQ-F27 | The parameter number is the 14-bit value **P = MB x 128 + LB**. Absolute data is **D = VC x 128 + VF** (0..16383). Mute and assignment values: `VC 00`, `VF 01` = on, `VF 00` = off. | AS1 1.3, 3.3 `[VENDOR-DOC]`; AS9 `channel-parser.ts:134` (`vc === 0x00 && vf < 0x02`) `[PERMISSIVE-CODE MIT]` |
| SQ-F28 | Inbound parsing: AS9 normalises MIDI running status and ignores unknown messages. **SQ V1.5.x** sends a scene change as `Bn 00 BK  Cn PG 00` (one extra data byte, which running status turns into a second program change); V1.6.x sends it without the extra byte. V1.5.0 fixed NRPNs whose MSB or LSB is `7F` (ID-1620). | AS9 `tokenizer.ts`, `channel-parser.ts:160-166` `[PERMISSIVE-CODE MIT]`; AS4 V1.5.0 `[VENDOR-DOC]`; running status from the SQ `[TO-VERIFY]` (item 4) |
| SQ-F29 | There is no documented message for identity (model, firmware, name), for the current scene, or for the fader law. AS9: "The MIDI API doesn't allow us to query the current scene". | AS1, AS3 (absent) `[VENDOR-DOC]`; AS9 `mixer.ts` (comment on `currentScene`) `[PERMISSIVE-CODE MIT]` |

### 3.2 Strip indices and mutes (same on SQ V1.5, V1.6 and SQ+)
Strip index s used by mutes and "to LR": `Ip1`..`Ip48` = 0x00..0x2F, `Grp1`..`Grp12` = 0x30..0x3B, `FX1Rtn`..`FX8Rtn` =
0x3C..0x43, `LR` = 0x44, `Aux1`..`Aux12` = 0x45..0x50, `FX1Snd`..`FX4Snd` = 0x51..0x54, `Mtx1`..`Mtx3` = 0x55..0x57,
`Mtx4`..`Mtx6` = 0x58..0x5A (V1.6 and SQ+ tables only).

| ID | Parameter | MB LB | Source |
|---|---|---|---|
| SQ-F40 | Mute of strip s | `00 s` (Ip1 `00 00`, LR `00 44`, Aux1 `00 45`, Mtx3 `00 57`) | AS1 p.21; AS2, AS3 mute tables `[VENDOR-DOC]` |
| SQ-F41 | Mute of DCA d (1..8) | `02 (d-1)` | AS1 p.21 `[VENDOR-DOC]` |
| SQ-F42 | Mute group g (1..8) engaged | `04 (g-1)` | AS1 p.21 `[VENDOR-DOC]` |

### 3.3 Levels, pan/balance, assignments: block formulas
Every table in AS1/AS2/AS3 is a row-major block: P = base + (row - 1) x columns + (col - 1). AS9 uses the same rule
("the combined 14-bit MSB:LSB increases by one as you move rightward across a row, then down"). **Pan/balance** of a
send = level P + 0x0800 (MB + 0x10); **assignment** = level P + 0x1000 (MB + 0x20), except where noted.
Bases are MB LB of the first cell. All `[VENDOR-DOC]` (AS1 pp.22-31, AS2, AS3) unless marked.

| ID | Block (rows x columns) | Level base | Pan base | Assign base | Notes |
|---|---|---|---|---|---|
| SQ-F43 | Source s (Ip, Grp, FXRtn) to LR (1 column) | `40 00` + s | `50 00` + s | `60 00` + s | AS1 examples: Ip1 to LR 0 dB `B0 63 40 B0 62 00 B0 06 76 B0 26 5C` |
| SQ-F44a | Ip1..48 to Aux1..12 (48 x 12) | `40 44` | `50 44` | `60 44` | Ip48 to Aux12 = `45 03` |
| SQ-F44b | Grp1..12 to Aux1..12 (12 x 12) | `45 04` | `55 04` | `65 04` | see SQ-F48 |
| SQ-F44c | FX1..8Rtn to Aux1..12 (8 x 12) | `46 14` | `56 14` | `66 14` | |
| SQ-F44d | Ip1..48 to Grp1..12 (48 x 12) | none | none | `66 74` | routing only |
| SQ-F44e | FX1..8Rtn to Grp1..12 (8 x 12) | none | none | `6B 34` | AS9: a level block here was "a onetime A&H MIDI API docs bug" |
| SQ-F44f | Ip1..48 to FX1..4Snd (48 x 4) | `4C 14` | none | `6C 14` | |
| SQ-F44g | Grp1..12 to FX1..4Snd (12 x 4) | `4D 54` | none | `6D 54` | |
| SQ-F44h | FX1..8Rtn to FX1..4Snd (8 x 4) | `4E 04` | none | `6E 04` | AS1 example "FX2Rtn to FX3Snd Assign" `BB 63 6E BB 62 0A` |
| SQ-F45 | **SQ V1.x** matrix sends: LR to Mtx1..3 (1 x 3), Aux1..12 to Mtx1..3 (12 x 3), Grp1..12 to Mtx1..3 (12 x 3) | `4E 24`, `4E 27`, `4E 4B` | `5E 24`, `5E 27`, `5E 4B` | `6E 24`, `6E 27`, `6E 4B` | AS1 p.24, p.27, p.31; AS2 |
| SQ-F46 | **SQ+** matrix sends: LR to Mtx1..6 (1 x 6), Aux1..12 to Mtx1..6 (12 x 6), Grp1..12 to Mtx1..6 (12 x 6) | `4E 24`, `4E 2A`, `4E 72` | `5E 24`, `5E 2A`, `5E 72` | `6E 24`, `6E 2A`, `6E 72` | AS3 images 48079317116305, 48079309677841, 48079317123217. Grp12 to Mtx6 = `4F 39` |

| ID | Fact | Source |
|---|---|---|
| SQ-F47 | **Output masters** (master faders): SQ V1.x: LR `4F 00`, Aux1..12 `4F 01`..`0C`, FX1..4Snd `4F 0D`..`10`, Mtx1..3 `4F 11`..`13`, Mtx4..6 `4F 14`..`16` (V1.6), DCA1..8 `4F 20`..`27`. **SQ+**: LR `4F 50`, Aux `4F 51`..`5C`, FXSnd `4F 5D`..`60`, Mtx1..6 `4F 61`..`66`, DCA1..8 `4F 67`..`6E`. Output balance: SQ `5F 00`, `5F 01`..`0C`, `5F 11`..`13`; SQ+ `5F 50`, `5F 51`..`5C`, `5F 61`..`63`. | AS1 p.24, p.27; AS2 image 47833372120593; AS3 images 48079317118865, 48079317122705 `[VENDOR-DOC]` |
| SQ-F48 | **Document defect, groups to aux:** AS1 and the AS3 image print `-` (no number) for Grp g to Aux a whenever g + a > 12 (a triangle: Grp1 has Aux1-11, Grp12 none). The numbers printed fit the 12 x 12 block exactly (Grp1 Aux1 `45 04`, Grp2 Aux1 `45 10`), and the next block starts at `46 14` = base + 144, so the full block exists in the numbering. AS9 computes every cell by formula. mixcore computes them too but enables a cell only after a get for it is answered (item 6). | AS1 p.23, p.26, p.30; AS3 image 47833301752849 `[VENDOR-DOC]`; AS9 `source-to-sink.ts` `[PERMISSIVE-CODE MIT]`; derived; `[TO-VERIFY]` (item 6) |
| SQ-F49 | SQ V1.6 known issue: "Due to a lack of remaining crosspoint addresses, remote NRPN MIDI control of send levels and assignments to Matrices 4-6 is not currently possible." | AS4 "Known Issues" `[VENDOR-DOC]` |
| SQ-F50 | Overlap hazard, derived from SQ-F45..F47: the same P means different things on SQ and SQ+ (`4E 2A` = Aux2 to Mtx1 on SQ, Aux1 to Mtx1 on SQ+; `4F 00` = LR master on SQ, Grp3 to Mtx3 on SQ+). | derived from AS2, AS3 `[VENDOR-DOC]` |

### 3.4 Unified model mapping
| Unified | SQ name | Fader | Mute | Pan | Name, colour |
|---|---|---|---|---|---|
| input 1..48 | Ip1..48 | to LR (SQ-F43) | SQ-F40 | to LR pan | not in MIDI |
| fxReturn 1..8 | FX1Rtn..8 | to LR | SQ-F40 | to LR balance | not in MIDI |
| bus (group) 1..12 | Grp1..12 | to LR | SQ-F40 | to LR balance | not in MIDI |
| bus (aux mix) 1..12 | Aux1..12 | output master (SQ-F47) | SQ-F40 | output balance | not in MIDI |
| bus (FX send) 1..4 | FX1..4Snd | output master | SQ-F40 | none | not in MIDI |
| matrix 1..3 (6) | Mtx1..3 (6) | output master | SQ-F40 | output balance | not in MIDI |
| main | LR | output master | `00 44` | output balance | not in MIDI |
| dca 1..8 | DCA1..8 | output master ("Control") | SQ-F41 | none | not in MIDI |
| muteGroup 1..8 | MGRP1..8 | none | SQ-F42 | none | not in MIDI |
| Send (input/group/FX return to aux) | level SQ-F44 | on = assignment (SQ-F44, "in the same way as holding the Assign key and pressing a Sel key") | | pan = SQ-F44 pan | pre/post tap not in MIDI |

DCA membership and mute-group membership are **not** in the MIDI protocol, so the effective mute of a channel cannot be
computed (the X32-F39 OR rule needs membership). mixcore shows own mute, DCA and mute-group states side by side and
reports the capability `effectiveMute = unknown` until the MixPad capture (C3) provides membership.

### 3.5 Value encodings and reference points
| ID | Fact | Source |
|---|---|---|
| SQ-F60 | Two NRPN fader laws, chosen on the console (Utility > General > MIDI > "NRPN Fader Law"); the law applies to sending and receiving; it has no effect on increment/decrement (always 1 dB). Linear Taper: 16384 values. Audio Taper: "255 possible values" (AS1, AS2) or "256 possible values" (AS3). | AS1 3.4 pp.12-14; AS3 "NRPN Fader Law" `[VENDOR-DOC]`; count conflict harmless (both laws are table-driven) |
| SQ-F61 | **Linear Taper**: the AS1 reference table ("Example Linear Taper Level Values", p.20, 59 points from -89 to +10 dB plus -inf = `00 00`) is linear in dB: D = round(118.7215 x dB + 15195.94) reproduces every point within 1 LSB (least-squares fit by us). 0 dB = D 15196 (`76 5C`), +10 dB = 16383 (`7F 7F`), -89 dB = 4630 (`24 16`). AS9 uses D = floor(15196 + 118.775 x dB) and says the formula was found "by observation and experimentation". | AS1 p.20 `[VENDOR-DOC]`; fit derived; AS9 `level.ts:14-35,96-103` `[PERMISSIVE-CODE MIT]` |
| SQ-F62 | **Document conflict:** the worked examples in AS1/AS3 encode -20 dB as `63 49` (D 12745), -12 dB as `6B 06` (13702), -24 dB as `5F 57` (12247). Under SQ-F61 these are -20.64, -12.58 and -24.84 dB; the table gives -20 dB = `64 16`, -12 dB = `6B 4B`, -24 dB = `60 3B`. 0 dB agrees (`76 5C`). mixcore follows the table and checks both on hardware. | AS1 p.13 vs p.20; AS3 `[VENDOR-DOC]`; derived; `[TO-VERIFY]` (item 5) |
| SQ-F63 | **Audio Taper**: only `VF` = `00` or `40` occur in the table, so the value is u = 2 x VC + VF/64 (0..255). Table points (AS1 p.20, "Approximate"): -inf 0, -89 3, -80 5, -60 12, -50 16, -40 31, -30 62, -20 93, -10 124, -6 150, -3 173, 0 196 (`62 00`), +3 217, +6 235, +10 255 (`7F 40`). Piecewise; the console fader scale maps -inf, -40, -30, -20, -10, -5, 0, +5, +10 to external MIDI 1, 16, 32, 48, 64, 80, 100, 116, 128 (AS3 figure). | AS1 pp.12-13, p.20; AS3 image 47860314766225 `[VENDOR-DOC]`; u derived |
| SQ-F64 | Values below the -89 dB point (D 1..4629) and their dB meaning are not documented; AS9 treats a decoded level below -89 dB as -inf. mixcore reads D 1..4629 as -inf (reported as raw for diagnostics) and never writes them: -inf is written as `00 00`, the lowest finite write is -89 dB. | AS9 `level.ts:34` `[PERMISSIVE-CODE MIT]`; `[TO-VERIFY]` (item 5) |
| SQ-F65 | **Pan/balance**: D 0 = L100%, 8191 (`3F 7F`) = centre, 16383 (`7F 7F`) = R100%. The AS1 table (L100..R100 in steps) fits p = D / 16383 x 200 - 100 within 0.02 % (e.g. R20% = `4C 65` = 9829, L50% = `1F 7F` = 4095). Inc/dec step size not documented. | AS1 3.5 p.15, p.20 `[VENDOR-DOC]`; fit derived; step `[TO-VERIFY]` (item 5) |

Linear Taper reference points (AS1 p.20; the driver's table test reproduces all 59 points within 1 LSB):

| dB | VC VF | D | dB | VC VF | D | dB | VC VF | D |
|---|---|---|---|---|---|---|---|---|
| -inf | 00 00 | 0 | -30 | 5A 72 | 11634 | -3 | 73 78 | 14840 |
| -89 | 24 16 | 4630 | -20 | 64 16 | 12822 | 0 | 76 5C | 15196 |
| -80 | 2C 42 | 5698 | -12 | 6B 4B | 13771 | +3 | 79 40 | 15552 |
| -60 | 3F 09 | 8073 | -10 | 6D 39 | 14009 | +6 | 7C 24 | 15908 |
| -40 | 51 4F | 10447 | -6 | 71 14 | 14484 | +10 | 7F 7F | 16383 |

Example messages (MIDI channel 1, Linear Taper; from AS1 unless marked):

| What | Bytes |
|---|---|
| Ip1 mute on | `B0 63 00 B0 62 00 B0 06 00 B0 26 01` |
| LR mute off | `B0 63 00 B0 62 44 B0 06 00 B0 26 00` |
| Mute group 4 on, channel 7 | `B6 63 04 B6 62 03 B6 06 00 B6 26 01` |
| Ip1 to LR at 0 dB | `B0 63 40 B0 62 00 B0 06 76 B0 26 5C` |
| Ip40 to Aux5 at -20 dB (table value; AS1 example uses `63 49`, SQ-F62) | `B0 63 44 B0 62 1C B0 06 64 B0 26 16` |
| Get LR mute (table address; AS1's example labelled "LR Mute" uses `00 00`, which is Ip1, SQ-F66) | `B0 63 00 B0 62 44 B0 60 7F` |
| Scene 156, channel 1 | `B0 00 01 C0 1B` |
| SoftKey 1 press / release | `90 30 7F` / `80 30 00` |
| SQ+ LR master at 0 dB | `B0 63 4F B0 62 50 B0 06 76 B0 26 5C` |

| ID | Fact | Source |
|---|---|---|
| SQ-F66 | **Document defect:** the "Get Value" example "LR Mute, Ch1 `B0 63 00 B0 62 00 B0 60 7F`" uses `00 00`, the Ip1 mute (SQ-F40); the mute table gives LR = `00 44`. Same text in AS1 and AS3. | AS1 p.18, p.21; AS3 `[VENDOR-DOC]` |

### 3.6 What the MIDI protocol leaves out (compared with SQ-MixPad)
Everything below is controllable from SQ-MixPad (AS4 lists MixPad features per release) but has no MIDI message in
AS1/AS2/AS3; each is a capture task C3..C9 in section 7.
- Identity: model, firmware, unit name; current scene number and name; scene list, store, recall filters, cue list.
- Names and colours of every strip.
- Preamp: gain, pad, 48 V, trim, polarity, HPF/LPF, delay; input patching and stereo state of channels.
- Processing: gate, 4-band PEQ, compressor and the DEEP add-ons, bus GEQ, FX type and parameters, AMM, ganging.
- Sends: pre/post tap point; Mtx4..6 sends on SQ (SQ-F49).
- DCA and mute-group membership; PAFL/solo; talkback (only via a SoftKey assigned to Talk); signal generator.
- **All meters** (inputs, buses, mains, gain reduction) and RTA.
- User permissions, SQ-Drive transport (the MMC buttons send, they do not receive).

## 4. Echo, push vs poll, client limits

| ID | Fact | Source |
|---|---|---|
| SQ-F70 | Push: the console transmits a change made on the surface, by MixPad or by a scene recall as the same NRPN (or bank/program) message ("The SQ sends and receives absolute ... mute messages"; "The way the SQ responds to and sends level messages ..."). AS9 updates its feedback from these messages. | AS1 3.3, 3.4, 3.1 `[VENDOR-DOC]`; AS9 `mixer.ts` (`#processMixerReplies`) `[PERMISSIVE-CODE MIT]` |
| SQ-F71 | Echo: V1.6.0 fixed an "issue where some received MIDI messages are echoed back to the Sender" (ID-1982). So from V1.6.0 a write is **not** sent back to its own connection; before V1.6.0 some writes were. Whether a write on one MIDI connection is sent to another MIDI connection is not documented. | AS4 V1.6.0 `[VENDOR-DOC]`; cross-connection `[TO-VERIFY]` (item 8) |
| SQ-F72 | Client limits: no MIDI connection limit is documented for SQ (legacy Qu and CQ document exactly one). SQ-MixPad: 3 instances from V1.5.0. Forum: "The maximum number of SQ connected clients is 8, of which a maximum of 3 can be SQ-Mixpad", Mixing Station "included in this"; Mixing Station's own docs count its FoH mode against SQ-MixPad (2 instances) and its personal mode against SQ4You (7). Whether MIDI connections count against the 8, and what the 9th client (or the second MIDI client) gets, is unknown. | AS4 V1.5.0 `[VENDOR-DOC]`; AS11 `[COMMUNITY]`; AS12 `[COMMUNITY]`; `[TO-VERIFY]` (item 9) |
| SQ-F73 | Scene recall at the console is reported as the bank/program message of SQ-F24 (a direct signal, unlike X32). The parameter changes of the recall are probably also transmitted; their extent is unknown. | AS1 3.1 `[VENDOR-DOC]`; AS9 `channel-parser.ts` (`scene` event) `[PERMISSIVE-CODE MIT]`; burst `[TO-VERIFY]` (item 10) |

### Echo and conflict design for `driver/ahsq`
- **One** TCP connection per session (the MIDI slot count is unknown, SQ-F72). Writes are not echoed (SQ-F71), so
  confirmation is by **read-back**: 150 ms after the last write to a parameter (and at most every 150 ms during a
  drag) the driver sends a get (SQ-F23) for it. A reply equal to the last written value (within 1 LSB) is that write's
  echo; a different reply is a console change. A pushed message for a parameter with no pending get is an external
  change. Holds, the 2 s window and the 32-value history follow `x32.md`.
- If item 8 shows that a second MIDI connection receives the first one's writes, the X32 two-socket scheme becomes an
  option, at the cost of a second slot.
- Toggle-type increments (SQ-F21) are never sent: every mute and assignment write is absolute, so a lost or doubled
  message cannot invert the state. Relative level steps (+-1 dB) are never sent either.
- Background re-read: every 15 s while no fader is held, a paced get pass over the parameters in use (as X32's re-read;
  a lost push is unlikely over TCP but a missed console change after a reconnect is not).

## 5. Meters
| ID | Fact | Source |
|---|---|---|
| SQ-F80 | The MIDI protocol has **no meters** in any SQ document. SQ-MixPad shows meters, RTA and gain reduction, so they travel in the MixPad protocol. AS7 (other A&H products) describes their app protocol as TCP 51321 plus a UDP pair per client from 51324 upward; whether SQ uses the same ports is unknown. | AS1, AS2, AS3 (absent) `[VENDOR-DOC]`; AS4 V1.5.0 ("Full RTA support" in MixPad) `[VENDOR-DOC]`; AS7 `[VENDOR-DOC]` (other products); SQ ports `[TO-VERIFY]` (C1, C6) |

Until C6 is done, the `ahsq` driver reports `meters: none` (capability absent, never faked).

## 6. Known quirks and failure modes
- SQ-F16: possible connection reset when writing during the connect burst (one source). The driver's 500 ms quiet start
  avoids it either way.
- SQ-F28: the V1.5.x extra byte after a scene program change; the tokenizer must accept running status.
- SQ-F48, SQ-F62, SQ-F66: three vendor-document defects; the simulator offers each reading as an option.
- SQ-F50: SQ vs SQ+ address overlap. SQ-F15: RackUltra channels reuse "to LR" numbers.
- SQ-F18: MIDI port not released after a network change (fixed V1.6.0).
- SQ+ V2.0.3 known issue: "Preamp gain control is lost for 15-20s after turning off Phantom Power" (ID-1929); matters
  for the preamp capture (C4). AS5 `[VENDOR-DOC]`.
- Full read cost: AS9's full get pass is about 26 KB each way and "briefly clogs" the link (SQ-F16).

## Identity, channel and fader-law detection (design, from SQ-F05, F14, F29, F50, F60)
None of the three is readable over MIDI, and each one, if wrong, makes a write land on a different control. So:
1. The user confirms model family (SQ or SQ+), MIDI channel and fader law once per console (defaults SQ, 1, Linear),
   stored with the console. Discovery (C1) may prefill the model.
2. Checks before any write is enabled, all read-only gets on the configured channel: LR mute `00 44` and DCA1 mute
   `02 00` must answer (a RackUltra channel has no DCA mutes); SQ+ check: a get of `4F 6E` (DCA8 master on SQ+, unused
   on SQ) answers only on SQ+; fader law: any level reply with `VF` not in {`00`, `40`} proves Linear; four or more
   non-zero level replies all with `VF` in {`00`, `40`} indicate Audio Taper. A mismatch with the user's settings makes
   the session read-only with a message; it never "corrects" itself.
3. Audio Taper is supported read-only in v1 (levels shown via the SQ-F63 table); writes need Linear Taper.
All three checks are `[TO-VERIFY]` (item 1).

## Driver shape and simulator (recommendation for core-engineer)
- `driver/internal/ahmidi`: MIDI tokenizer (running status, real-time bytes, SysEx skipped), NRPN encoder/decoder,
  get/inc/dec, scene bank/program, fuzz-tested. Reusable for CQ and Qu-5/6/7 later (same NRPN style per the earlier
  survey; their tables are separate documents).
- `driver/ahsq`: two generated address tables, `sqV1` (AS1 + AS2 additions) and `sqPlus` (AS3), as data built from
  the block bases of section 3.3, with a test that reproduces every example of AS1/AS3 and every printed table cell we
  transcribed (except the defects, which are tested as documented exceptions). Model mapping per section 3.4.
- Connect: TCP 51325 via a `DialFunc` in `driver.Options` (contract change needed, same as `soundcraft-ui.md`);
  quiet start (section 2); identity checks; initial read in priority order (mutes, LR levels and pans, output masters
  and mutes, DCA, mute groups: about 260 gets), then sends per bus on demand when a mix is opened; gets paced at most
  32 in flight (window), measured by item 2.
- `sim/ahsq`: a TCP server on 51325 that parses MIDI with running status and keeps values per (variant, channel, P);
  answers gets; pushes changes to every other client; sends bank/program on scene recall plus a configurable burst;
  options: variant SQ/SQ+, MIDI channel, fader law, echo-to-sender (pre-V1.6), connect burst with reset-on-early-write,
  extra byte after program change, max MIDI clients and the N+1 policy, Active Sensing behaviour, triangle cells absent
  or present, RackUltra channels.

## 7. To verify on hardware (open list)
Setup: an SQ-5/6/7 on V1.6.3 (and, when available, an SQ5+ on V2.0.3) on a dedicated router; a Mac on the same
network; amplifiers off, nothing plugged into the inputs used for preamp tests. Captures go to
`~/Desktop/dybyskz-mix/research/captures/sq/<date>/`. MIDI probes use `nc <ip> 51325 | xxd` with bytes from
`printf` (or a 30-line Go helper the core-engineer adds to `cmd/mixprobe`). `mixcapture` decodes the MIDI stream:
`mixcapture live -i en0 -console <sq-ip> -decoder 51325=midi -out ...`.

1. Identity checks (design section). Read `00 44`, `02 00`, `4F 6E`, `4F 00` on channel 1 and on a channel the console
   does not use; switch the fader law on the console and read Ip1 to LR at 0 dB. Pass: the checks separate SQ and SQ+,
   main and wrong channel, Linear and Audio.
2. SQ-F16 connect. Connect and log 3 s without sending; then reconnect and send a get within 50 ms. Pass: burst content
   and size, whether the early get resets the connection; then the highest get rate (32, 64, 128 in flight) with no
   lost replies over 1 000 gets.
3. SQ-F17 Active Sensing. Idle 60 s: does the console send `FE`? Then send one `FE` and stay silent 20 s; log when (if)
   the connection closes. Pass: keep-alive rules.
4. SQ-F28 running status. Move a fader at the console fast; check whether NRPN groups arrive with or without repeated
   status bytes; recall a scene and log the bytes.
5. SQ-F61/F62/F64/F65 laws. Set Ip1 to LR to -20, -12, -24 and 0 dB at the console (type values in the processing
   screen) and get it; write the table value and the example value for -20 dB and read the console screen; write
   D = 2000 and read the screen; pan R20% and one pan increment. Pass: which document reading is true.
6. SQ-F48 triangle cells. Get Grp1 to Aux12 (`45 0F`) and Grp12 to Aux12 (`46 13`) with groups and mixes configured;
   change one at the console and log the push. Pass: the cells exist.
7. SQ-F04 stereo. Make Aux1+2 a stereo mix and Ip1+2 a stereo pair; write Ip1 to Aux1 level only; log pushes for
   Ip2 and Aux2. Pass: what the console mirrors.
8. SQ-F71 echo. Two MIDI connections A and B. Write Ip1 mute on A; log A and B for 2 s. Pass: no echo on A (V1.6),
   and whether B receives it; repeat on V1.5 firmware if available.
9. SQ-F72 limits. With 3 MixPads and 4 SQ4You connected, open MIDI connections one by one up to 4; log which are
   accepted and served. Pass: the MIDI cap and the N+1 behaviour.
10. SQ-F73 scene recall. Recall a scene at the console; log the bank/program and any parameter burst (count, duration).
11. SQ-F11 security. Enable a user with restricted permissions on the console and log in as that user at the surface;
    from MIDI, write a parameter the user may not change. Probe TCP 51327 with a TLS client (`openssl s_client`).
    **Never try passwords.** Pass: whether MIDI obeys permissions; whether TLS exists.

**Black-box capture of the vendor apps (C-items).** Lawful per invariant 8: our console, its own app used as an
ordinary user, no decompiling, no bypassing of passwords or encryption. Use a test console user with a throwaway
password if user permissions are on; delete real names from exports. Setup C of `docs/tools/mixcapture.md` (a managed
switch mirroring the console port) is preferred because the apps discover the console by broadcast, which setup A's
NAT may block. App versions must match the firmware (SQ-F06).

- **C1 discovery.** `tcpdump -i en5 -n -s 0 -w disc.pcap udp and host <sq-ip>` for 60 s with no app running, then
  while SQ-MixPad and A&H MIDI Control search. Pass: port, period, payload layout (name, model, firmware), and what
  the apps send.
- **C2 MIDI Control.** Connect A&H MIDI Control by TCP/IP to the console while recording
  `mixcapture live -i en5 -console <sq-ip> -decoder 51325=midi`. Pass: what it sends before or instead of NRPN (SysEx
  identity, channel query, `FE`), how it learns the MIDI channel (SQ-F14).
- **C3 MixPad session map.** Record `mixcapture live -i en5 -console <sq-ip> -out ...`; open SQ-MixPad and connect;
  wait for `harvest 1`; `:s baseline`. The "Traffic" section names the TCP and UDP ports (fill SQ-F80). Then one mark
  per control: names and colours of Ip1, Aux1, DCA1; DCA1 and mute group 1 membership of Ip1; preamp gain, pad, 48 V,
  trim, polarity, HPF, delay of Ip1; PEQ band 1-4 freq/gain/Q/type; gate and compressor parameters; send pre/post of
  Ip1 to Aux1; FX1 type and two parameters; PAFL of Ip1; talkback; scene store, scene name, current scene. Use
  `-bin-key` 4, 6, 8 in `mixcapture report` until each control maps to one key.
- **C4 preamp and fader laws.** For Ip1 gain mark `= 0`, `= +20`, `= +40`, `= +60` dB and the minimum (read from the
  console screen), then `:law <key>`; same for Ip1 to LR fader at -inf, -40, -20, -10, 0, +10 dB. Mind ID-1929 on SQ+.
- **C5 scene recall.** `:s before`, recall a scene at the console, `:s after`. Pass: the diff, and how the MixPad
  protocol signals "current scene".
- **C6 meters.** Feed a 1 kHz sine at -20 dBFS (console meter) into Ip1, then -40 dBFS; record 10 s each; identify the
  UDP meter flow, the frame rate, the position of Ip1 in the frame, the scale (two points), and the keep-alive the app
  sends to keep meters flowing.
- **C7 identity.** From C3's first frames: model, firmware, unit name fields.
- **C8 SQ+.** Repeat C1, C3 (short), C6 on an SQ+; record the differences.
- **C9 client limits of the app protocol.** Connect MixPad instances one by one up to 4; log what the 4th receives.

Every C-item result is added here as a `[CAPTURE]` fact with the session path, then to `sim/ahsq`, then to the driver.
A MixPad-protocol driver (`driver/ahsqapp`) is a separate decision for the lead after C3 and C6 are mapped.

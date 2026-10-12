# mixcapture runbook: map a console protocol from its own app's traffic

`cmd/mixcapture` records the traffic between a console and its vendor app (or surface software), decodes it into keys,
and tells you which keys change when you touch a control. A mapping session is: start recording, open the vendor app,
then label what you are about to do and do it. The tool prints the candidates live and writes tables that the
protocol researcher pastes into `docs/protocols/<family>.md` as `[CAPTURE]` facts.

Legal basis: black-box observation of traffic between hardware we use and its own app (CLAUDE.md invariant 8 of the
product repository; lawyer confirmed). Never decompile apps or firmware, never bypass authentication or encryption,
never copy vendor or GPL code into the repository.

## What mixcapture never does
- It never sends anything to a console on its own. The relay (option B) forwards only the bytes the vendor app sent;
  `introspect` sends only the read-only queries of `docs/protocols/yamaha-rcp.md` (its client refuses any other line).
- There is no write probing. To learn a write, make the vendor app (or the surface) do it while recording.
- It never stores a session inside the public mixcore repository (it refuses such a path).

**Personal data.** Captures may contain channel, scene and device names (often people's names) and IP/MAC addresses.
Store sessions only in the private product repository, `research/captures/<console>/<date>/`, and share only the facts
you extract. Every session and report repeats this note.

**Sound safety.** While mapping you will move faders to the top and recall scenes. Turn the amplifiers off or the
mains down, as for `mixprobe`.

## Build
```bash
cd ~/Desktop/mixcore && go build -o ~/bin/mixcapture ./cmd/mixcapture     # pure Go, no cgo
GOOS=linux GOARCH=arm64 go build -o mixcapture-linux-arm64 ./cmd/mixcapture  # e.g. for a Raspberry Pi
```
Live capture (option A) uses `tcpdump`, which ships with macOS (`/usr/sbin/tcpdump`). Reading pcap and pcapng files
(option C) needs nothing else.

## Choose a setup

| Setup | Hardware | Works when | You see |
|---|---|---|---|
| A. Mac as Wi-Fi hotspot (Internet Sharing) | a USB-C/Thunderbolt Ethernet adapter | the vendor app lets you type the console IP (discovery does not cross the Mac's NAT) | every port, both directions, the app's real address |
| B. Relay | nothing | the vendor app lets you type an IP (you type the Mac's) and you know the console's ports | only the relayed ports |
| C. Mirror port / pcap from elsewhere | a managed switch with port mirroring, or any machine that can capture | always, including apps that only discover | everything on the mirrored port |

For an unknown console, start with A or C: the "Traffic" section of `report.md` lists every flow with its ports and
the decoder chosen. Use B when only a relay is possible.

Session directory (all setups): `-out ~/Desktop/dybyskz-mix/research/captures/<console>/<date>` (create the parent
first). It receives `capture.pcap` (or your own pcap/pcapng), `index.jsonl`, `session.json` and the report files.

### A. Mac as a Wi-Fi hotspot (Internet Sharing)
1. Plug the console into the Mac's Ethernet adapter. Find the adapter's service name and give it an address in the
   console's subnet (example: console at 192.168.1.62/24):
   ```bash
   networksetup -listallnetworkservices
   sudo networksetup -setmanual "USB 10/100/1000 LAN" 192.168.1.10 255.255.255.0
   ping -c 3 192.168.1.62
   ```
   (Use the service name your Mac lists. To undo later: `sudo networksetup -setdhcp "USB 10/100/1000 LAN"`.)
2. System Settings > General > Sharing > Internet Sharing (the (i) button): share your connection from the Ethernet
   adapter, to devices using Wi-Fi; set a network name and password in Wi-Fi Options; turn Internet Sharing on.
3. Find the bridge and its address (usually `bridge100`, 192.168.2.1):
   ```bash
   ifconfig bridge100
   ```
4. Join the tablet or phone to the Mac's Wi-Fi network. In the vendor app, enter the console IP manually
   (192.168.1.62). The Mac routes the traffic and sees it on `bridge100`.
5. Record (tcpdump needs root on macOS; mixcapture gives the files back to your user when run with sudo):
   ```bash
   sudo mixcapture live -i bridge100 -console 192.168.1.62 \
     -out ~/Desktop/dybyskz-mix/research/captures/<console>/2026-10-12
   ```
   Without sudo, install Wireshark's ChmodBPF helper (it grants your user capture rights) and run without sudo.

Not verified on hardware yet: whether a given vendor app accepts a console behind the Mac's NAT (meters on some
consoles use broadcast or multicast and may not arrive). If the app refuses, use C.

### B. Transparent relay
The Mac, the console and the tablet are on the same network (the console's router). The vendor app connects to the
Mac, the Mac forwards to the console and records both directions. The console sees the Mac as the client, so the
console's client limit counts the relay.
```bash
ipconfig getifaddr en0          # the Mac's address on that network, e.g. 192.168.1.10
mixcapture relay -console 192.168.1.62 -listen 192.168.1.10 -udp 10024 \
  -out ~/Desktop/dybyskz-mix/research/captures/xr18/2026-10-12
```
Then enter 192.168.1.10 as the console address in the vendor app. Ports: `-udp` and `-tcp` take a list
(`-udp 10023,10024 -tcp 49280`) or `listen:console` pairs when the Mac must listen on another port (`-udp 19024:10024`).
Documented ports: X32/M32 UDP 10023 (`docs/protocols/x32.md`), X Air/M Air UDP 10024 (`xair.md`), Yamaha RCP TCP 49280
(`yamaha-rcp.md`). For other consoles, find the ports with A or C first. If macOS asks whether mixcapture may accept
incoming connections, allow it.

### C. Mirror port, or a pcap from anywhere
Connect the console and the vendor app's access point to a managed switch, mirror the console's port to a port
connected to the capturing machine, and capture:
```bash
sudo tcpdump -i en5 -s 0 -w mirror.pcap host 192.168.1.62     # or save a pcapng from Wireshark
```
If the capturing machine is the Mac, prefer `sudo mixcapture live -i en5 -console 192.168.1.62 -out ...`: you get
live marks. Otherwise analyze the file afterwards, with marks written as times in a text file:
```bash
mixcapture read -pcap mirror.pcap -console 192.168.1.62 -marks marks.txt \
  -out ~/Desktop/dybyskz-mix/research/captures/<console>/2026-10-12
```
`marks.txt`: one mark per line, `+<seconds from the first packet> <label>` or `<RFC 3339 time> <label>`;
`:s <name>` makes a snapshot. Without `-console` the tool guesses the console (the TCP server or the UDP endpoint on a
fixed port that clients talk to) and prints its guess.

## A mapping session
Type into the terminal while recording; Enter sends the line.

| Input | Effect |
|---|---|
| `ch1 fader up` Enter | a mark: keys that change within `-window` (default 5 s) are its candidates, printed live |
| Enter alone | an unlabelled mark |
| `ch1 fader = -10 dB` | a mark with a law point: the key's value at the end of the window is x, -10 is y |
| `:s before recall` | a snapshot of the latest value of every key; from the second one on, the diff is printed |
| `:keys fader` | keys containing "fader" with their latest values |
| `:law /ch/01/mix/fader` | fit a value law from the labelled marks so far |
| `:harvest` | initial-sync harvests so far |
| `:q` | stop, write the report |

Recipe:
1. Start recording, then open the vendor app and connect. The burst of the app's initial sync is the
   **harvest**: when no new key has arrived for `-quiet` (default 2 s) the tool prints `harvest 1 ... N keys` and
   `harvest.csv` lists every key with its value: the near-complete parameter list in one go.
2. `:s baseline`.
3. For each control: type its label, press Enter, then touch that one control within the window. The tool prints
   `candidate: /ch/01/mix/fader 0.75→0.80` (or `NRPN ch1 0x0017 0x5A→0x5F`, a text address, a JSON path, or for binary
   protocols the changed byte offsets `bin a5010017/12 @8 5a→5f`). Keys that also changed before the mark (meters,
   clocks, keep-alives) are flagged as noise and left out.
4. Value laws: set the control on the surface or in the app to known values and mark each one, e.g.
   `ch1 fader = -inf`, `= -30`, `= -10`, `= 0`, `= +10` (move first and mark, or mark and then move: both work). Then
   `:law <key>`. The helper tries a straight line, `a*log10(x) + b` and a piecewise-linear table, and prints the
   simplest one within 0.1 units. Four well-spread points recover a four-segment fader law over the range they span;
   add points near the bottom for the lowest segment.
5. Scenes: `:s before`, recall a scene on the surface or in the app, `:s after`: the diff lists every parameter the
   recall changed.
6. `:q`. Hand `report.md`, `marks.csv`, `keys.csv` and `harvest.csv` to the protocol researcher. Each row has an
   evidence cell `capture.pcap:<byte offset>` (the packet record) and the `pkt` number is in `index.jsonl`.

Re-analysis: `mixcapture report -session <dir>` re-reads the capture and the marks of `index.jsonl` and rewrites the
report, for example with other decoder flags. `mixcapture law -session <dir> -key <key> [-p 0=-inf]` fits a law from
the session's labelled marks plus extra points.

## Decoders and tuning
Each flow (transport, app endpoint, console endpoint) gets one decoder, chosen from its first 16 chunks or 2 s:

| Decoder | Recognizes | Key | Value |
|---|---|---|---|
| `osc` | OSC 1.0 messages and bundles over UDP; size-prefixed OSC over TCP | address | arguments (an address without arguments is a read: no value) |
| `midi` | raw MIDI bytes (running status, real-time bytes, SysEx) | `NRPN ch1 0x0017` (data entry MSB after CC 99/98), `... lsb` (CC 38), `RPN`, `CC ch1 #7`, notes, program, pressure, pitch bend, `SYSEX F0 <first 6 bytes>` | 7-bit value in hex |
| `text` | newline-terminated text lines (RCP style) and JSON lines | the first token with `/` or `:` plus the index tokens after it; verbs and status words before it are dropped, so `set`, `OK set` and `NOTIFY set` of one parameter share a key | the last token |
| `json` | JSON objects anywhere in a byte stream, also inside binary framing | JSON path (`$.channels[0].fader`), or the address of an object with a `path`/`address`/`param`/`key` field and a `value` | the leaf |
| `binary` | anything else; TCP framing is guessed (u8/u16/u32, both byte orders, at offset 0/2/4, counting the rest or the whole frame) | the first `-bin-key` bytes (default 4) in hex plus the frame length | the rest in hex; the report lists the changing offsets |

Flags shared by `relay`, `live`, `read`, `report`, `law`: `-window 5s`, `-quiet 2s`,
`-decoder 10024=osc,49280=text` (force a decoder per console port), `-ports 10024,51325` (analyze only these console
ports), `-bin-key N`, `-sysex-key N`.

## Self-describing consoles (read-only)
Yamaha RCP consoles describe their own parameter table:
```bash
mixcapture introspect -family yamaha-rcp -console 192.168.0.128 \
  -out ~/Desktop/dybyskz-mix/research/captures/tf5/2026-10-12
```
It sends `devinfo productname`, `devinfo devicename`, then `prminfo 0, 1, ...` and `mtrinfo 0, 1, ...` one at a time
until the console answers `ERROR`, a reply times out (`-timeout 2s`) or `-max` entries were read, and writes
`params.csv`, `meters.csv` and `introspect.md`. The conversation is in `capture.pcap`. Facts and open questions:
`docs/protocols/yamaha-rcp.md`. Run it when no vendor app needs the connection slot.

## Troubleshooting
- `live` fails with a permission error: run with `sudo` or install ChmodBPF (see A).
- Nothing decoded: check `-console`; the report's Traffic line counts packets "not to or from the console".
- A flow decoded as `binary` that you know is OSC or text: force it with `-decoder <port>=osc` and run `report`.
- Too many candidates per mark: shorten `-window`, or touch one control per mark.
- Binary keys too coarse or too fine: change `-bin-key` and run `report` again.

## Not verified without hardware
- Every setup was exercised against the simulators on loopback only (`make sim-test`); Internet Sharing, relaying a
  real vendor app and a real mirror port are not verified.
- Yamaha RCP introspection was tested against a scripted stand-in only (T1-T4 of `yamaha-rcp.md`).

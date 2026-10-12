# Yamaha RCP (TCP text protocol): introspection subset

Scope: Yamaha TF, CL, QL, DM3, DM7 and RIVAGE PM consoles over the RCP text protocol (compatibility statement only;
mixcore is not affiliated with or endorsed by any console maker). **This file is a seed, not a driver document.** It
lists only the facts that `cmd/mixcapture introspect -family yamaha-rcp` uses to ask a console for its own parameter
table, read-only. A `driver/yamaharcp` needs a full document from the protocol researcher first.

Facts have IDs `YR-Fnn`. Research date of the sources: 2026-10-11 (the private research note
`docs/research/protocols/yamaha-roland-tascam-avid.md`, section 1a, of the Mixhand product repository). No hardware was
available: **no captures**. Tags as in `xair.md`: `[VENDOR-DOC]`, `[PERMISSIVE-CODE <licence>]`, `[COMMUNITY]`,
`[TO-VERIFY]`, `[CAPTURE]`.

## Sources
- **YS1** Yamaha, "Python Script Template V1.0.0" (`command.py`, `Readme_EN.pdf`, `command_list.pdf`),
  https://usa.yamaha.com/files/download/other_assets/0/1266290/Python_Script_Template_V100.zip `[VENDOR-DOC]`.
- **YS2** `bitfocus/companion-module-yamaha-rcp` v4.0.1, TypeScript, MIT: `src/index.ts`, `paramFuncs.ts`,
  `variables.ts`, and the `schemas/*.txt` parameter-table dumps. https://github.com/bitfocus/companion-module-yamaha-rcp
  `[PERMISSIVE-CODE MIT]`.
- **YS3** BrenekH/yamaha-rcp-docs (unofficial notes, no licence: read for meaning only, nothing copied).
  https://github.com/BrenekH/yamaha-rcp-docs `[COMMUNITY]`.

## Facts

| ID | Fact | Source |
|---|---|---|
| YR-F01 | Transport TCP, port **49280**. | YS1 `command.py` ("Port must be 49280") `[VENDOR-DOC]`; YS2 `RCP_PORT = 49280` |
| YR-F02 | ASCII lines terminated by `\n`; tokens separated by spaces; strings in double quotes. | YS2 `paramFuncs.ts`; YS3 `[COMMUNITY]` |
| YR-F03 | Replies start with `OK`, `OKm`, `NOTIFY` or `ERROR`. `NOTIFY` lines are unsolicited (changes made elsewhere) and can arrive between a request and its reply. | YS2 `paramFuncs.ts`; YS3 `[COMMUNITY]` |
| YR-F04 | `devinfo productname` and `devinfo devicename` are read requests for the model and the device name. | YS2 `variables.ts` `[PERMISSIVE-CODE MIT]` |
| YR-F05 | `prminfo <n>` returns the n-th entry of the console's parameter table, e.g. `OK prminfo 0 "MIXER:Current/InCh/Fader/Level" 40 0 -32768 1000 -32768 "dB" integer any rw 100`: index, address, X count, Y count, min, max, default, unit, type, UI, rw, scale. | YS2 `schemas/*.txt` dumps `[PERMISSIVE-CODE MIT]` |
| YR-F06 | `mtrinfo <n>` returns the n-th entry of the meter table in a similar form (index, address, then fields). The meaning of the fields after the address is not documented. | YS2 `schemas/*.txt` `[PERMISSIVE-CODE MIT]`; field meanings `[TO-VERIFY]` |
| YR-F07 | RCP has no login: any client on the control LAN is accepted. | YS2 (no auth step) `[PERMISSIVE-CODE MIT]` |

## Behaviour mixcapture assumes (to verify on hardware)
- `[TO-VERIFY]` T1. What a console replies to `prminfo <n>` past the end of its table. mixcapture stops at the first
  `ERROR` reply **or** at a reply timeout, and never asks for more than its index cap.
- `[TO-VERIFY]` T2. Whether `prminfo` and `mtrinfo` indexes start at 0 on every model (the dumps start at 0).
- `[TO-VERIFY]` T3. Whether the RCP client limit counts an introspection connection against the Editor/StageMix pool.
  Run introspection when no vendor app needs the slot.
- `[TO-VERIFY]` T4. `mtrinfo` field meanings (YR-F06).

## Read-only rule
mixcapture sends exactly these verbs to a console in introspect mode: `devinfo`, `prminfo`, `mtrinfo`, each with one
argument made of letters or digits. Its client refuses every other line before it reaches the socket (unit-tested).
`set`, `ssrecall_ex`, `scpmode`, `mtrstart` and any other verb are never sent.

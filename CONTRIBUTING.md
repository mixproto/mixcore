# Contributing to mixcore

Thank you for helping. Please read this before opening a pull request.

## Developer Certificate of Origin
Every commit must be signed off (`git commit -s`), which adds:

    Signed-off-by: Your Name <you@example.com>

By signing off you certify the [Developer Certificate of Origin 1.1](https://developercertificate.org/): you wrote the
change or have the right to submit it under the project's license (MPL-2.0).

## Clean-room rules for protocol work
- Allowed sources: vendor documentation, permissively licensed code (MIT, BSD, Apache-2.0), your own packet captures of
  hardware you legally use, and community write-ups.
- Never copy, translate or paraphrase GPL/LGPL/AGPL code, and never use decompiled vendor apps or firmware.
- Every new protocol fact goes into `docs/protocols/<family>.md` with its source and a tag:
  `[OFFICIAL]`, `[CODE]`, `[CAPTURE]`, `[COMMUNITY]` or `[UNVERIFIED]`.

## Engineering rules
- Live-show safety first: no write without a user gesture, no state push on connect, no replay of queued writes.
- Tests are required: conversion tables, driver behaviour against the simulators in `sim/`, fuzz targets for parsers.
- `make lint test sim-test` must pass. Every new Go file starts with the MPL-2.0 header:

      // This Source Code Form is subject to the terms of the Mozilla Public
      // License, v. 2.0. If a copy of the MPL was not distributed with this
      // file, You can obtain one at https://mozilla.org/MPL/2.0/.

## Hardware captures
Captures from consoles we do not own are the most valuable contribution. Open an issue with the console model,
firmware version and a short description; we will tell you how to record safely.

#!/bin/sh
# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
#
# Runs the C smoke test of the c-shared library against a real mixsim on
# loopback. Usage: smoke.sh <dir with libmixcore + libmixcore.h> <mixsim binary>
# Environment: CC (default cc).
set -eu

libdir=$(cd "$1" && pwd)
mixsim=$2
cc=${CC:-cc}
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
sim_pid=""
cleanup() {
	if [ -n "$sim_pid" ]; then kill "$sim_pid" 2>/dev/null || true; fi
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

"$mixsim" -listen 127.0.0.1:0 -demo 2>"$work/mixsim.log" &
sim_pid=$!
port=""
for _ in $(seq 100); do
	port=$(sed -n 's/.*addr=127\.0\.0\.1:\([0-9][0-9]*\).*/\1/p' "$work/mixsim.log" | head -n 1)
	[ -n "$port" ] && break
	sleep 0.05
done
[ -n "$port" ] || { echo "smoke: mixsim did not start" >&2; cat "$work/mixsim.log" >&2; exit 1; }

"$cc" -O2 -std=c11 -Wall -Wextra -Werror -DMIX_CAPITEST -I"$libdir" -o "$work/smoke" "$here/smoke.c" \
	-L"$libdir" -lmixcore -Wl,-rpath,"$libdir" -lpthread
"$work/smoke" "$port"

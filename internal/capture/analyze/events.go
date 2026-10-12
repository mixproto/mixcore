// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package analyze

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/mixproto/mixcore/internal/capture/decode"
)

// Event is a live notification for the operator's terminal.
type Event interface{ isEvent() }

// CandidateFound: a key changed for the first time inside an open mark.
type CandidateFound struct {
	Mark      *Mark
	Candidate Candidate
}

// MarkClosed: a mark's window ended; its candidates are final and sorted.
type MarkClosed struct{ Mark *Mark }

// HarvestDone: an initial-sync burst ended.
type HarvestDone struct{ Harvest *Harvest }

func (CandidateFound) isEvent() {}
func (MarkClosed) isEvent()     {}
func (HarvestDone) isEvent()    {}

// maxShownOffsets bounds the changed byte offsets listed for a binary key.
const maxShownOffsets = 4

// describeChange renders a change as "key from→to". A binary key lists the
// changed byte offsets of the frame: "bin a5010017/12 @8 5a→5f".
func describeChange(k decode.Key, from, to decode.Value) string {
	if k.Kind == decode.KindBinary && from.Present {
		if d, ok := binaryDiff(k, from.Text, to.Text); ok {
			return k.String() + " " + d
		}
	}
	return fmt.Sprintf("%s %s→%s", k.String(), shown(from), shown(to))
}

func shown(v decode.Value) string {
	if !v.Present {
		return "(new)"
	}
	return v.Text
}

// keyBytes is the number of frame bytes in a binary key ("a5010017/12" -> 4).
func keyBytes(k decode.Key) int {
	h, _, _ := strings.Cut(k.ID, "/")
	return len(h) / 2
}

func binaryDiff(k decode.Key, from, to string) (string, bool) {
	a, errA := hex.DecodeString(from)
	b, errB := hex.DecodeString(to)
	if errA != nil || errB != nil || len(a) != len(b) {
		return "", false
	}
	base := keyBytes(k)
	var parts []string
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		if len(parts) == maxShownOffsets {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, fmt.Sprintf("@%d %02x→%02x", base+i, a[i], b[i]))
	}
	return strings.Join(parts, " "), len(parts) > 0
}

// markVarying records which frame offsets differ between two binary values.
func (st *keyState) markVarying(from, to string) {
	a, errA := hex.DecodeString(from)
	b, errB := hex.DecodeString(to)
	if errA != nil || errB != nil {
		return
	}
	if st.varying == nil {
		st.varying = map[int]bool{}
	}
	base := keyBytes(st.key)
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			st.varying[base+i] = true
		}
	}
}

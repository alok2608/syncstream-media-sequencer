// Package playback contains the deterministic playback clock.
//
// Playback is never driven by a chain of timers. Instead, the item that a
// window must be showing is a pure function of three inputs:
//
//	(cycle anchor, current time, playlist item durations)
//
// This is what makes refresh, reconnect and multi-client rendering reliable:
// every client that agrees on the current time resolves the same item.
//
// The same algorithm is implemented in the frontend (frontend/src/utils/playback.js).
// Both implementations are verified against the shared fixture file
// backend/testdata/playback_cases.json so they can never silently diverge.
package playback

// CycleMillis is the 5-hour playback cycle: 5 * 60 * 60 * 1000.
//
// The cycle is a *re-anchor clock*, not a fill. Within one cycle the playlist
// repeats back-to-back for the whole 5 hours; unused time is never converted
// into blank playback. When the cycle boundary is crossed, playback
// deterministically restarts from playlist position 0.
const CycleMillis int64 = 5 * 60 * 60 * 1000

// Item is one resolved playlist entry. Durations are milliseconds so that all
// playback arithmetic stays in exact integers (and matches JavaScript exactly).
type Item struct {
	PlaylistItemID int64 `json:"playlistItemId"`
	MediaID        int64 `json:"mediaId"`
	DurationMillis int64 `json:"durationMillis"`
}

// Timeline is a playlist flattened into cumulative start offsets.
type Timeline struct {
	items   []Item
	starts  []int64 // starts[i] = offset of item i from the start of one pass
	totalMS int64   // duration of one complete pass through the playlist
}

// NewTimeline builds a timeline, ignoring items with a non-positive duration
// so that bad configuration can never produce a zero-length infinite loop.
func NewTimeline(items []Item) Timeline {
	tl := Timeline{
		items:  make([]Item, 0, len(items)),
		starts: make([]int64, 0, len(items)),
	}
	for _, it := range items {
		if it.DurationMillis <= 0 {
			continue
		}
		tl.starts = append(tl.starts, tl.totalMS)
		tl.items = append(tl.items, it)
		tl.totalMS += it.DurationMillis
	}
	return tl
}

// TotalMillis is the length of a single pass through the playlist.
func (t Timeline) TotalMillis() int64 { return t.totalMS }

// Len is the number of playable items.
func (t Timeline) Len() int { return len(t.items) }

// State describes what a window must render at a given instant.
type State struct {
	Item  Item `json:"item"`
	Index int  `json:"index"` // position within the playlist

	// CycleIndex counts 5-hour cycles elapsed since the window's anchor.
	CycleIndex int64 `json:"cycleIndex"`
	// ElapsedInCycleMillis is the offset into the current 5-hour cycle.
	ElapsedInCycleMillis int64 `json:"elapsedInCycleMillis"`
	// LoopIteration counts complete playlist repetitions inside this cycle.
	LoopIteration int64 `json:"loopIteration"`

	ElapsedInItemMillis   int64 `json:"elapsedInItemMillis"`
	RemainingInItemMillis int64 `json:"remainingInItemMillis"`

	// TruncatedByCycle reports that the current item is cut short because the
	// 5-hour boundary arrives before the item would naturally finish.
	TruncatedByCycle bool `json:"truncatedByCycle"`
}

// Resolve returns the state for nowMillis. ok is false when the playlist has
// no playable item, which the caller renders as the fallback state.
func Resolve(t Timeline, anchorMillis, nowMillis int64) (State, bool) {
	if t.totalMS <= 0 {
		return State{}, false
	}

	delta := nowMillis - anchorMillis
	cycleIndex := floorDiv(delta, CycleMillis)
	elapsedInCycle := delta - cycleIndex*CycleMillis

	loopIteration := elapsedInCycle / t.totalMS
	offset := elapsedInCycle % t.totalMS

	index := t.indexAt(offset)
	item := t.items[index]

	elapsedInItem := offset - t.starts[index]
	itemRemaining := item.DurationMillis - elapsedInItem
	cycleRemaining := CycleMillis - elapsedInCycle

	remaining := itemRemaining
	truncated := false
	if cycleRemaining < itemRemaining {
		remaining = cycleRemaining
		truncated = true
	}

	return State{
		Item:                  item,
		Index:                 index,
		CycleIndex:            cycleIndex,
		ElapsedInCycleMillis:  elapsedInCycle,
		LoopIteration:         loopIteration,
		ElapsedInItemMillis:   elapsedInItem,
		RemainingInItemMillis: remaining,
		TruncatedByCycle:      truncated,
	}, true
}

// indexAt binary-searches the cumulative offsets for the item covering offset.
func (t Timeline) indexAt(offset int64) int {
	lo, hi := 0, len(t.starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if t.starts[mid] <= offset {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// floorDiv is integer division that rounds towards negative infinity, so that
// timestamps before the anchor still land on a well-defined cycle.
func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

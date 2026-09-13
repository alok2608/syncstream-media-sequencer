package playback

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fixtureCase mirrors one entry of backend/testdata/playback_cases.json, the
// file shared with the frontend test-suite.
type fixtureCase struct {
	Name         string `json:"name"`
	Items        []Item `json:"items"`
	AnchorMillis int64  `json:"anchorMillis"`
	NowMillis    int64  `json:"nowMillis"`
	Expect       *struct {
		Index                 int   `json:"index"`
		PlaylistItemID        int64 `json:"playlistItemId"`
		MediaID               int64 `json:"mediaId"`
		CycleIndex            int64 `json:"cycleIndex"`
		ElapsedInCycleMillis  int64 `json:"elapsedInCycleMillis"`
		LoopIteration         int64 `json:"loopIteration"`
		ElapsedInItemMillis   int64 `json:"elapsedInItemMillis"`
		RemainingInItemMillis int64 `json:"remainingInItemMillis"`
		TruncatedByCycle      bool  `json:"truncatedByCycle"`
	} `json:"expect"`
}

type fixtureFile struct {
	CycleMillis int64         `json:"cycleMillis"`
	Cases       []fixtureCase `json:"cases"`
}

func loadFixtures(t *testing.T) fixtureFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "playback_cases.json"))
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var f fixtureFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	if len(f.Cases) == 0 {
		t.Fatal("fixture file contains no cases")
	}
	return f
}

func TestCycleMillisMatchesFixture(t *testing.T) {
	f := loadFixtures(t)
	if f.CycleMillis != CycleMillis {
		t.Fatalf("cycle length mismatch: fixture %d, code %d", f.CycleMillis, CycleMillis)
	}
	if CycleMillis != 18_000_000 {
		t.Fatalf("the assignment fixes the cycle at 5 hours (18000s), got %dms", CycleMillis)
	}
}

func TestResolveAgainstSharedFixtures(t *testing.T) {
	f := loadFixtures(t)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			state, ok := Resolve(NewTimeline(tc.Items), tc.AnchorMillis, tc.NowMillis)

			if tc.Expect == nil {
				if ok {
					t.Fatalf("expected no playable item, got index %d", state.Index)
				}
				return
			}
			if !ok {
				t.Fatal("expected a playable item, got none")
			}

			checks := []struct {
				field string
				got   int64
				want  int64
			}{
				{"index", int64(state.Index), int64(tc.Expect.Index)},
				{"playlistItemId", state.Item.PlaylistItemID, tc.Expect.PlaylistItemID},
				{"mediaId", state.Item.MediaID, tc.Expect.MediaID},
				{"cycleIndex", state.CycleIndex, tc.Expect.CycleIndex},
				{"elapsedInCycleMillis", state.ElapsedInCycleMillis, tc.Expect.ElapsedInCycleMillis},
				{"loopIteration", state.LoopIteration, tc.Expect.LoopIteration},
				{"elapsedInItemMillis", state.ElapsedInItemMillis, tc.Expect.ElapsedInItemMillis},
				{"remainingInItemMillis", state.RemainingInItemMillis, tc.Expect.RemainingInItemMillis},
			}
			for _, c := range checks {
				if c.got != c.want {
					t.Errorf("%s = %d, want %d", c.field, c.got, c.want)
				}
			}
			if state.TruncatedByCycle != tc.Expect.TruncatedByCycle {
				t.Errorf("truncatedByCycle = %v, want %v", state.TruncatedByCycle, tc.Expect.TruncatedByCycle)
			}
		})
	}
}

// TestPlaybackIsContinuous walks a whole 5-hour cycle item by item and asserts
// there is never a gap, an overlap, or a stall between playlist items.
func TestPlaybackIsContinuous(t *testing.T) {
	items := []Item{
		{PlaylistItemID: 1, MediaID: 101, DurationMillis: 10_000},
		{PlaylistItemID: 2, MediaID: 102, DurationMillis: 20_000},
		{PlaylistItemID: 3, MediaID: 103, DurationMillis: 30_000},
	}
	tl := NewTimeline(items)

	var now int64
	transitions := 0
	for now < CycleMillis {
		state, ok := Resolve(tl, 0, now)
		if !ok {
			t.Fatalf("playback stopped at %dms", now)
		}
		if state.RemainingInItemMillis <= 0 {
			t.Fatalf("non-advancing state at %dms", now)
		}
		// The instant the current item ends, the next item must already own it.
		next := now + state.RemainingInItemMillis
		if next < CycleMillis {
			following, ok := Resolve(tl, 0, next)
			if !ok {
				t.Fatalf("playback stopped at handover %dms", next)
			}
			if following.ElapsedInItemMillis != 0 {
				t.Fatalf("gap at %dms: next item started %dms in", next, following.ElapsedInItemMillis)
			}
			wantIndex := (state.Index + 1) % tl.Len()
			if following.Index != wantIndex {
				t.Fatalf("at %dms expected item index %d, got %d", next, wantIndex, following.Index)
			}
		}
		now = next
		transitions++
	}
	// 18000s / 60s per pass * 3 items.
	if transitions != 900 {
		t.Fatalf("expected 900 item transitions in one cycle, got %d", transitions)
	}
}

// TestCycleBoundaryNeverBlanks asserts the remaining cycle time is filled by
// looping the playlist rather than by blank playback.
func TestCycleBoundaryNeverBlanks(t *testing.T) {
	// 7s playlist: 18000000 / 7000 = 2571 whole passes with 3000ms left over.
	tl := NewTimeline([]Item{{PlaylistItemID: 7, MediaID: 701, DurationMillis: 7_000}})

	for _, now := range []int64{17_997_000, 17_998_500, 17_999_999} {
		state, ok := Resolve(tl, 0, now)
		if !ok {
			t.Fatalf("no item at %dms - the tail of the cycle went blank", now)
		}
		if state.Item.MediaID != 701 {
			t.Fatalf("unexpected media %d at %dms", state.Item.MediaID, now)
		}
	}

	// And the very next millisecond re-anchors to the top of the playlist.
	state, ok := Resolve(tl, 0, CycleMillis)
	if !ok {
		t.Fatal("no item at the cycle boundary")
	}
	if state.CycleIndex != 1 || state.ElapsedInItemMillis != 0 || state.LoopIteration != 0 {
		t.Fatalf("cycle did not re-anchor cleanly: %+v", state)
	}
}

func TestTimelineSkipsUnplayableItems(t *testing.T) {
	tl := NewTimeline([]Item{
		{PlaylistItemID: 1, MediaID: 101, DurationMillis: 5_000},
		{PlaylistItemID: 2, MediaID: 102, DurationMillis: 0},
		{PlaylistItemID: 3, MediaID: 103, DurationMillis: -1},
	})
	if tl.Len() != 1 {
		t.Fatalf("expected 1 playable item, got %d", tl.Len())
	}
	if tl.TotalMillis() != 5_000 {
		t.Fatalf("expected total 5000ms, got %d", tl.TotalMillis())
	}
}

func TestFloorDiv(t *testing.T) {
	cases := []struct{ a, b, want int64 }{
		{0, 10, 0},
		{9, 10, 0},
		{10, 10, 1},
		{-1, 10, -1},
		{-10, 10, -1},
		{-11, 10, -2},
	}
	for _, c := range cases {
		if got := floorDiv(c.a, c.b); got != c.want {
			t.Errorf("floorDiv(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

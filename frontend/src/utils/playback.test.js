import { describe, expect, it } from 'vitest';

import fixtures from '../../../backend/testdata/playback_cases.json';
import {
  CYCLE_MILLIS,
  buildTimeline,
  occurrenceKey,
  resolvePlayback,
  timelineFromPlaylist,
} from './playback.js';

describe('shared fixtures', () => {
  it('agrees with the backend on the cycle length', () => {
    expect(CYCLE_MILLIS).toBe(fixtures.cycleMillis);
    expect(CYCLE_MILLIS).toBe(18_000_000);
  });

  // Every case in this file is also asserted by the Go test-suite, which is
  // what guarantees the two playback clocks stay identical.
  for (const testCase of fixtures.cases) {
    it(testCase.name, () => {
      const state = resolvePlayback(
        buildTimeline(testCase.items),
        testCase.anchorMillis,
        testCase.nowMillis,
      );

      if (testCase.expect === null) {
        expect(state).toBeNull();
        return;
      }

      expect(state).not.toBeNull();
      expect(state.index).toBe(testCase.expect.index);
      expect(state.item.playlistItemId).toBe(testCase.expect.playlistItemId);
      expect(state.item.mediaId).toBe(testCase.expect.mediaId);
      expect(state.cycleIndex).toBe(testCase.expect.cycleIndex);
      expect(state.elapsedInCycleMillis).toBe(testCase.expect.elapsedInCycleMillis);
      expect(state.loopIteration).toBe(testCase.expect.loopIteration);
      expect(state.elapsedInItemMillis).toBe(testCase.expect.elapsedInItemMillis);
      expect(state.remainingInItemMillis).toBe(testCase.expect.remainingInItemMillis);
      expect(state.truncatedByCycle).toBe(testCase.expect.truncatedByCycle);
    });
  }
});

describe('continuous playback', () => {
  const playlist = buildTimeline([
    { playlistItemId: 1, mediaId: 101, durationMillis: 10_000 },
    { playlistItemId: 2, mediaId: 102, durationMillis: 20_000 },
    { playlistItemId: 3, mediaId: 103, durationMillis: 30_000 },
  ]);

  it('hands over from one item to the next with no gap and no stall', () => {
    let now = 0;
    let transitions = 0;

    while (now < CYCLE_MILLIS) {
      const state = resolvePlayback(playlist, 0, now);
      expect(state).not.toBeNull();
      expect(state.remainingInItemMillis).toBeGreaterThan(0);

      const next = now + state.remainingInItemMillis;
      if (next < CYCLE_MILLIS) {
        const following = resolvePlayback(playlist, 0, next);
        // The instant an item ends, the next one already owns the clock.
        expect(following.elapsedInItemMillis).toBe(0);
        expect(following.index).toBe((state.index + 1) % playlist.items.length);
      }
      now = next;
      transitions += 1;
    }
    // 18000s / 60s per pass * 3 items.
    expect(transitions).toBe(900);
  });

  it('loops the playlist rather than blanking the rest of the cycle', () => {
    // A 7s playlist divides the cycle unevenly: 2571 passes plus 3000ms.
    const short = buildTimeline([{ playlistItemId: 7, mediaId: 701, durationMillis: 7_000 }]);

    for (const now of [17_997_000, 17_998_500, 17_999_999]) {
      const state = resolvePlayback(short, 0, now);
      expect(state).not.toBeNull();
      expect(state.item.mediaId).toBe(701);
    }
  });

  it('re-anchors to playlist position 0 at the 5 hour boundary', () => {
    const last = resolvePlayback(playlist, 0, CYCLE_MILLIS - 1);
    expect(last.cycleIndex).toBe(0);
    expect(last.loopIteration).toBe(299);

    const first = resolvePlayback(playlist, 0, CYCLE_MILLIS);
    expect(first.cycleIndex).toBe(1);
    expect(first.index).toBe(0);
    expect(first.elapsedInItemMillis).toBe(0);
    expect(first.loopIteration).toBe(0);
  });

  it('resolves the same item regardless of when a client joins', () => {
    // Two clients computing at the same instant must agree; this is what makes
    // a page refresh seamless.
    const instant = 1_234_567;
    expect(resolvePlayback(playlist, 0, instant)).toEqual(resolvePlayback(playlist, 0, instant));
  });
});

describe('timelineFromPlaylist', () => {
  const apiPlaylist = [
    { id: 11, position: 0, media: { id: 1, name: 'M1', type: 'image', durationSeconds: 10 } },
    { id: 12, position: 1, media: { id: 2, name: 'M7', type: 'blank', durationSeconds: 5 } },
    { id: 13, position: 2, media: { id: 3, name: 'M3', type: 'video', durationSeconds: 30 } },
  ];

  it('converts API playlists into the millisecond timeline', () => {
    const timeline = timelineFromPlaylist(apiPlaylist);
    expect(timeline.totalMillis).toBe(45_000);
    expect(timeline.starts).toEqual([0, 10_000, 15_000]);
  });

  it('treats a configured blank item as ordinary playable media', () => {
    const timeline = timelineFromPlaylist(apiPlaylist);
    const state = resolvePlayback(timeline, 0, 12_000);
    expect(state.item.mediaId).toBe(2);
    expect(state.remainingInItemMillis).toBe(3_000);
  });

  it('returns no state for an empty or missing playlist', () => {
    expect(resolvePlayback(timelineFromPlaylist([]), 0, 5_000)).toBeNull();
    expect(resolvePlayback(timelineFromPlaylist(undefined), 0, 5_000)).toBeNull();
  });
});

describe('occurrenceKey', () => {
  const timeline = buildTimeline([
    { playlistItemId: 1, mediaId: 101, durationMillis: 10_000 },
    { playlistItemId: 2, mediaId: 102, durationMillis: 20_000 },
  ]);

  it('is stable while one item plays', () => {
    const a = occurrenceKey(resolvePlayback(timeline, 0, 1_000));
    const b = occurrenceKey(resolvePlayback(timeline, 0, 9_999));
    expect(a).toBe(b);
  });

  it('changes when the item changes and when the playlist comes round again', () => {
    const first = occurrenceKey(resolvePlayback(timeline, 0, 1_000));
    const second = occurrenceKey(resolvePlayback(timeline, 0, 11_000));
    const firstAgain = occurrenceKey(resolvePlayback(timeline, 0, 31_000));

    expect(second).not.toBe(first);
    // Same item, next pass: the key must change so a video restarts.
    expect(firstAgain).not.toBe(first);
  });

  it('handles the empty state', () => {
    expect(occurrenceKey(null)).toBe('empty');
  });
});

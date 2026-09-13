import { describe, expect, it } from 'vitest';

import { SYNC_PHASE, isSyncOnScreen, resolveSyncPhase } from './sync.js';

/** A sync that starts one second from `now` and runs for 30 seconds. */
const eventAt = (startMillis, durationMillis = 30_000) => ({
  id: 1,
  media: { id: 2, name: 'M2', type: 'image', durationSeconds: 20 },
  startAt: new Date(startMillis).toISOString(),
  endAt: new Date(startMillis + durationMillis).toISOString(),
});

describe('resolveSyncPhase', () => {
  const start = 1_700_000_000_000;
  const event = eventAt(start);

  it('is idle when there is no event', () => {
    const result = resolveSyncPhase(null, start);
    expect(result.phase).toBe(SYNC_PHASE.idle);
    expect(isSyncOnScreen(result.phase)).toBe(false);
  });

  it('is pending during the lead-in, so clients can preload', () => {
    const result = resolveSyncPhase(event, start - 800);
    expect(result.phase).toBe(SYNC_PHASE.pending);
    expect(result.untilStartMillis).toBe(800);
    // Not on screen yet: this is what keeps windows from starting early.
    expect(isSyncOnScreen(result.phase)).toBe(false);
  });

  it('becomes active exactly at startAt', () => {
    expect(resolveSyncPhase(event, start - 1).phase).toBe(SYNC_PHASE.pending);
    expect(resolveSyncPhase(event, start).phase).toBe(SYNC_PHASE.active);
    expect(isSyncOnScreen(resolveSyncPhase(event, start).phase)).toBe(true);
  });

  it('reports the remaining time while active', () => {
    const result = resolveSyncPhase(event, start + 12_000);
    expect(result.phase).toBe(SYNC_PHASE.active);
    expect(result.remainingMillis).toBe(18_000);
    expect(result.progress).toBeCloseTo(0.4);
  });

  it('ends exactly at endAt and stays ended', () => {
    expect(resolveSyncPhase(event, start + 29_999).phase).toBe(SYNC_PHASE.active);
    expect(resolveSyncPhase(event, start + 30_000).phase).toBe(SYNC_PHASE.ended);
    expect(resolveSyncPhase(event, start + 600_000).phase).toBe(SYNC_PHASE.ended);
    expect(isSyncOnScreen(SYNC_PHASE.ended)).toBe(false);
  });

  // This is the property that makes sync actually synchronous: two clients that
  // received the message at different times still agree on the phase, because
  // neither uses its arrival time for anything.
  it('resolves identically for clients that received the event at different times', () => {
    const instant = start + 5_000;
    const earlyClient = resolveSyncPhase(event, instant);
    const lateClient = resolveSyncPhase(eventAt(start), instant);

    expect(earlyClient).toEqual(lateClient);
  });

  // A client that loads mid-sync fetches the same event and lands mid-way.
  it('lets a refreshing client join at the right offset', () => {
    const joined = resolveSyncPhase(event, start + 21_500);
    expect(joined.phase).toBe(SYNC_PHASE.active);
    expect(joined.remainingMillis).toBe(8_500);
  });

  it('treats a client with a skewed clock correctly once corrected', () => {
    // The browser clock is 45s fast; the corrected server time is what counts.
    const browserNow = start + 45_000 + 3_000;
    const offset = -45_000;
    const result = resolveSyncPhase(event, browserNow + offset);
    expect(result.phase).toBe(SYNC_PHASE.active);
    expect(result.remainingMillis).toBe(27_000);
  });
});

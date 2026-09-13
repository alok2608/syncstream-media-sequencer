/**
 * Deterministic playback clock.
 *
 * Playback is never driven by a chain of `setTimeout` calls. The item a window
 * must show is a pure function of three inputs:
 *
 *     (cycle anchor, current time, playlist item durations)
 *
 * That is what makes refresh, reconnect and multi-client rendering reliable:
 * any client that agrees on the current time resolves the same item.
 *
 * This is a mirror of the Go implementation in backend/internal/playback. Both
 * are verified against the shared fixtures in backend/testdata/playback_cases.json,
 * so the two can never silently drift apart.
 *
 * All arithmetic is in integer milliseconds, which JavaScript represents
 * exactly, so the two implementations agree bit for bit.
 */

/**
 * The 5-hour playback cycle: 5 * 60 * 60 * 1000.
 *
 * The cycle is a *re-anchor clock*, not a fill. Within one cycle the playlist
 * repeats back-to-back for the whole five hours; unused time is never turned
 * into blank playback. Crossing the boundary restarts playback deterministically
 * at playlist position 0.
 */
export const CYCLE_MILLIS = 5 * 60 * 60 * 1000;

/**
 * Flattens a playlist into cumulative start offsets.
 *
 * Items with a non-positive duration are dropped so bad configuration can never
 * produce a zero-length infinite loop.
 *
 * @param {Array<{playlistItemId: number, mediaId: number, durationMillis: number}>} items
 * @returns {{items: Array, starts: number[], totalMillis: number}}
 */
export function buildTimeline(items) {
  const timeline = { items: [], starts: [], totalMillis: 0 };

  for (const item of items ?? []) {
    if (!(item.durationMillis > 0)) continue;
    timeline.starts.push(timeline.totalMillis);
    timeline.items.push(item);
    timeline.totalMillis += item.durationMillis;
  }
  return timeline;
}

/**
 * Converts an API playlist into timeline items.
 *
 * @param {Array} playlist playlist items as returned by the backend
 */
export function timelineFromPlaylist(playlist) {
  return buildTimeline(
    (playlist ?? []).map((item) => ({
      playlistItemId: item.id,
      mediaId: item.media.id,
      durationMillis: item.media.durationSeconds * 1000,
    })),
  );
}

/**
 * Resolves what a window must render at `nowMillis`.
 *
 * @returns {null|{
 *   item: object, index: number, cycleIndex: number, elapsedInCycleMillis: number,
 *   loopIteration: number, elapsedInItemMillis: number, remainingInItemMillis: number,
 *   truncatedByCycle: boolean
 * }} null when the playlist has no playable item, which the caller renders as
 *   the fallback state.
 */
export function resolvePlayback(timeline, anchorMillis, nowMillis) {
  if (!timeline || timeline.totalMillis <= 0) return null;

  const delta = nowMillis - anchorMillis;
  const cycleIndex = floorDiv(delta, CYCLE_MILLIS);
  const elapsedInCycleMillis = delta - cycleIndex * CYCLE_MILLIS;

  const loopIteration = Math.floor(elapsedInCycleMillis / timeline.totalMillis);
  const offset = elapsedInCycleMillis % timeline.totalMillis;

  const index = indexAt(timeline, offset);
  const item = timeline.items[index];

  const elapsedInItemMillis = offset - timeline.starts[index];
  const itemRemaining = item.durationMillis - elapsedInItemMillis;
  const cycleRemaining = CYCLE_MILLIS - elapsedInCycleMillis;

  const truncatedByCycle = cycleRemaining < itemRemaining;

  return {
    item,
    index,
    cycleIndex,
    elapsedInCycleMillis,
    loopIteration,
    elapsedInItemMillis,
    remainingInItemMillis: truncatedByCycle ? cycleRemaining : itemRemaining,
    truncatedByCycle,
  };
}

/**
 * A stable identifier for one *occurrence* of a playlist item. It changes every
 * time the item comes round again, which is how the player knows to restart a
 * video rather than let the previous pass keep playing.
 */
export function occurrenceKey(state) {
  if (!state) return 'empty';
  return `${state.cycleIndex}:${state.loopIteration}:${state.index}:${state.item.playlistItemId}`;
}

/** Binary-searches the cumulative offsets for the item covering `offset`. */
function indexAt(timeline, offset) {
  let lo = 0;
  let hi = timeline.starts.length - 1;
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2);
    if (timeline.starts[mid] <= offset) lo = mid;
    else hi = mid - 1;
  }
  return lo;
}

/**
 * Integer division rounding towards negative infinity, so a timestamp before
 * the anchor still lands on a well-defined cycle.
 */
function floorDiv(a, b) {
  return Math.floor(a / b);
}

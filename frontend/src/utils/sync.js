/**
 * Global sync is resolved from absolute, backend-generated timestamps.
 *
 * Clients deliberately do NOT start a local timer when the SYNC_STARTED message
 * arrives: messages reach different browsers milliseconds apart, and that spread
 * would show up directly as windows falling out of step. Instead every client
 * compares the shared `startAt` / `endAt` instants against its own
 * offset-corrected view of the server clock, so they converge on the same
 * moment no matter when - or whether - they received the message.
 *
 * The same reasoning covers refresh and late joiners: a page that loads during
 * an active sync fetches the event and lands at exactly the right offset.
 */

export const SYNC_PHASE = {
  /** No override exists. */
  idle: 'idle',
  /** The override exists but has not started; clients preload during this window. */
  pending: 'pending',
  /** The override is on screen. */
  active: 'active',
  /** The override has finished; windows are back on their own playlists. */
  ended: 'ended',
};

/**
 * Resolves the phase of a sync event at a given server time.
 *
 * @param {null|{startAt: string, endAt: string}} event
 * @param {number} serverNowMillis current time on the server's clock
 */
export function resolveSyncPhase(event, serverNowMillis) {
  if (!event) return { phase: SYNC_PHASE.idle, remainingMillis: 0, untilStartMillis: 0, progress: 0 };

  const startAt = new Date(event.startAt).getTime();
  const endAt = new Date(event.endAt).getTime();

  if (serverNowMillis < startAt) {
    return {
      phase: SYNC_PHASE.pending,
      remainingMillis: endAt - startAt,
      untilStartMillis: startAt - serverNowMillis,
      progress: 0,
    };
  }
  if (serverNowMillis >= endAt) {
    return { phase: SYNC_PHASE.ended, remainingMillis: 0, untilStartMillis: 0, progress: 1 };
  }

  const total = endAt - startAt;
  return {
    phase: SYNC_PHASE.active,
    remainingMillis: endAt - serverNowMillis,
    untilStartMillis: 0,
    progress: total > 0 ? (serverNowMillis - startAt) / total : 1,
  };
}

/** True when the override should currently be displayed on every window. */
export function isSyncOnScreen(phase) {
  return phase === SYNC_PHASE.active;
}

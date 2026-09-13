import { useCallback, useRef, useState } from 'react';

import { api } from '../api/client.js';

/**
 * Estimates this browser's offset from the server clock.
 *
 * Every playback decision - which playlist item is current, and whether a
 * global sync has started - is made against absolute server timestamps. A
 * browser whose clock is minutes off would otherwise render the wrong item, so
 * the offset is measured rather than assumed.
 *
 * The estimate uses the NTP formula with a single server timestamp:
 *
 *     offset = serverTime - (sentAt + receivedAt) / 2
 *
 * Samples are collected over both REST and the WebSocket. The sample with the
 * lowest round trip wins, because a fast exchange brackets the server's clock
 * most tightly.
 */
const MAX_SAMPLES = 8;

export function useServerClock() {
  const samples = useRef([]);
  const [clock, setClock] = useState({ offsetMillis: 0, rttMillis: null, synced: false });

  /**
   * Records one measurement.
   *
   * @param {number} serverMillis the server's clock at the moment it replied
   * @param {number} sentAt Date.now() when the request left the browser
   * @param {number} receivedAt Date.now() when the reply arrived
   */
  const recordSample = useCallback((serverMillis, sentAt, receivedAt) => {
    const rtt = receivedAt - sentAt;
    // Discard nonsense measurements (a suspended tab can produce them).
    if (!Number.isFinite(serverMillis) || rtt < 0 || rtt > 10_000) return;

    const offset = serverMillis - (sentAt + receivedAt) / 2;
    samples.current = [...samples.current, { offset, rtt }].slice(-MAX_SAMPLES);

    const best = samples.current.reduce((a, b) => (b.rtt < a.rtt ? b : a));
    setClock({ offsetMillis: Math.round(best.offset), rttMillis: best.rtt, synced: true });
  }, []);

  /** Takes a fresh sample over REST. Used at startup and while the socket is down. */
  const syncOverHttp = useCallback(
    async (signal) => {
      const sentAt = Date.now();
      const data = await api.getTime(signal);
      recordSample(data.serverTimeMillis, sentAt, Date.now());
    },
    [recordSample],
  );

  return { ...clock, recordSample, syncOverHttp };
}

/**
 * Converts a browser timestamp into server time using a measured offset.
 * Exported as a plain function so it can be used outside React rendering.
 */
export function toServerMillis(clientMillis, offsetMillis) {
  return clientMillis + offsetMillis;
}

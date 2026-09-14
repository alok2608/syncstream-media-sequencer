import { useCallback, useEffect, useRef, useState } from 'react';

import { WS_URL, api } from '../api/client.js';
import { WS_STATUS, useWebSocket } from './useWebSocket.js';

/** How often to poll while the WebSocket is unavailable. */
const POLL_INTERVAL_MILLIS = 5_000;

/**
 * Owns the playback configuration: windows with their playlists, the media
 * library and the live global sync.
 *
 * State arrives two ways and both are handled identically:
 *   - REST, on load and whenever the socket is down (the fallback path);
 *   - WebSocket events, which patch the state in place for instant updates.
 *
 * @param {{recordSample: Function, syncOverHttp: Function}} clock
 */
export function useSequencerState(clock) {
  const [state, setState] = useState({
    windows: [],
    media: [],
    activeSync: null,
    loading: true,
    error: null,
  });

  const { recordSample, syncOverHttp } = clock;
  // Kept in a ref so the polling effect does not restart on every clock sample.
  const clockRef = useRef({ recordSample, syncOverHttp });
  clockRef.current = { recordSample, syncOverHttp };

  /** Replaces the whole configuration from a snapshot payload. */
  const applySnapshot = useCallback((snapshot) => {
    setState((previous) => ({
      ...previous,
      windows: snapshot.windows ?? [],
      media: snapshot.media ?? [],
      activeSync: snapshot.activeSync ?? null,
      loading: false,
      error: null,
    }));
  }, []);

  /** Fetches the full snapshot and takes a clock sample from the same round trip. */
  const refresh = useCallback(
    async (signal) => {
      const sentAt = Date.now();
      const snapshot = await api.getState(signal);
      clockRef.current.recordSample(new Date(snapshot.serverTime).getTime(), sentAt, Date.now());
      applySnapshot(snapshot);
      return snapshot;
    },
    [applySnapshot],
  );

  /** Replaces one window in place, preserving the order of the grid. */
  const upsertWindow = useCallback((incoming) => {
    setState((previous) => {
      const exists = previous.windows.some((w) => w.id === incoming.id);
      return {
        ...previous,
        windows: exists
          ? previous.windows.map((w) => (w.id === incoming.id ? incoming : w))
          : [...previous.windows, incoming],
      };
    });
  }, []);

  const handleEvent = useCallback(
    (event) => {
      // Every event carries the server clock, so the estimate keeps improving
      // for as long as the page is open.
      if (event.serverTime) {
        const serverMillis = new Date(event.serverTime).getTime();
        if (event.type === 'PONG' && event.payload?.clientTime) {
          recordSample(serverMillis, event.payload.clientTime, Date.now());
        }
      }

      switch (event.type) {
        case 'HELLO':
          // The greeting carries a full snapshot, so a reconnecting client is
          // immediately consistent without an extra REST round trip.
          if (event.payload?.windows) applySnapshot(event.payload);
          break;

        case 'PLAYLIST_UPDATED':
        case 'WINDOW_CREATED':
          upsertWindow(event.payload);
          break;

        case 'MEDIA_CREATED':
          setState((previous) =>
            previous.media.some((m) => m.id === event.payload.id)
              ? previous
              : { ...previous, media: [...previous.media, event.payload] },
          );
          break;

        case 'SYNC_STARTED':
          setState((previous) => ({ ...previous, activeSync: event.payload }));
          break;

        case 'SYNC_ENDED':
        case 'SYNC_CANCELLED':
          setState((previous) => ({ ...previous, activeSync: null }));
          break;

        default:
          break;
      }
    },
    [applySnapshot, recordSample, upsertWindow],
  );

  /**
   * Refreshes and records whether the backend is currently reachable, so the
   * status bar reflects an outage instead of showing a stale "connected".
   * The last known configuration is kept either way - playback never stops
   * just because the backend went away.
   */
  const refreshAndTrackReachability = useCallback(
    async (signal) => {
      try {
        await refresh(signal);
      } catch (error) {
        if (error.name === 'AbortError') return;
        setState((previous) => ({ ...previous, loading: false, error }));
      }
    },
    [refresh],
  );

  const { status: socketStatus } = useWebSocket(WS_URL, {
    onEvent: handleEvent,
    // A reconnect may have missed events; re-read the authoritative state.
    onOpen: () => refreshAndTrackReachability(),
  });

  // Initial load.
  useEffect(() => {
    const controller = new AbortController();
    refreshAndTrackReachability(controller.signal);
    return () => controller.abort();
  }, [refreshAndTrackReachability]);

  // Polling fallback: only while the socket is not open, so a healthy
  // connection costs nothing.
  //
  // This deliberately depends on a boolean rather than on socketStatus itself.
  // While reconnecting, the status flips between "closed" and "connecting" on
  // every attempt; keying the effect on the raw status would tear down and
  // recreate the interval each time and the poll would never actually fire.
  const socketOpen = socketStatus === WS_STATUS.open;

  useEffect(() => {
    if (socketOpen) return undefined;

    const controller = new AbortController();
    const id = setInterval(() => {
      refreshAndTrackReachability(controller.signal);
      clockRef.current.syncOverHttp(controller.signal).catch(() => {});
    }, POLL_INTERVAL_MILLIS);

    return () => {
      controller.abort();
      clearInterval(id);
    };
  }, [socketOpen, refreshAndTrackReachability]);

  // The reachability-tracking variant is what callers get: it never rejects,
  // so a UI callback cannot turn a failed refresh into an unhandled rejection.
  return { ...state, socketStatus, refresh: refreshAndTrackReachability };
}

import { useCallback, useEffect, useRef, useState } from 'react';

/** Connection states surfaced to the UI. */
export const WS_STATUS = {
  connecting: 'connecting',
  open: 'open',
  closed: 'closed',
};

const BASE_RETRY_MILLIS = 1_000;
const MAX_RETRY_MILLIS = 15_000;
/** How often to exchange a ping, which both keeps the clock fresh and the socket alive. */
const PING_INTERVAL_MILLIS = 10_000;

/**
 * Maintains a WebSocket with automatic reconnection.
 *
 * The socket is a delivery optimisation, not a source of truth: everything it
 * carries can also be fetched over REST. That is why a disconnect degrades to
 * polling instead of stopping playback - playback itself runs entirely off the
 * local cycle clock and keeps going regardless.
 *
 * @param {string} url
 * @param {{onEvent: (event: object) => void, onOpen?: () => void}} handlers
 */
export function useWebSocket(url, { onEvent, onOpen }) {
  const [status, setStatus] = useState(WS_STATUS.connecting);

  const socketRef = useRef(null);
  const retryRef = useRef(0);
  const timersRef = useRef({ reconnect: null, ping: null });
  // Handlers are kept in refs so re-renders never tear down a healthy socket.
  const handlersRef = useRef({ onEvent, onOpen });
  handlersRef.current = { onEvent, onOpen };

  /** Sends a JSON message if the socket is open; silently drops it otherwise. */
  const send = useCallback((message) => {
    const socket = socketRef.current;
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify(message));
      return true;
    }
    return false;
  }, []);

  useEffect(() => {
    let disposed = false;

    const clearTimers = () => {
      clearTimeout(timersRef.current.reconnect);
      clearInterval(timersRef.current.ping);
    };

    const scheduleReconnect = () => {
      if (disposed) return;
      // Exponential backoff with jitter, so a backend restart does not get a
      // synchronised stampede from every open tab.
      const delay = Math.min(BASE_RETRY_MILLIS * 2 ** retryRef.current, MAX_RETRY_MILLIS);
      const jittered = delay * (0.7 + Math.random() * 0.6);
      retryRef.current += 1;
      timersRef.current.reconnect = setTimeout(connect, jittered);
    };

    const connect = () => {
      if (disposed) return;
      setStatus(WS_STATUS.connecting);

      let socket;
      try {
        socket = new WebSocket(url);
      } catch {
        scheduleReconnect();
        return;
      }
      socketRef.current = socket;

      socket.onopen = () => {
        if (disposed) return;
        retryRef.current = 0;
        setStatus(WS_STATUS.open);
        handlersRef.current.onOpen?.();

        timersRef.current.ping = setInterval(() => {
          send({ type: 'PING', clientTime: Date.now() });
        }, PING_INTERVAL_MILLIS);
        send({ type: 'PING', clientTime: Date.now() });
      };

      socket.onmessage = (message) => {
        try {
          handlersRef.current.onEvent(JSON.parse(message.data));
        } catch {
          // A malformed frame is ignored rather than breaking the connection.
        }
      };

      socket.onclose = () => {
        if (disposed) return;
        clearInterval(timersRef.current.ping);
        setStatus(WS_STATUS.closed);
        scheduleReconnect();
      };

      // onerror is always followed by onclose, which owns the retry.
      socket.onerror = () => socket.close();
    };

    connect();

    return () => {
      disposed = true;
      clearTimers();
      const socket = socketRef.current;
      if (socket) {
        socket.onclose = null;
        socket.onerror = null;
        socket.close();
      }
    };
  }, [url, send]);

  return { status, send };
}

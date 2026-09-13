import { WS_STATUS } from '../hooks/useWebSocket.js';

/**
 * Connection and mode indicators.
 *
 * @param {{
 *   backendReachable: boolean, socketStatus: string,
 *   syncLabel: string, syncActive: boolean, clockOffsetMillis: number, clockSynced: boolean,
 * }} props
 */
export function StatusBar({
  backendReachable,
  socketStatus,
  syncLabel,
  syncActive,
  clockOffsetMillis,
  clockSynced,
}) {
  const socketLabel = {
    [WS_STATUS.open]: 'WebSocket connected',
    [WS_STATUS.connecting]: 'WebSocket connecting…',
    [WS_STATUS.closed]: 'WebSocket reconnecting…',
  }[socketStatus];

  return (
    <div className="status-bar" role="status" aria-live="polite">
      <Indicator ok={backendReachable} label={backendReachable ? 'Backend connected' : 'Backend unreachable'} />
      <Indicator ok={socketStatus === WS_STATUS.open} label={socketLabel} />
      <Indicator ok={!syncActive} tone={syncActive ? 'sync' : undefined} label={syncLabel} />
      <span className="status-bar__clock" title="Measured offset between this browser's clock and the server's">
        clock {clockSynced ? `${clockOffsetMillis >= 0 ? '+' : ''}${clockOffsetMillis} ms` : 'measuring…'}
      </span>
    </div>
  );
}

function Indicator({ ok, label, tone }) {
  const modifier = tone ?? (ok ? 'ok' : 'bad');
  return (
    <span className={`indicator indicator--${modifier}`}>
      <span className="indicator__dot" aria-hidden="true" />
      {label}
    </span>
  );
}

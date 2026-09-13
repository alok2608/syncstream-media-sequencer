import { secondsRemaining } from '../utils/format.js';
import { SYNC_PHASE } from '../utils/sync.js';

/**
 * The prominent banner shown while a global sync is pending or on screen.
 *
 * @param {{phase: string, media: object|null, remainingMillis: number, untilStartMillis: number, progress: number}} props
 */
export function SyncBanner({ phase, media, remainingMillis, untilStartMillis, progress }) {
  if (phase !== SYNC_PHASE.active && phase !== SYNC_PHASE.pending) return null;

  const pending = phase === SYNC_PHASE.pending;

  return (
    <div className={`sync-banner${pending ? ' sync-banner--pending' : ''}`} role="alert">
      <div className="sync-banner__main">
        <span className="sync-banner__title">
          {pending ? 'GLOBAL SYNC STARTING' : 'GLOBAL SYNC ACTIVE'}
        </span>
        <span className="sync-banner__media">Media: {media?.name ?? 'unknown'}</span>
      </div>

      <div className="sync-banner__timer">
        {pending ? (
          <>starts in {Math.max(0, Math.round(untilStartMillis))} ms</>
        ) : (
          <>Remaining: {secondsRemaining(remainingMillis)}s</>
        )}
      </div>

      <div className="sync-banner__progress" aria-hidden="true">
        <div className="sync-banner__progress-bar" style={{ width: `${Math.min(100, progress * 100)}%` }} />
      </div>
    </div>
  );
}

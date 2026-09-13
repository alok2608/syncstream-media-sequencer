import { useMemo } from 'react';

import { CYCLE_MILLIS, occurrenceKey, resolvePlayback, timelineFromPlaylist } from '../utils/playback.js';
import { formatDuration, formatSeconds } from '../utils/format.js';
import { MediaPlayer } from './MediaPlayer.jsx';

/**
 * One display window: its media surface plus everything an evaluator needs to
 * see that the clock is behaving - current item, progress, loop counter and
 * position in the 5-hour cycle.
 *
 * The rendered item is recomputed from `serverNowMillis` on every tick, so this
 * component holds no playback state of its own and is correct immediately after
 * a refresh.
 *
 * @param {{
 *   window: object,
 *   serverNowMillis: number,
 *   syncMedia: object|null,
 *   isSyncActive: boolean,
 * }} props
 */
export function WindowCard({ window, serverNowMillis, syncMedia, isSyncActive }) {
  // The timeline only changes when the playlist does.
  const timeline = useMemo(() => timelineFromPlaylist(window.playlist), [window.playlist]);

  const state = resolvePlayback(timeline, new Date(window.cycleAnchor).getTime(), serverNowMillis);
  const playlistItem = state ? window.playlist.find((item) => item.id === state.item.playlistItemId) : null;
  const ownMedia = playlistItem?.media ?? null;

  // During a sync the window shows the synced media instead of its own item -
  // but `state` above keeps advancing underneath, which is exactly why normal
  // playback resumes in the right place when the override ends.
  const displayed = isSyncActive ? syncMedia : ownMedia;
  const displayedKey = isSyncActive ? `sync:${syncMedia?.id}` : occurrenceKey(state);

  const itemProgress = state
    ? state.elapsedInItemMillis / (state.elapsedInItemMillis + state.remainingInItemMillis)
    : 0;

  return (
    <article className={`window-card${isSyncActive ? ' window-card--synced' : ''}`}>
      <header className="window-card__header">
        <div>
          <h3 className="window-card__name">{window.name}</h3>
          <p className="window-card__meta">
            {window.playlist.length === 0
              ? 'Empty playlist'
              : `${window.playlist.length} items · ${formatSeconds(window.playlistDurationMillis / 1000)} loop`}
          </p>
        </div>
        {isSyncActive && <span className="badge badge--sync">SYNCED</span>}
      </header>

      <div className="window-card__stage">
        <MediaPlayer
          media={displayed}
          elapsedInItemMillis={isSyncActive ? 0 : (state?.elapsedInItemMillis ?? 0)}
          occurrenceKey={displayedKey}
        />

        {/* The window's own item stays visible during a sync so it is obvious
            that the playlist was overridden, not modified. */}
        {isSyncActive && ownMedia && (
          <div className="window-card__underlay" title="This window's own playlist keeps running underneath">
            underneath: {ownMedia.name}
          </div>
        )}
      </div>

      <div className="window-card__now">
        <div className="window-card__now-main">
          <span className="window-card__now-name">{displayed ? displayed.name : 'No media'}</span>
          {displayed && <span className={`chip chip--${displayed.type}`}>{displayed.type}</span>}
        </div>
        {state && !isSyncActive && (
          <span className="window-card__now-time">
            {formatDuration(state.elapsedInItemMillis)} / {formatDuration(state.elapsedInItemMillis + state.remainingInItemMillis)}
          </span>
        )}
      </div>

      <div
        className="progress"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(itemProgress * 100)}
      >
        <div className="progress__bar" style={{ width: `${Math.min(100, itemProgress * 100)}%` }} />
      </div>

      {state ? (
        <dl className="window-card__stats">
          <div>
            <dt>Position</dt>
            <dd>
              {state.index + 1} / {timeline.items.length}
            </dd>
          </div>
          <div>
            <dt>Loop</dt>
            <dd>#{state.loopIteration + 1}</dd>
          </div>
          <div>
            <dt>Cycle</dt>
            <dd>#{state.cycleIndex + 1}</dd>
          </div>
          <div>
            <dt title="Time left in this window's 5-hour cycle">Cycle left</dt>
            <dd>{formatDuration(CYCLE_MILLIS - state.elapsedInCycleMillis)}</dd>
          </div>
        </dl>
      ) : (
        <p className="window-card__empty-note">
          This window has no playable items, so it shows the fallback state. It is not blank playback &mdash;
          blank only appears when a blank item is configured.
        </p>
      )}
    </article>
  );
}

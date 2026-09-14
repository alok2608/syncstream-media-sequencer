import { WindowCard } from './WindowCard.jsx';

/**
 * Responsive grid of display windows.
 *
 * @param {{
 *   windows: Array, serverNowMillis: number,
 *   syncMedia: object|null, syncElapsedMillis: number,
 *   isSyncActive: boolean, loading: boolean, unreachable: boolean,
 * }} props
 */
export function WindowGrid({
  windows,
  serverNowMillis,
  syncMedia,
  syncElapsedMillis,
  isSyncActive,
  loading,
  unreachable,
}) {
  if (loading) {
    return (
      <div className="grid-placeholder">
        <p>Loading playback configuration&hellip;</p>
      </div>
    );
  }

  if (windows.length === 0) {
    // Distinguish "there really are no windows" from "we could not load them",
    // which otherwise look identical and send an operator hunting the wrong bug.
    return unreachable ? (
      <div className="grid-placeholder">
        <p>Playback configuration could not be loaded.</p>
        <p className="grid-placeholder__hint">
          The backend is unreachable. Windows will appear as soon as the connection is restored.
        </p>
      </div>
    ) : (
      <div className="grid-placeholder">
        <p>No windows are configured yet.</p>
        <p className="grid-placeholder__hint">Create one in the Playlist Management panel below.</p>
      </div>
    );
  }

  return (
    <div className="window-grid">
      {windows.map((window) => (
        <WindowCard
          key={window.id}
          window={window}
          serverNowMillis={serverNowMillis}
          syncMedia={syncMedia}
          syncElapsedMillis={syncElapsedMillis}
          isSyncActive={isSyncActive}
        />
      ))}
    </div>
  );
}

import { useMemo } from 'react';

import { PlaylistManager } from './components/PlaylistManager.jsx';
import { StatusBar } from './components/StatusBar.jsx';
import { SyncBanner } from './components/SyncBanner.jsx';
import { SyncControls } from './components/SyncControls.jsx';
import { WindowGrid } from './components/WindowGrid.jsx';
import { useNow } from './hooks/useNow.js';
import { useSequencerState } from './hooks/useSequencerState.js';
import { toServerMillis, useServerClock } from './hooks/useServerClock.js';
import { CYCLE_MILLIS } from './utils/playback.js';
import { SYNC_PHASE, isSyncOnScreen, resolveSyncPhase } from './utils/sync.js';
import { formatDuration } from './utils/format.js';

/** Playback is recomputed at 10 Hz, which is smooth without being wasteful. */
const TICK_MILLIS = 100;

export default function App() {
  const clock = useServerClock();
  const { windows, media, activeSync, loading, error, socketStatus, refresh } = useSequencerState(clock);

  // One clock for the whole page: every window resolves its current item from
  // the same offset-corrected instant.
  const clientNow = useNow(TICK_MILLIS);
  const serverNowMillis = toServerMillis(clientNow, clock.offsetMillis);

  const sync = useMemo(
    () => resolveSyncPhase(activeSync, serverNowMillis),
    [activeSync, serverNowMillis],
  );
  const syncOnScreen = isSyncOnScreen(sync.phase);

  const syncLabel =
    sync.phase === SYNC_PHASE.active
      ? 'Global sync active'
      : sync.phase === SYNC_PHASE.pending
        ? 'Global sync starting'
        : 'Normal playback';

  return (
    <div className="app">
      <header className="app__header">
        <div className="app__title">
          <h1>Multi-Window Media Sequencer</h1>
          <p className="app__tagline">
            Deterministic continuous playback with a 5-hour cycle ({formatDuration(CYCLE_MILLIS)}) and
            timestamp-aligned global sync.
          </p>
        </div>
        <StatusBar
          backendReachable={!error}
          socketStatus={socketStatus}
          syncLabel={syncLabel}
          syncActive={sync.phase !== SYNC_PHASE.idle && sync.phase !== SYNC_PHASE.ended}
          clockOffsetMillis={clock.offsetMillis}
          clockSynced={clock.synced}
        />
      </header>

      <SyncBanner
        phase={sync.phase}
        media={activeSync?.media ?? null}
        remainingMillis={sync.remainingMillis}
        untilStartMillis={sync.untilStartMillis}
        progress={sync.progress}
      />

      {error && (
        <p className="feedback feedback--error" role="alert">
          {error.detail ?? error.message}. Windows keep playing from the last known configuration; the
          page reconnects automatically.
        </p>
      )}

      <main className="app__main">
        <WindowGrid
          windows={windows}
          serverNowMillis={serverNowMillis}
          syncMedia={activeSync?.media ?? null}
          isSyncActive={syncOnScreen}
          loading={loading}
          unreachable={!!error}
        />

        <aside className="app__sidebar">
          <SyncControls media={media} activeSync={activeSync} disabled={!!error} />
          <PlaylistManager windows={windows} media={media} onChanged={refresh} />
        </aside>
      </main>

      <footer className="app__footer">
        <span>
          Playback is computed locally from the server clock; the backend serves configuration and sync
          events only.
        </span>
      </footer>
    </div>
  );
}

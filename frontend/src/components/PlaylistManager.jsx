import { useEffect, useMemo, useState } from 'react';

import { api } from '../api/client.js';
import { formatSeconds } from '../utils/format.js';

const EMPTY_FORM = { name: '', type: 'image', url: '', durationSeconds: 10 };

/**
 * Admin panel: create windows, inspect a window's playlist, and add, reorder or
 * remove its items.
 *
 * Every change is a REST write; the resulting state arrives back through the
 * WebSocket broadcast, so all open browsers converge on the same configuration
 * without anything being restarted or reloaded.
 *
 * @param {{windows: Array, media: Array, onChanged: Function}} props
 */
export function PlaylistManager({ windows, media, onChanged }) {
  const [selectedWindowId, setSelectedWindowId] = useState(null);
  const [source, setSource] = useState('new'); // 'new' | 'library'
  const [form, setForm] = useState(EMPTY_FORM);
  const [libraryMediaId, setLibraryMediaId] = useState('');
  const [newWindowName, setNewWindowName] = useState('');
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState(null);

  // Keep a valid selection as windows come and go.
  useEffect(() => {
    if (windows.length === 0) {
      setSelectedWindowId(null);
      return;
    }
    if (!windows.some((w) => w.id === selectedWindowId)) {
      setSelectedWindowId(windows[0].id);
    }
  }, [windows, selectedWindowId]);

  useEffect(() => {
    if (media.length > 0 && !media.some((m) => String(m.id) === libraryMediaId)) {
      setLibraryMediaId(String(media[0].id));
    }
  }, [media, libraryMediaId]);

  const selectedWindow = useMemo(
    () => windows.find((w) => w.id === selectedWindowId) ?? null,
    [windows, selectedWindowId],
  );

  const urlRequired = source === 'new' && form.type !== 'blank';

  /**
   * Runs a mutation, surfacing success or the backend's error message.
   * Returns whether it succeeded, so callers only clear their inputs when the
   * change actually went through - a rejected form should keep what was typed.
   */
  const runAction = async (action, successMessage) => {
    setBusy(true);
    setFeedback(null);
    try {
      await action();
      setFeedback({ tone: 'success', text: successMessage });
      await onChanged?.();
      return true;
    } catch (error) {
      setFeedback({ tone: 'error', text: error.detail ?? error.message });
      return false;
    } finally {
      setBusy(false);
    }
  };

  const handleCreateWindow = async (event) => {
    event.preventDefault();
    const name = newWindowName.trim();
    if (!name) return;
    if (await runAction(() => api.createWindow(name), `Created "${name}".`)) {
      setNewWindowName('');
    }
  };

  const handleAddMedia = async (event) => {
    event.preventDefault();
    if (!selectedWindow) return;

    const payload =
      source === 'library'
        ? { mediaId: Number(libraryMediaId) }
        : {
            media: {
              name: form.name.trim(),
              type: form.type,
              url: form.type === 'blank' ? '' : form.url.trim(),
              durationSeconds: Number(form.durationSeconds),
            },
          };

    const added = await runAction(
      () => api.addPlaylistItem(selectedWindow.id, payload),
      `Added to ${selectedWindow.name}.`,
    );
    if (added && source === 'new') setForm(EMPTY_FORM);
  };

  const handleMove = (item, direction) => {
    if (!selectedWindow) return;
    const target = item.position + direction;
    if (target < 0 || target >= selectedWindow.playlist.length) return;
    runAction(
      () => api.movePlaylistItem(selectedWindow.id, item.id, target),
      `Moved ${item.media.name}.`,
    );
  };

  const handleRemove = (item) => {
    if (!selectedWindow) return;
    runAction(
      () => api.removePlaylistItem(selectedWindow.id, item.id),
      `Removed ${item.media.name}.`,
    );
  };

  return (
    <section className="panel" aria-labelledby="playlist-heading">
      <header className="panel__header">
        <h2 id="playlist-heading">Playlist Management</h2>
        <p className="panel__subtitle">
          Changes persist in PostgreSQL and reach every open window over the WebSocket.
        </p>
      </header>

      <form className="field-row" onSubmit={handleCreateWindow}>
        <label className="field field--grow">
          <span>New window</span>
          <input
            type="text"
            value={newWindowName}
            placeholder="e.g. Window 4 - Atrium"
            onChange={(e) => setNewWindowName(e.target.value)}
            maxLength={120}
          />
        </label>
        <button type="submit" className="button button--ghost" disabled={busy || !newWindowName.trim()}>
          Add window
        </button>
      </form>

      <hr className="divider" />

      <label className="field">
        <span>Select window</span>
        <select
          value={selectedWindowId ?? ''}
          onChange={(e) => setSelectedWindowId(Number(e.target.value))}
          disabled={windows.length === 0}
        >
          {windows.map((window) => (
            <option key={window.id} value={window.id}>
              {window.name}
            </option>
          ))}
        </select>
      </label>

      {selectedWindow && (
        <>
          <ol className="playlist">
            {selectedWindow.playlist.length === 0 && (
              <li className="playlist__empty">
                This playlist is empty. The window shows its fallback state until an item is added.
              </li>
            )}
            {selectedWindow.playlist.map((item, index) => (
              <li key={item.id} className="playlist__item">
                <span className="playlist__position">{index + 1}</span>
                <span className="playlist__name">{item.media.name}</span>
                <span className={`chip chip--${item.media.type}`}>{item.media.type}</span>
                <span className="playlist__duration">{formatSeconds(item.media.durationSeconds)}</span>
                <span className="playlist__actions">
                  <button
                    type="button"
                    className="icon-button"
                    onClick={() => handleMove(item, -1)}
                    disabled={busy || index === 0}
                    aria-label={`Move ${item.media.name} earlier`}
                    title="Move earlier"
                  >
                    ↑
                  </button>
                  <button
                    type="button"
                    className="icon-button"
                    onClick={() => handleMove(item, 1)}
                    disabled={busy || index === selectedWindow.playlist.length - 1}
                    aria-label={`Move ${item.media.name} later`}
                    title="Move later"
                  >
                    ↓
                  </button>
                  <button
                    type="button"
                    className="icon-button icon-button--danger"
                    onClick={() => handleRemove(item)}
                    disabled={busy}
                    aria-label={`Remove ${item.media.name}`}
                    title="Remove"
                  >
                    ×
                  </button>
                </span>
              </li>
            ))}
          </ol>

          <form className="stack" onSubmit={handleAddMedia}>
            <div className="segmented" role="radiogroup" aria-label="Media source">
              <button
                type="button"
                role="radio"
                aria-checked={source === 'new'}
                className={source === 'new' ? 'segmented__option is-selected' : 'segmented__option'}
                onClick={() => setSource('new')}
              >
                New media
              </button>
              <button
                type="button"
                role="radio"
                aria-checked={source === 'library'}
                className={source === 'library' ? 'segmented__option is-selected' : 'segmented__option'}
                onClick={() => setSource('library')}
                disabled={media.length === 0}
              >
                From library
              </button>
            </div>

            {source === 'library' ? (
              <label className="field">
                <span>Media</span>
                <select value={libraryMediaId} onChange={(e) => setLibraryMediaId(e.target.value)}>
                  {media.map((item) => (
                    <option key={item.id} value={item.id}>
                      {item.name} · {item.type} · {formatSeconds(item.durationSeconds)}
                    </option>
                  ))}
                </select>
              </label>
            ) : (
              <>
                <div className="field-row">
                  <label className="field field--grow">
                    <span>Media name</span>
                    <input
                      type="text"
                      required
                      maxLength={120}
                      value={form.name}
                      placeholder="e.g. M9 - Winter Sale"
                      onChange={(e) => setForm({ ...form, name: e.target.value })}
                    />
                  </label>
                  <label className="field">
                    <span>Type</span>
                    <select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })}>
                      <option value="image">Image</option>
                      <option value="video">Video</option>
                      <option value="blank">Blank</option>
                    </select>
                  </label>
                </div>

                <label className="field">
                  <span>
                    Media URL {form.type === 'blank' && <em className="field__note">not used for blank</em>}
                  </span>
                  <input
                    type="url"
                    value={form.url}
                    required={urlRequired}
                    disabled={form.type === 'blank'}
                    placeholder="https://example.com/media.jpg"
                    onChange={(e) => setForm({ ...form, url: e.target.value })}
                  />
                </label>

                <label className="field">
                  <span>Duration (seconds)</span>
                  <input
                    type="number"
                    min={1}
                    max={3600}
                    required
                    value={form.durationSeconds}
                    onChange={(e) => setForm({ ...form, durationSeconds: e.target.value })}
                  />
                </label>
              </>
            )}

            <button type="submit" className="button button--primary" disabled={busy}>
              {busy ? 'Saving…' : 'Add media to window'}
            </button>
          </form>
        </>
      )}

      {feedback && (
        <p className={`feedback feedback--${feedback.tone}`} role="status">
          {feedback.text}
        </p>
      )}
    </section>
  );
}

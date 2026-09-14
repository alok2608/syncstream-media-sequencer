/**
 * Thin REST client for the sequencer backend.
 *
 * The base URLs come from the environment so no deployment domain is ever baked
 * into application logic.
 */

/** Base URL of the API, without a trailing slash. */
export const API_URL = (import.meta.env.VITE_API_URL ?? 'http://localhost:8080').replace(/\/+$/, '');

/**
 * WebSocket endpoint. When VITE_WS_URL is not set it is derived from the API
 * URL, which keeps a deployment to a single variable in the common case and
 * automatically upgrades to wss:// behind HTTPS.
 */
export const WS_URL =
  import.meta.env.VITE_WS_URL ?? `${API_URL.replace(/^http/, 'ws')}/ws`;

/** ApiError carries the backend's structured error payload to the UI. */
export class ApiError extends Error {
  constructor(message, { status = 0, code = 'unknown', fields = [] } = {}) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.fields = fields;
  }

  /** A human-readable summary including any field-level problems. */
  get detail() {
    if (!this.fields.length) return this.message;
    return this.fields.map((f) => `${f.field}: ${f.message}`).join(', ');
  }
}

/**
 * Performs a request and unwraps the `{ data }` envelope.
 *
 * @param {string} path API path beginning with a slash
 * @param {{method?: string, body?: object, signal?: AbortSignal}} options
 */
async function request(path, { method = 'GET', body, signal } = {}) {
  let response;
  try {
    response = await fetch(`${API_URL}${path}`, {
      method,
      signal,
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
  } catch (cause) {
    // A network-level failure: the backend is down, unreachable or blocked.
    if (cause.name === 'AbortError') throw cause;
    throw new ApiError('Cannot reach the backend', { code: 'network_error' });
  }

  if (response.status === 204) return null;

  let payload = null;
  try {
    payload = await response.json();
  } catch {
    // Fall through: a non-JSON body is reported using the status alone.
  }

  if (!response.ok) {
    const error = payload?.error;
    throw new ApiError(error?.message ?? `Request failed with status ${response.status}`, {
      status: response.status,
      code: error?.code ?? 'unknown',
      fields: error?.fields ?? [],
    });
  }
  return payload?.data ?? null;
}

// Only the calls the UI actually makes are exposed. The full HTTP surface is
// documented in the README; adding a wrapper here before something uses it
// would just be dead code.
export const api = {
  /** Full bootstrap state: windows with playlists, media library, live sync. */
  getState: (signal) => request('/api/state', { signal }),

  /** Server clock, used to estimate this browser's offset. */
  getTime: (signal) => request('/api/time', { signal }),

  createWindow: (name) => request('/api/windows', { method: 'POST', body: { name } }),

  /** Appends media to a playlist, either by id or by defining it inline. */
  addPlaylistItem: (windowId, payload) =>
    request(`/api/windows/${windowId}/playlist`, { method: 'POST', body: payload }),

  movePlaylistItem: (windowId, itemId, position) =>
    request(`/api/windows/${windowId}/playlist/${itemId}`, { method: 'PATCH', body: { position } }),

  removePlaylistItem: (windowId, itemId) =>
    request(`/api/windows/${windowId}/playlist/${itemId}`, { method: 'DELETE' }),

  startSync: (mediaId, durationSeconds) =>
    request('/api/sync', { method: 'POST', body: { mediaId, durationSeconds } }),
  cancelSync: () => request('/api/sync/cancel', { method: 'POST' }),
};

/**
 * Resolves a media URL for use in an `<img>` or `<video>`.
 *
 * Media may be stored as an absolute URL, an inline `data:` URI, or a
 * root-relative path such as `/api/assets/clip.mp4`. The relative form is what
 * the seeded demo media uses: it keeps the database portable, because the same
 * rows resolve against whichever backend this build is pointed at.
 */
export function resolveMediaUrl(url) {
  if (!url) return '';
  if (url.startsWith('/')) return `${API_URL}${url}`;
  return url;
}

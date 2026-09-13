/** Small presentation helpers shared by the UI. */

/** Formats milliseconds as a compact clock, e.g. "0:07" or "1:02:03". */
export function formatDuration(millis) {
  const total = Math.max(0, Math.round(millis / 1000));
  const seconds = total % 60;
  const minutes = Math.floor(total / 60) % 60;
  const hours = Math.floor(total / 3600);

  const pad = (n) => String(n).padStart(2, '0');
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${minutes}:${pad(seconds)}`;
}

/** Formats seconds as "10s" / "1m 30s". */
export function formatSeconds(seconds) {
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return rest === 0 ? `${minutes}m` : `${minutes}m ${rest}s`;
}

/** Rounds up to whole seconds for a countdown that never displays "0" early. */
export function secondsRemaining(millis) {
  return Math.max(0, Math.ceil(millis / 1000));
}

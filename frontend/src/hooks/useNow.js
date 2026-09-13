import { useEffect, useState } from 'react';

/**
 * Ticks at a fixed interval and returns the current browser timestamp.
 *
 * Components recompute their state from this timestamp on every tick. Nothing
 * is scheduled per playlist item, so a tab that was throttled or asleep simply
 * resolves the correct item on its next tick instead of replaying a backlog of
 * missed timers.
 *
 * @param {number} intervalMillis how often to re-render
 */
export function useNow(intervalMillis = 200) {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMillis);

    // A tab returning to the foreground should catch up immediately rather than
    // waiting out the remainder of the interval.
    const onVisible = () => {
      if (document.visibilityState === 'visible') setNow(Date.now());
    };
    document.addEventListener('visibilitychange', onVisible);

    return () => {
      clearInterval(id);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [intervalMillis]);

  return now;
}

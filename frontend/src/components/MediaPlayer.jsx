import { useEffect, useRef, useState } from 'react';

import { resolveMediaUrl } from '../api/client.js';

/**
 * Renders a single media item at a given offset into its playback.
 *
 * The component is told *what* to show and *how far into it we are*; it never
 * decides when to advance. Progression is owned entirely by the cycle clock,
 * which is what keeps every window - and every browser - in agreement.
 *
 * Videos: the configured duration is authoritative, not the file's own length.
 * A video shorter than its configured duration loops inside its slot; one that
 * is longer is cut off when the slot ends. The element is seeked to the correct
 * offset whenever a new occurrence begins, so a refresh mid-video resumes in
 * the right place instead of restarting.
 *
 * @param {{
 *   media: object|null,
 *   elapsedInItemMillis: number,
 *   occurrenceKey: string,
 *   muted?: boolean,
 * }} props
 */
export function MediaPlayer({ media, elapsedInItemMillis, occurrenceKey, muted = true }) {
  const [failed, setFailed] = useState(false);
  const videoRef = useRef(null);

  // A new occurrence is a fresh start: clear any previous load failure.
  useEffect(() => setFailed(false), [occurrenceKey, media?.id]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || media?.type !== 'video') return;

    /** Seeks the element to where the cycle clock says we should be. */
    const align = () => {
      const length = video.duration;
      if (!Number.isFinite(length) || length <= 0) return;

      // Videos shorter than their slot loop within it.
      const target = (elapsedInItemMillis / 1000) % length;
      if (Math.abs(video.currentTime - target) > 0.75) {
        video.currentTime = target;
      }
    };

    if (video.readyState >= 1) align();
    else video.addEventListener('loadedmetadata', align, { once: true });

    // Autoplay can be refused; muted inline playback is normally allowed.
    video.play().catch(() => {});

    return () => video.removeEventListener('loadedmetadata', align);
    // Deliberately keyed on the occurrence, not on elapsed time: re-seeking on
    // every tick would fight with normal playback.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [occurrenceKey, media?.id, media?.type]);

  if (!media) {
    return (
      <div className="media-surface media-surface--fallback" role="img" aria-label="No media configured">
        <div className="media-fallback">
          <span className="media-fallback__icon" aria-hidden="true">○</span>
          <p className="media-fallback__title">Nothing scheduled</p>
          <p className="media-fallback__hint">Add media to this window&rsquo;s playlist to start playback.</p>
        </div>
      </div>
    );
  }

  // An explicitly configured blank item. This is the only route to a blank
  // screen: unused cycle time is never turned into blank playback.
  if (media.type === 'blank') {
    return (
      <div className="media-surface media-surface--blank" role="img" aria-label={`Blank media: ${media.name}`}>
        <span className="media-blank__label">BLANK</span>
      </div>
    );
  }

  if (failed) {
    return (
      <div className="media-surface media-surface--fallback" role="img" aria-label={`${media.name} failed to load`}>
        <div className="media-fallback">
          <span className="media-fallback__icon media-fallback__icon--warn" aria-hidden="true">!</span>
          <p className="media-fallback__title">Media unavailable</p>
          <p className="media-fallback__hint">
            {media.name} could not be loaded. Playback continues to the next item.
          </p>
        </div>
      </div>
    );
  }

  if (media.type === 'video') {
    return (
      <video
        // Keying on the occurrence gives each pass a fresh element, which is the
        // most reliable way to restart a clip when the playlist comes round.
        key={occurrenceKey}
        ref={videoRef}
        className="media-surface media-surface--video"
        src={resolveMediaUrl(media.url)}
        muted={muted}
        playsInline
        autoPlay
        loop
        preload="auto"
        onError={() => setFailed(true)}
        aria-label={media.name}
      />
    );
  }

  return (
    <img
      className="media-surface media-surface--image"
      src={resolveMediaUrl(media.url)}
      alt={media.name}
      onError={() => setFailed(true)}
      draggable={false}
    />
  );
}

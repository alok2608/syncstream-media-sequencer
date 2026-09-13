-- Core schema: windows, media library, per-window playlists and sync events.

CREATE TABLE IF NOT EXISTS windows (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name         TEXT        NOT NULL,
    cycle_anchor TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT windows_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT windows_name_unique UNIQUE (name)
);

CREATE TABLE IF NOT EXISTS media (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name             TEXT        NOT NULL,
    type             TEXT        NOT NULL,
    url              TEXT        NOT NULL DEFAULT '',
    duration_seconds INTEGER     NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT media_name_not_blank CHECK (length(btrim(name)) > 0),
    CONSTRAINT media_type_valid CHECK (type IN ('image', 'video', 'blank')),
    CONSTRAINT media_duration_range CHECK (duration_seconds BETWEEN 1 AND 3600),
    -- Image and video need a source; blank must not carry one.
    CONSTRAINT media_url_matches_type CHECK (
        (type IN ('image', 'video') AND length(btrim(url)) > 0)
        OR (type = 'blank' AND url = '')
    )
);

CREATE TABLE IF NOT EXISTS playlist_items (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    window_id  BIGINT      NOT NULL REFERENCES windows (id) ON DELETE CASCADE,
    media_id   BIGINT      NOT NULL REFERENCES media (id)   ON DELETE RESTRICT,
    position   INTEGER     NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT playlist_items_position_non_negative CHECK (position >= 0),
    -- Deferred so a reorder can shuffle positions inside one transaction.
    CONSTRAINT playlist_items_window_position_unique UNIQUE (window_id, position)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX IF NOT EXISTS playlist_items_window_position_idx
    ON playlist_items (window_id, position);
CREATE INDEX IF NOT EXISTS playlist_items_media_idx
    ON playlist_items (media_id);

CREATE TABLE IF NOT EXISTS sync_events (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    media_id     BIGINT      NOT NULL REFERENCES media (id) ON DELETE CASCADE,
    start_at     TIMESTAMPTZ NOT NULL,
    end_at       TIMESTAMPTZ NOT NULL,
    cancelled_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT sync_events_window_ordered CHECK (end_at > start_at)
);

-- The active-sync lookup is "the newest event that has not finished yet".
CREATE INDEX IF NOT EXISTS sync_events_end_at_idx ON sync_events (end_at DESC);

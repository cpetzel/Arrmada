-- 0351_grab_published_at (#81): when the release a series grab took was posted, as the
-- indexer said (RFC3339 UTC), so the import can tell a yearless release of one show from
-- a same-named show's ("Scrubs.S02E03" of the 2001 show or of the 2026 revival) by the
-- episodes' air dates. '' when the indexer didn't say, and on every grab from before this
-- column: the import then goes by when the download was added, which only ever holds more
-- back, never less.
ALTER TABLE grabs ADD COLUMN published_at TEXT NOT NULL DEFAULT '';

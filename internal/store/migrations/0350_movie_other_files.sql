-- 0350_movie_other_files: the videos in a film's folder that Arrmada doesn't use (another
-- cut, an old copy, a second film left in the folder). A library scan or a refresh notes
-- them; the movies list shows the count and the film's page lists them. Read-only: the
-- row only describes what was seen, nothing acts on it.
--
-- One row per film that has been looked at. file_count is every such file found;
-- files_json holds up to the first 50 ({name, rel_path, path, size_bytes, resolution}).
-- shared = 1 when the folder isn't provably the film's own, so only the files no film in
-- the library records were counted, and only at the folder's top level.
CREATE TABLE movie_other_files (
    movie_id   INTEGER PRIMARY KEY,
    file_count INTEGER NOT NULL DEFAULT 0,
    files_json TEXT NOT NULL DEFAULT '[]',
    shared     INTEGER NOT NULL DEFAULT 0,
    truncated  INTEGER NOT NULL DEFAULT 0,
    checked_at TEXT NOT NULL DEFAULT ''
);

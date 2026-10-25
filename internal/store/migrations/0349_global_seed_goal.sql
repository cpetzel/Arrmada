-- 0349_global_seed_goal (issue #78): a global seed goal (Settings → Downloads) that an
-- indexer opts into. Forward-only: nothing that exists changes meaning.
--
-- indexers.seed_use_global = 1 means "seed to the global goal". It is its own state, not
-- a reading of 0/0: since 0056 every new indexer got 336 hours, so an indexer at 0/0 is
-- one its owner set to seed forever, and taking that over would delete their torrents.
-- Every existing indexer reads 0 and keeps its values exactly as they are.
ALTER TABLE indexers ADD COLUMN seed_use_global INTEGER NOT NULL DEFAULT 0;

-- grabs.seed_source records where a grab's goal came from when it was grabbed: 'own' (its
-- indexer's, recorded on the row), 'global' (read live at each check, so changing the
-- global goal reaches it), 'none' (seed forever) or 'default' (the standard window, for a
-- manual or unknown-indexer grab). '' is a grab from before this column: it keeps the goal
-- recorded on it and never picks up the global one.
ALTER TABLE grabs ADD COLUMN seed_source TEXT NOT NULL DEFAULT '';

-- grabs.import_unmatched counts the files a series import couldn't match to an episode.
-- Seed cleanup removes an imported torrent WITH its data, which would delete those files;
-- such a torrent keeps seeding instead.
ALTER TABLE grabs ADD COLUMN import_unmatched INTEGER NOT NULL DEFAULT 0;

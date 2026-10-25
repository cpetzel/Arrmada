package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0349 adds the global seed goal forward-only: every existing indexer keeps its own
// values, 0/0 ("seed forever") included, and is not switched to the global goal; every
// existing grab keeps the goal recorded on it (an empty source).
func TestGlobalSeedGoalMigrationLeavesEverythingAsItWas(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, n := range names {
		if n >= "0349" {
			break
		}
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		before[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0348: %v", err)
	}
	for _, q := range []string{
		`INSERT INTO indexers (id, name, kind, url, seed_enabled, seed_ratio, seed_hours) VALUES (1, 'Forever', 'torznab', 'http://a', 1, 0, 0)`,
		`INSERT INTO indexers (id, name, kind, url, seed_enabled, seed_ratio, seed_hours) VALUES (2, 'Own', 'torznab', 'http://b', 1, 2, 336)`,
		`INSERT INTO grabs (id, movie_id, title, media_type, info_hash, status, seed_enabled, seed_ratio, seed_hours) VALUES (1, 1, 'Heat.1995.1080p', 'movie', 'abc', 'imported', 1, 0, 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := runMigrations(ctx, db, all); err != nil {
		t.Fatalf("migrate the rest: %v", err)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM indexers WHERE seed_use_global = 0
		AND ((id = 1 AND seed_ratio = 0 AND seed_hours = 0) OR (id = 2 AND seed_ratio = 2 AND seed_hours = 336))`); n != 2 {
		t.Errorf("indexers unchanged and not on the global goal: %d of 2", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM grabs WHERE id = 1 AND seed_source = '' AND import_unmatched = 0 AND seed_hours = 0`); n != 1 {
		t.Error("the existing grab changed")
	}
}

package store

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

// 0348 (issue #76) puts TVDB's artwork host in front of episode images stored as a path,
// and leaves full addresses (TMDB, TVmaze, TVDB) and empty ones alone.
func TestTVDBEpisodeImageURLsMigration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, 1)
	all := embeddedMigrations()
	names, err := listMigrations(all)
	if err != nil {
		t.Fatal(err)
	}
	before, rest := fstest.MapFS{}, fstest.MapFS{}
	for _, n := range names {
		body, err := fs.ReadFile(all, n)
		if err != nil {
			t.Fatal(err)
		}
		if n < "0348" {
			before[n] = &fstest.MapFile{Data: body}
		}
		rest[n] = &fstest.MapFile{Data: body}
	}
	if err := runMigrations(ctx, db, before); err != nil {
		t.Fatalf("migrate to 0347: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO series (id, tmdb_id, title) VALUES (1, 100, 'Show')`); err != nil {
		t.Fatal(err)
	}
	cases := map[int][2]string{
		1: {"/banners/v4/episode/9431113/screencap/6acb1abcc060d.jpg", "https://artworks.thetvdb.com/banners/v4/episode/9431113/screencap/6acb1abcc060d.jpg"},
		2: {"banners/episodes/1/2.jpg", "https://artworks.thetvdb.com/banners/episodes/1/2.jpg"},
		3: {"https://artworks.thetvdb.com/banners/v4/episode/1/a.jpg", "https://artworks.thetvdb.com/banners/v4/episode/1/a.jpg"},
		4: {"https://image.tmdb.org/t/p/w300/abc.jpg", "https://image.tmdb.org/t/p/w300/abc.jpg"},
		5: {"https://static.tvmaze.com/uploads/images/original_untouched/175/439288.jpg", "https://static.tvmaze.com/uploads/images/original_untouched/175/439288.jpg"},
		6: {"", ""},
	}
	for ep, c := range cases {
		if _, err := db.ExecContext(ctx, `INSERT INTO episodes (series_id, season_number, episode_number, still_url) VALUES (1, 1, ?, ?)`, ep, c[0]); err != nil {
			t.Fatal(err)
		}
	}
	if err := runMigrations(ctx, db, rest); err != nil {
		t.Fatalf("migrate 0348: %v", err)
	}
	for ep, c := range cases {
		var got string
		if err := db.QueryRowContext(ctx, `SELECT still_url FROM episodes WHERE series_id = 1 AND episode_number = ?`, ep).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != c[1] {
			t.Errorf("episode %d: still_url = %q, want %q", ep, got, c[1])
		}
	}
}

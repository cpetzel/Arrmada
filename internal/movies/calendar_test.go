package movies

import (
	"context"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// The Calendar marks the day a monitored, missing film's search starts: its digital entry
// when that decides it, or an entry of its own ("search") on cinema + 90 days when no home
// date is known. A film with its file, or not monitored, has no start day.
func TestUpcomingMarksSearchStart(t *testing.T) {
	s := titlesService(t, nil)
	ctx := context.Background()
	day := func(offset int) string { return time.Now().AddDate(0, 0, offset).Format(time.DateOnly) }
	us := func(offset int) *metadata.DatedRelease { return &metadata.DatedRelease{Date: day(offset), Country: "US"} }
	for _, m := range []Movie{
		{TMDBID: 1, Title: "Digital", Monitored: true, MinAvailability: "released",
			Extra: &MovieExtra{ReleaseDate: day(-10), Releases: &metadata.ReleaseDates{Cinema: us(-10), Digital: us(20)}}},
		{TMDBID: 2, Title: "NoHome", Monitored: true, MinAvailability: "released",
			Extra: &MovieExtra{ReleaseDate: day(-10), Releases: &metadata.ReleaseDates{Cinema: us(-10)}}},
		{TMDBID: 3, Title: "Unwanted", Monitored: false, MinAvailability: "released",
			Extra: &MovieExtra{ReleaseDate: day(-10), Releases: &metadata.ReleaseDates{Cinema: us(-10)}}},
	} {
		if _, err := s.repo.Create(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Upcoming(ctx, day(-30), day(100))
	if err != nil {
		t.Fatal(err)
	}
	type key struct{ title, release, date string }
	marks := map[key]bool{}
	for _, u := range got {
		marks[key{u.Title, u.Release, u.ReleaseDate}] = u.SearchStart
	}
	want := map[key]bool{
		{"Digital", "", day(-10)}:        false,
		{"Digital", "digital", day(20)}:  true,
		{"NoHome", "", day(-10)}:         false,
		{"NoHome", "search", day(80)}:    true,
		{"Unwanted", "", day(-10)}:       false,
	}
	if len(marks) != len(want) {
		t.Fatalf("entries = %v, want %v", marks, want)
	}
	for k, v := range want {
		if got, ok := marks[k]; !ok || got != v {
			t.Errorf("%v: present %v, search start %v; want %v", k, ok, got, v)
		}
	}
}

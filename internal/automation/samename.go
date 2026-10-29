package automation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tristenlammi/arrmada/internal/download"
	"github.com/tristenlammi/arrmada/internal/indexer"
	"github.com/tristenlammi/arrmada/internal/parser"
	"github.com/tristenlammi/arrmada/internal/series"
)

// Same-named shows (#81): see series/samename.go for the rule. Here is where it applies.
//
//   - The automatic searches — the sweep, RSS, Search now, the quick Grab buttons and
//     upgrades — skip a release that can't be the show's (posted before its episodes
//     aired) and one that could as well be a same-named sibling's: grabbing it could only
//     end in Needs you. A release that states the show's year or country, or whose date
//     rules every sibling out, is taken as before.
//   - The interactive list still shows those releases, marked with why they aren't taken
//     automatically, and they can still be grabbed by hand.
//   - The import holds a grabbed download in Needs you when it can't be told from a
//     sibling's, instead of placing it — or replacing a file — in the show it was grabbed
//     for.
//
// A show with no same-named library show never reaches any of this: sameNameFor answers
// nil and every judge says SameNameNone.

// sameNameJudge judges releases for one show against its same-named library shows. A nil
// judge (the show has none) says SameNameNone to everything.
type sameNameJudge struct {
	s     series.Series
	named []series.Series
}

// sameNameFor is s's judge, or nil when no library show shares its name. s must carry its
// episodes (Get). An error means the siblings couldn't be read: callers skip the show for
// the round rather than grab blind.
func (c *Coordinator) sameNameFor(ctx context.Context, s series.Series) (*sameNameJudge, error) {
	if c.series == nil {
		return nil, nil
	}
	named, err := c.series.SameNameShows(ctx, s)
	if err != nil {
		return nil, err
	}
	if len(named) == 0 {
		return nil, nil
	}
	return &sameNameJudge{s: s, named: named}, nil
}

// judge is a search's verdict on rel: its title as parsed, its indexer publish date exact.
func (j *sameNameJudge) judge(rel indexer.Release) (series.SameName, string) {
	if j == nil {
		return series.SameNameNone, ""
	}
	return series.JudgeSameName(parser.Parse(rel.Title), j.s, j.named, rel.PublishedAt, true)
}

// refuses reports whether an automatic search must leave rel alone, and why.
func (j *sameNameJudge) refuses(rel indexer.Release) (bool, string) {
	switch v, why := j.judge(rel); v {
	case series.SameNameOther, series.SameNameUnsure:
		return true, why
	}
	return false, ""
}

// sameNameRefuses is refuses for a switch case.
func sameNameRefuses(j *sameNameJudge, rel indexer.Release) bool {
	refuse, _ := j.refuses(rel)
	return refuse
}

// releasePublishedKey carries the publish date of the release a grab is being made for, so
// the grab record keeps it (grabs.published_at) for the import's same-name check.
type releasePublishedKey struct{}

// withReleasePublished attaches a release's publish date (zero: unknown) to a grab's ctx.
func withReleasePublished(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, releasePublishedKey{}, t)
}

// WithReleasePublished is withReleasePublished for the HTTP layer's grabs: the publish date
// the interactive list showed for the release (RFC3339, "" when unknown).
func WithReleasePublished(ctx context.Context, rfc3339 string) context.Context {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return ctx
	}
	return withReleasePublished(ctx, t)
}

// releasePublished is the publish date attached to ctx, as stored: RFC3339 UTC, "" when
// unknown.
func releasePublished(ctx context.Context) string {
	t, _ := ctx.Value(releasePublishedKey{}).(time.Time)
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// grabPublished is the publish date recorded on the grab of a download (by info hash, else
// by its release name), zero when none was recorded.
func (c *Coordinator) grabPublished(ctx context.Context, hash, name string) time.Time {
	var at string
	err := sql.ErrNoRows
	if hash != "" {
		err = c.db.QueryRowContext(ctx,
			`SELECT published_at FROM grabs WHERE media_type = 'series' AND lower(info_hash) = lower(?) ORDER BY id DESC LIMIT 1`,
			hash).Scan(&at)
	}
	if errors.Is(err, sql.ErrNoRows) {
		rows, qerr := c.db.QueryContext(ctx,
			`SELECT title, published_at FROM grabs WHERE media_type = 'series' ORDER BY id DESC LIMIT 1000`)
		if qerr != nil {
			return time.Time{}
		}
		defer rows.Close()
		target := normRelease(name)
		for rows.Next() {
			var title, pub string
			if rows.Scan(&title, &pub) == nil && normRelease(title) == target {
				at = pub
				break
			}
		}
	}
	t, perr := time.Parse(time.RFC3339, at)
	if perr != nil {
		return time.Time{}
	}
	return t
}

// sameNameHold is the import's check for a download about to go into show s: the reason
// to hold it in Needs you instead, or "" to import as before. Always "" when no other
// library show shares s's name and fits the download's name (named is SameNamed over the
// sweep's library snapshot, so that costs no query).
//
// The release's publish date is the one recorded on its grab when there is one; otherwise
// only when the client added the download is known, which is never before the release
// was posted — enough to rule out a sibling whose episodes aired later, never to rule out
// s. A download with neither can't be told apart, so it's held.
func (c *Coordinator) sameNameHold(ctx context.Context, s series.Series, named []series.Series, parsed parser.Release, it download.Item) (string, error) {
	if len(named) == 0 || len(series.SameNameSiblings(parsed, s, named)) == 0 {
		return "", nil // no show shares the name, or its year or country names s
	}
	full, err := c.series.Get(ctx, s.ID)
	if err != nil {
		return "", err
	}
	j, err := c.sameNameFor(ctx, full)
	if err != nil || j == nil {
		return "", err
	}
	published, exact := c.grabPublished(ctx, it.Hash, it.Name), true
	if published.IsZero() && it.AddedOn > 0 {
		published, exact = time.Unix(it.AddedOn, 0).UTC(), false
	}
	switch v, why := series.JudgeSameName(parsed, full, j.named, published, exact); v {
	case series.SameNameOther, series.SameNameUnsure:
		return fmt.Sprintf("Grabbed for %s, but %s", series.DescribeCandidates([]series.Series{full}), why), nil
	}
	return "", nil
}

// sameNameDropNote is the log line's sample for releases a search skipped as possibly
// another same-named show's.
func sameNameDropNote(title, why string) string {
	return strings.TrimSpace(title + " (" + why + ")")
}

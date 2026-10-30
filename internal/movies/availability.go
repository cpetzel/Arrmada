package movies

import (
	"time"

	"github.com/tristenlammi/arrmada/internal/metadata"
)

// Minimum availability is when a monitored film starts being searched for: "announced"
// at once, "inCinemas" from its release date, "released" once it is out at home. This
// file is the one rule; everything that asks "is it out yet?" — the scheduled search and
// RSS sync, a search started by an add or a request approval, Wanted's Upcoming, the
// movie page, the Calendar, Discover and a requester's request card — asks
// AvailabilityOf (or AvailabilityFor, for a film not in the library). A person's own
// Search never asks: it searches whatever the film's availability.

// HomeAfterCinema is how long after its cinema release a film counts as out at home when
// TMDB knows no digital or disc date for it: Radarr's rule (it waits the same 90 days, and
// Arrmada has no availability-delay setting of its own). Cinema-to-home windows run
// 17–90 days, so by then a proper release exists; before it, almost everything is a cam.
const HomeAfterCinema = 90

// lateHomeRelease is how far after the cinema date a digital or disc date stops being the
// film's first home release: past a year it is a re-release (a 4K disc of an old film, a
// region that only now gets a disc), and the film is counted out a year after cinemas
// rather than held back until the re-release.
const lateHomeRelease = 365

// What decides Availability.From.
const (
	AvailableByDigital  = "digital"  // the digital release date
	AvailableByPhysical = "physical" // the disc release date
	AvailableByCinema   = "cinema"   // no home release date: the cinema date + HomeAfterCinema days
	AvailableByRelease  = "release"  // "inCinemas": the film's main release date
)

// Availability says whether a film has reached its minimum availability, and when it
// does.
type Availability struct {
	Available bool `json:"available"`
	// From is the day (YYYY-MM-DD) the film becomes available, when a date decides it ("" when
	// none does: "announced", or no dates at all and TMDB's status alone).
	From string `json:"from,omitempty"`
	// By is what From is (AvailableBy*).
	By string `json:"by,omitempty"`
}

// AvailabilityOf is m's availability at now.
func AvailabilityOf(m Movie, now time.Time) Availability {
	var main string
	var rel *metadata.ReleaseDates
	if m.Extra != nil {
		main, rel = m.Extra.ReleaseDate, m.Extra.Releases
	}
	return AvailabilityFor(m.MinAvailability, m.Status, main, rel, now)
}

// Available reports whether m has reached its minimum availability at now.
func Available(m Movie, now time.Time) bool { return AvailabilityOf(m, now).Available }

// AvailabilityFor is the availability of a film with this minimum availability, TMDB
// status, main release date (YYYY-MM-DD) and cinema/digital/physical dates at now — for a
// film that isn't in the library yet (Discover), where the minimum is a request's
// ("released").
//
//   - "announced" (and an unknown value): always.
//   - "inCinemas": from the main release date; with none, when TMDB says Released or
//     doesn't know (lenient: unchanged from before).
//   - "released": from the earlier of the digital and disc dates. With neither, the cinema
//     date (else the main release date) + HomeAfterCinema days. With no date at all, when
//     TMDB says Released.
func AvailabilityFor(minAvail, status, mainDate string, rel *metadata.ReleaseDates, now time.Time) Availability {
	switch minAvail {
	case "inCinemas":
		if d, ok := parseDay(mainDate); ok {
			return availableFrom(d, AvailableByRelease, now)
		}
		return Availability{Available: true}
	case "released":
		return releasedAvailability(status, mainDate, rel, now)
	default: // "announced", or unset
		return Availability{Available: true}
	}
}

func releasedAvailability(status, mainDate string, rel *metadata.ReleaseDates, now time.Time) Availability {
	var home time.Time
	by := ""
	cinema, haveCinema := parseDay(mainDate)
	if rel != nil {
		for _, k := range []struct {
			r    *metadata.DatedRelease
			name string
		}{{rel.Digital, AvailableByDigital}, {rel.Physical, AvailableByPhysical}} {
			if k.r == nil {
				continue
			}
			if d, ok := parseDay(k.r.Date); ok && (by == "" || d.Before(home)) {
				home, by = d, k.name
			}
		}
		if rel.Cinema != nil {
			if d, ok := parseDay(rel.Cinema.Date); ok {
				cinema, haveCinema = d, true
			}
		}
	}
	if by != "" {
		if haveCinema {
			if late := cinema.AddDate(0, 0, lateHomeRelease); home.After(late) {
				return availableFrom(late, AvailableByCinema, now)
			}
		}
		return availableFrom(home, by, now)
	}
	if haveCinema {
		return availableFrom(cinema.AddDate(0, 0, HomeAfterCinema), AvailableByCinema, now)
	}
	// No date at all: TMDB's status is all there is — strict, as before.
	return Availability{Available: status == "Released"}
}

func availableFrom(day time.Time, by string, now time.Time) Availability {
	return Availability{Available: !now.Before(day), From: day.Format(time.DateOnly), By: by}
}

// parseDay reads a stored YYYY-MM-DD (a time after it is ignored).
func parseDay(s string) (time.Time, bool) {
	if len(s) < 10 {
		return time.Time{}, false
	}
	t, err := time.Parse(time.DateOnly, s[:10])
	return t, err == nil
}

package metadata

import "strings"

// ReleaseDates are when a film reached cinemas, digital stores and disc, as Radarr and
// Overseerr show them. Minimum availability "released" waits for the earliest digital or
// physical date (movies.AvailabilityFor). A kind TMDB has no date for is nil.
type ReleaseDates struct {
	Cinema   *DatedRelease `json:"cinema,omitempty"`
	Digital  *DatedRelease `json:"digital,omitempty"`
	Physical *DatedRelease `json:"physical,omitempty"`
	// EarliestDigital and EarliestPhysical are the earliest digital and disc releases in
	// any country, which can be earlier than the region's own: a film out on US digital is
	// out on the web everywhere, whatever day it reaches the region's stores. Availability
	// goes by them; Digital and Physical are what's shown. Nil on dates stored before they
	// were kept (availability then goes by Digital and Physical).
	EarliestDigital  *DatedRelease `json:"earliest_digital,omitempty"`
	EarliestPhysical *DatedRelease `json:"earliest_physical,omitempty"`
	// Region is the discovery region they were picked for ("" for none), so a library film
	// read under another region can be read again (ReleaseRegion).
	Region string `json:"region,omitempty"`
}

// ReleaseRegion is the region release dates are picked for now: the admin's discovery
// region, "" when there's none.
func (t *TMDB) ReleaseRegion() string { return t.regionCode() }

// DatedRelease is one kind of release: the day (YYYY-MM-DD) and the country (ISO 3166-1
// alpha-2) whose release it is, which is the region's own when it has one.
type DatedRelease struct {
	Date    string `json:"date"`
	Country string `json:"country"`
}

// TMDB's release types (/movie/{id}/release_dates).
const (
	tmdbTheatricalLimited = 2
	tmdbTheatrical        = 3
	tmdbDigital           = 4
	tmdbPhysical          = 5
)

// pickReleaseDates reads a film's cinema, digital and physical dates from TMDB's
// release_dates. Each kind is looked for on its own: in region first (the admin's
// discovery region, "" for none), then the US, then the earliest anywhere — so a film
// out on disc only in the UK still shows that date, marked GB. Cinema counts limited
// releases with wide ones (the earliest wins); premieres and TV airings never count.
func pickReleaseDates(results []tmdbReleaseDates, region string) *ReleaseDates {
	return &ReleaseDates{
		Cinema:   pickRelease(results, region, tmdbTheatricalLimited, tmdbTheatrical),
		Digital:  pickRelease(results, region, tmdbDigital),
		Physical: pickRelease(results, region, tmdbPhysical),
		Region:   region,

		EarliestDigital:  earliestRelease(results, "", tmdbDigital),
		EarliestPhysical: earliestRelease(results, "", tmdbPhysical),
	}
}

// pickRelease is the earliest release of one of types: in region, else the US, else
// anywhere (a tie between countries goes to the first code alphabetically, so the answer
// doesn't change with TMDB's order). nil when there is none.
func pickRelease(results []tmdbReleaseDates, region string, types ...int) *DatedRelease {
	if region != "" {
		if r := earliestRelease(results, region, types...); r != nil {
			return r
		}
	}
	if region != "US" {
		if r := earliestRelease(results, "US", types...); r != nil {
			return r
		}
	}
	return earliestRelease(results, "", types...)
}

// earliestRelease is the earliest release of one of types in country ("" for any; a tie
// between countries goes to the first code alphabetically). nil when there is none.
func earliestRelease(results []tmdbReleaseDates, country string, types ...int) *DatedRelease {
	var best *DatedRelease
	for _, r := range results {
		c := strings.ToUpper(strings.TrimSpace(r.Country))
		if c == "" || (country != "" && c != country) {
			continue
		}
		for _, rd := range r.ReleaseDates {
			day := releaseDay(rd.ReleaseDate) // hardcover.go: the YYYY-MM-DD of a timestamp
			if day == "" || !hasType(types, rd.Type) {
				continue
			}
			if best == nil || day < best.Date || (day == best.Date && c < best.Country) {
				best = &DatedRelease{Date: day, Country: c}
			}
		}
	}
	return best
}

func hasType(types []int, t int) bool {
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}

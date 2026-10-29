package metadata

import "strings"

// ReleaseDates are when a film reached cinemas, digital stores and disc, as Radarr and
// Overseerr show them. Display only: nothing searches or grabs by them (minimum
// availability still goes by the film's main release date). A kind TMDB has no date for
// is nil.
type ReleaseDates struct {
	Cinema   *DatedRelease `json:"cinema,omitempty"`
	Digital  *DatedRelease `json:"digital,omitempty"`
	Physical *DatedRelease `json:"physical,omitempty"`
}

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
	}
}

// pickRelease is the earliest release of one of types: in region, else the US, else
// anywhere (a tie between countries goes to the first code alphabetically, so the answer
// doesn't change with TMDB's order). nil when there is none.
func pickRelease(results []tmdbReleaseDates, region string, types ...int) *DatedRelease {
	earliestIn := func(country string) *DatedRelease {
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
	if region != "" {
		if r := earliestIn(region); r != nil {
			return r
		}
	}
	if region != "US" {
		if r := earliestIn("US"); r != nil {
			return r
		}
	}
	return earliestIn("")
}

func hasType(types []int, t int) bool {
	for _, x := range types {
		if x == t {
			return true
		}
	}
	return false
}

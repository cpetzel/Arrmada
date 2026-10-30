import type { MovieAvailability } from "./api";
import { formatReleaseDay } from "./format";

// The words for a film that hasn't reached its minimum availability yet, from the server's
// one rule (movies.AvailabilityOf): what it waits for and the day. Staff pages and the
// Discover sheet only — never the requester's first load.

/** The movie page's line: "Waiting for the digital release on 12 Dec". */
export function availabilityWait(av: MovieAvailability | undefined, now = Date.now()): string {
  if (!av?.from) return "Waiting for it to be released";
  const day = formatReleaseDay(av.from, now);
  switch (av.by) {
    case "digital":
      return `Waiting for the digital release on ${day}`;
    case "physical":
      return `Waiting for the disc release on ${day}`;
    case "cinema":
      return `Waiting for a home release, expected by ${day}`;
    default:
      return `Waiting for its release on ${day}`;
  }
}

/** Discover's line for a film not out yet: "Out on digital 12 Dec — request ahead". null once it's out. */
export function availabilityAhead(av: MovieAvailability | undefined, now = Date.now()): string | null {
  if (!av || av.available || !av.from) return null;
  const day = formatReleaseDay(av.from, now);
  const out = av.by === "digital" ? `Out on digital ${day}` : av.by === "physical" ? `Out on disc ${day}` : av.by === "cinema" ? `At home by ${day}` : `Releases ${day}`;
  return `${out} — request ahead`;
}

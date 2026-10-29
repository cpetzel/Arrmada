import type { ReleaseDates } from "./api";

// A film's or show's shown title: the library's display title (Settings → Library → "Show
// titles as", or the title's own override) when the API sends one, else its title. The
// API keeps `title` canonical (TMDB's English title); only what people read changes.
export const shownTitle = (x: { title: string; display_title?: string }): string => x.display_title || x.title;

// A title's shown overview: in the language titles are shown in (the language mode of
// "Show titles as") when the API sends one, else TMDB's English overview.
export const shownOverview = (x?: { overview?: string; display_overview?: string } | null): string | undefined =>
  x?.display_overview || x?.overview;

// A film's cinema, digital and physical release dates (GH-93), in that order, only the
// kinds TMDB has a date for (YYYY-MM-DD; the page formats it). country is whose release it
// is: the discovery region's when it has one, else the US's or the earliest anywhere. It
// lives here, with the other things a title page shows, because this module is already
// shared by the movie page and the Discover sheet and stays out of a requester's first
// load: a module of its own would add a chunk to the entry's preload lists.
export interface ReleaseFact { label: string; date: string; country: string }
const RELEASE_KINDS = [["cinema", "In cinemas"], ["digital", "Digital"], ["physical", "Physical"]] as const;
export function releaseFacts(r?: ReleaseDates | null): ReleaseFact[] {
  const out: ReleaseFact[] = [];
  for (const [key, label] of RELEASE_KINDS) {
    const rel = r?.[key];
    if (rel?.date) out.push({ label, date: rel.date, country: rel.country });
  }
  return out;
}

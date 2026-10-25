import { describe, expect, it } from "vitest";
import type { Series } from "../../lib/api";
import { cardMeta, nextAiring, seriesLibraryConfig } from "./library";

const now = new Date(2026, 9, 9, 15, 0); // Fri 9 Oct 2026, mid-afternoon

describe("nextAiring", () => {
  it("says today, tomorrow, a weekday within the week, then a date", () => {
    expect(nextAiring("2026-10-09", now)).toBe("Airs today");
    expect(nextAiring("2026-10-10", now)).toBe("Airs tomorrow");
    expect(nextAiring("2026-10-15", now)).toMatch(/^Next Thu/);
    expect(nextAiring("2026-10-20", now)).toMatch(/^Next Oct 20|^Next 20 Oct/);
  });
  it("says nothing for no date or one already past", () => {
    expect(nextAiring(undefined, now)).toBe("");
    expect(nextAiring("2026-10-01", now)).toBe("");
  });
});

describe("cardMeta", () => {
  const show = (over: Partial<Series>): Series => ({ id: 1, tmdb_id: 1, title: "S", year: 2019, monitored: true, quality_profile: "", stats: { episodes: 10, have_files: 9, size_bytes: 0, seasons: 3 }, ...over });
  it("names the network and the next episode for a show still airing", () => {
    expect(cardMeta(show({ network: "Coastal", status: "Returning Series", stats: { episodes: 10, have_files: 9, size_bytes: 0, seasons: 3, next_air_date: "2026-10-10" } }), now)).toBe("Coastal · Airs tomorrow");
  });
  it("falls back to year and seasons for an ended show or one with nothing coming", () => {
    expect(cardMeta(show({ status: "Ended", stats: { episodes: 10, have_files: 9, size_bytes: 0, seasons: 3, next_air_date: "2026-10-10" } }), now)).toBe("2019 · 3 sns");
    expect(cardMeta(show({ stats: { episodes: 1, have_files: 1, size_bytes: 0, seasons: 1 } }), now)).toBe("2019 · 1 sn");
  });
});

// #82: the unfinished shows, whatever their searching switch says.
describe("the Missing episodes filter", () => {
  const filters = seriesLibraryConfig({ fits: new Map(), actions: () => [], rowAction: () => null }).filters;
  const missing = filters.find((f) => f.key === "missing")!;
  const show = (monitored: boolean, stats: Series["stats"]): Series => ({ id: 1, tmdb_id: 1, title: "S", year: 2019, monitored, quality_profile: "", stats });
  it("sits after Wanted, with a hint", () => {
    expect(filters.map((f) => f.key)).toEqual(["all", "monitored", "searching-off", "continuing", "ended", "wanted", "missing"]);
    expect(missing.label).toBe("Missing episodes");
    expect(missing.title).toBeTruthy();
  });
  it("takes a show with aired, monitored episodes missing, searching on or off", () => {
    expect(missing.match!(show(true, { episodes: 10, have_files: 8, size_bytes: 0, seasons: 1, missing: 2 }))).toBe(true);
    expect(missing.match!(show(false, { episodes: 10, have_files: 8, size_bytes: 0, seasons: 1, missing: 2 }))).toBe(true);
    // Wanted leaves out the one with its searching off.
    const wanted = filters.find((f) => f.key === "wanted")!;
    expect(wanted.match!(show(false, { episodes: 10, have_files: 8, size_bytes: 0, seasons: 1, missing: 2 }))).toBe(false);
  });
  it("leaves out a complete show, and episodes not monitored or not aired yet", () => {
    expect(missing.match!(show(true, { episodes: 10, have_files: 10, size_bytes: 0, seasons: 1, missing: 0 }))).toBe(false);
    expect(missing.match!(show(true, { episodes: 10, have_files: 10, size_bytes: 0, seasons: 1, missing: 0, unmonitored_missing: 4 }))).toBe(false);
    expect(missing.match!(show(true, { episodes: 10, have_files: 10, size_bytes: 0, seasons: 1, missing: 0, next_air_date: "2026-11-01" }))).toBe(false);
    expect(missing.match!(show(true, undefined))).toBe(false);
  });
  it("falls back to the gap for an older server that sends no missing count", () => {
    expect(missing.match!(show(true, { episodes: 10, have_files: 7, size_bytes: 0, seasons: 1 }))).toBe(true);
  });
});

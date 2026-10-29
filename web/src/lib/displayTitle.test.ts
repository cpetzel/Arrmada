import { describe, expect, it } from "vitest";
import { releaseFacts, shownOverview, shownTitle } from "./displayTitle";
import { seriesLibraryConfig } from "../pages/series/library";
import type { Series } from "./api";

describe("shownTitle", () => {
  it("shows the display title when the API sends one, else the title", () => {
    expect(shownTitle({ title: "The Travelling Players", display_title: "Ο Θίασος" })).toBe("Ο Θίασος");
    expect(shownTitle({ title: "Dune" })).toBe("Dune");
    expect(shownTitle({ title: "Dune", display_title: "" })).toBe("Dune");
  });
});

describe("shownOverview", () => {
  it("shows the overview in the display language when the API sends one, else the English one", () => {
    expect(shownOverview({ overview: "A shy waitress.", display_overview: "Une serveuse timide." })).toBe("Une serveuse timide.");
    expect(shownOverview({ overview: "A shy waitress." })).toBe("A shy waitress.");
    expect(shownOverview({ overview: "A shy waitress.", display_overview: "" })).toBe("A shy waitress.");
    expect(shownOverview(null)).toBeUndefined();
  });
});

describe("series library", () => {
  const show = (id: number, title: string, display_title?: string) =>
    ({ id, tmdb_id: id, title, display_title, year: 2000, monitored: true, quality_profile: "hd" }) as unknown as Series;
  const config = seriesLibraryConfig({ fits: new Map(), actions: () => [], rowAction: () => null });

  it("sorts by the shown title and finds a show by either name", () => {
    const list = [show(1, "Gomorrah", "Gomorra"), show(2, "Fargo"), show(3, "Babylon Berlin")];
    expect([...list].sort(config.compare).map((s) => s.id)).toEqual([3, 2, 1]);
    const text = config.searchText(list[0]).toLowerCase();
    expect(text).toContain("gomorra");
    expect(text).toContain("gomorrah");
  });
});

// A film's cinema, digital and physical release dates (GH-93): only the kinds there are,
// in order, each with whose release it is.
describe("releaseFacts", () => {
  it("lists the kinds there are, in order", () => {
    expect(releaseFacts({ physical: { date: "2027-01-12", country: "GB" }, cinema: { date: "2026-09-04", country: "AU" } })).toEqual([
      { label: "In cinemas", date: "2026-09-04", country: "AU" },
      { label: "Physical", date: "2027-01-12", country: "GB" },
    ]);
  });
  it("is empty without dates", () => {
    expect(releaseFacts(undefined)).toEqual([]);
    expect(releaseFacts({})).toEqual([]);
    expect(releaseFacts({ digital: { date: "", country: "US" } })).toEqual([]);
  });
});

import { describe, expect, it } from "vitest";
import { matchesFilter, mediaCount } from "./ImportSourcePicker";

describe("manual import picker helpers", () => {
  it("counts videos for movies and series, book files for books, with a + when cut short", () => {
    expect(mediaCount("movies", 1)).toBe("1 video");
    expect(mediaCount("series", 24)).toBe("24 videos");
    expect(mediaCount("series", 0)).toBe("0 videos");
    expect(mediaCount("books", 1)).toBe("1 book file");
    expect(mediaCount("books", 40, true)).toBe("40+ book files");
    expect(mediaCount("movies", 1, true)).toBe("1+ videos");
  });

  it("filters on every word, in any order, ignoring case and dots", () => {
    expect(matchesFilter("Dune.Part.One.2021.2160p", "dune 2160")).toBe(true);
    expect(matchesFilter("Dune.Part.One.2021.2160p", "2160P DUNE")).toBe(true);
    expect(matchesFilter("Dune.Part.One.2021.2160p", "dune 1080")).toBe(false);
    expect(matchesFilter("anything", "")).toBe(true);
    expect(matchesFilter("Amélie.2001", "amélie")).toBe(true);
  });

  it("folds accents both ways, as the server's search does", () => {
    expect(matchesFilter("Amélie.2001.1080p", "amelie")).toBe(true);
    expect(matchesFilter("Amelie.2001.1080p", "Amélie")).toBe(true);
    expect(matchesFilter("Pokémon.Heroes.2002", "pokemon heroes")).toBe(true);
    expect(matchesFilter("Pokémon.Heroes.2002", "pokemon villains")).toBe(false);
  });
});

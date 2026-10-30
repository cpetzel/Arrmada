import { describe, expect, it } from "vitest";
import { formatReleaseDay } from "./format";
import { approvedSearch, availabilityAhead, availabilityWait } from "./movieAvailability";

const now = new Date(2026, 9, 30).getTime();
// The day as the viewer's locale writes it ("12 Dec", "Dec 12"): the words are what's tested.
const dec12 = formatReleaseDay("2026-12-12", now);

describe("availabilityWait", () => {
  it("says what the film waits for, and the day", () => {
    expect(availabilityWait({ available: false, from: "2026-12-12", by: "digital" }, now)).toBe(`Waiting for the digital release on ${dec12}`);
    expect(availabilityWait({ available: false, from: "2026-12-12", by: "physical" }, now)).toBe(`Waiting for the disc release on ${dec12}`);
    expect(availabilityWait({ available: false, from: "2026-12-12", by: "cinema" }, now)).toBe(`Waiting for a home release, expected by ${dec12}`);
    expect(availabilityWait({ available: false, from: "2026-12-12", by: "release" }, now)).toBe(`Waiting for its release on ${dec12}`);
    expect(dec12).toMatch(/12/);
  });
  it("has a plain line without a date", () => {
    expect(availabilityWait({ available: false }, now)).toBe("Waiting for it to be released");
    expect(availabilityWait(undefined, now)).toBe("Waiting for it to be released");
  });
});

describe("availabilityAhead", () => {
  it("names the home release a request waits for", () => {
    expect(availabilityAhead({ available: false, from: "2026-12-12", by: "digital" }, now)).toBe(`Out on digital ${dec12} — request ahead`);
    expect(availabilityAhead({ available: false, from: "2026-12-12", by: "physical" }, now)).toBe(`Out on disc ${dec12} — request ahead`);
    expect(availabilityAhead({ available: false, from: "2026-12-12", by: "cinema" }, now)).toBe(`At home by ${dec12} — request ahead`);
  });
  it("says nothing once it's out, or without a date", () => {
    expect(availabilityAhead({ available: true, from: "2026-10-01", by: "digital" }, now)).toBeNull();
    expect(availabilityAhead({ available: false }, now)).toBeNull();
    expect(availabilityAhead(undefined, now)).toBeNull();
  });
});

describe("approvedSearch", () => {
  it("says an approval waits for a film not out yet", () => {
    expect(approvedSearch(undefined, now)).toBe("searching now");
    expect(approvedSearch({ mode: "search" }, now)).toBe("searching now");
    expect(approvedSearch({ mode: "waiting", from: "2026-12-12" }, now)).toBe(`searches from ${dec12}`);
    expect(approvedSearch({ mode: "waiting" }, now)).toBe("searches once it's out");
  });
});

import { describe, expect, it } from "vitest";
import { describeGoal, fmtSeedHours, goalSourceLabel, seedCheckSummary, seedHoursProblem, seedProgress, seedRatioProblem, type SeedCheckRow } from "./seedingApi";
import { seedBlankMeans } from "../components/connections/IndexerEditor";

const row = (over: Partial<SeedCheckRow> = {}): SeedCheckRow => ({
  hash: "h", name: "Show.S01", indexer: "TL", ratio: 0.4, ratio_known: true, seeding_seconds: 76 * 3600,
  goal: { enabled: true, ratio: 2, hours: 336, source: "indexer" }, outcome: "seeding", ...over,
});

describe("seed goal validation", () => {
  it("takes blank as 0 and refuses a negative or absurd ratio", () => {
    expect(seedRatioProblem("")).toBeNull();
    expect(seedRatioProblem("1.5")).toBeNull();
    expect(seedRatioProblem("-1")).toMatch(/negative/);
    expect(seedRatioProblem("101")).toMatch(/at most 100/);
    expect(seedRatioProblem("1.2.3")).toMatch(/must be a number/);
  });
  it("wants whole hours within five years", () => {
    expect(seedHoursProblem("336")).toBeNull();
    expect(seedHoursProblem("1.5")).toMatch(/whole number/);
    expect(seedHoursProblem("43801")).toMatch(/at most 43800/);
  });
});

describe("seed goal wording", () => {
  it("reads a goal in days where it can", () => {
    expect(fmtSeedHours(336)).toBe("14 days");
    expect(fmtSeedHours(24)).toBe("1 day");
    expect(fmtSeedHours(36)).toBe("36 hours");
    expect(describeGoal(2, 336)).toBe("ratio 2 or 14 days");
    expect(describeGoal(0, 0)).toBe("none");
  });
  it("says where a goal comes from", () => {
    expect(goalSourceLabel(row())).toBe("Indexer (TL)");
    expect(goalSourceLabel(row({ indexer: "manual" }))).toBe("Recorded when grabbed");
    expect(goalSourceLabel(row({ goal: { enabled: true, ratio: 1, hours: 0, source: "global" } }))).toBe("Global default");
  });
  it("shows progress against the goal, and an unknown ratio as unknown", () => {
    expect(seedProgress(row())).toBe("ratio 0.40 of 2 · 3d 4h of 14 days");
    expect(seedProgress(row({ ratio_known: false, ratio: 0, goal: { enabled: true, ratio: 0, hours: 0, source: "none" } }))).toBe("ratio unknown · seeded 3d 4h");
  });
  it("sums up the check", () => {
    expect(seedCheckSummary(0, 0)).toBe("No finished torrents in the client.");
    expect(seedCheckSummary(3, 1)).toBe("3 finished torrents · 1 would be removed on the next check");
    expect(seedCheckSummary(1, 0)).toBe("1 finished torrent · none would be removed now");
  });
});

describe("an indexer left at 0/0", () => {
  it("says it uses the global default when one is set", () => {
    expect(seedBlankMeans({ ratio: 2, hours: 0 })).toBe("uses the global default (ratio 2).");
    expect(seedBlankMeans({ ratio: 0, hours: 336 }, true)).toBe("uses the global default (14 days).");
  });
  it("says what it did before when none is", () => {
    expect(seedBlankMeans({ ratio: 0, hours: 0 })).toMatch(/until you remove it \(no global default/);
    expect(seedBlankMeans({ ratio: 0, hours: 0 }, true)).toMatch(/standard 14 days/);
    expect(seedBlankMeans(null)).toMatch(/unless a global default is set/);
  });
});

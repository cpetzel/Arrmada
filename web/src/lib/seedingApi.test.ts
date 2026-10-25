import { describe, expect, it } from "vitest";
import { checkBlockedBy, describeGoal, fmtSeedHours, goalSourceLabel, seedCheckSummary, seedHoursProblem, seedProgress, seedRatioProblem, type SeedCheckRow } from "./seedingApi";
import { globalGoalText, seedBody, seedModeOf } from "../components/connections/IndexerEditor";

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
  it("blocks the check on an invalid field rather than checking something else", () => {
    expect(checkBlockedBy("2", "336")).toBeNull();
    expect(checkBlockedBy("1.2.3", "0")).toMatch(/^Fix the goal above to check it: .*must be a number/);
    expect(checkBlockedBy("1", "1.5")).toMatch(/whole number/);
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
  it("says whether a goal is the live global one or was recorded when grabbed", () => {
    expect(goalSourceLabel(row())).toBe("TL's goal (recorded when grabbed)");
    expect(goalSourceLabel(row({ indexer: "manual" }))).toBe("Recorded when grabbed");
    expect(goalSourceLabel(row({ goal: { enabled: true, ratio: 1, hours: 0, source: "global" } }))).toBe("Global default (live)");
    expect(goalSourceLabel(row({ goal: { enabled: true, ratio: 0, hours: 336, source: "global", global_unset: true } }))).toMatch(/none set, so the standard 14 days/);
    expect(goalSourceLabel(row({ goal: { enabled: true, ratio: 0, hours: 0, source: "none" } }))).toBe("Seeds forever (recorded when grabbed)");
    expect(goalSourceLabel(row({ goal: { enabled: true, ratio: 0, hours: 336, source: "default" } }))).toBe("Standard window (recorded when grabbed)");
  });
  it("shows progress against the goal, and an unknown ratio as unknown", () => {
    expect(seedProgress(row())).toBe("ratio 0.40 of 2 · 3d 4h of 14 days");
    expect(seedProgress(row({ ratio_known: false, ratio: 0, goal: { enabled: true, ratio: 0, hours: 0, source: "none" } }))).toBe("ratio unknown · seeded 3d 4h");
  });
  it("sums up the check, with the per-check limit", () => {
    expect(seedCheckSummary(0, 0)).toBe("No finished torrents in the client");
    expect(seedCheckSummary(3, 1)).toBe("3 finished torrents · 1 would be removed on the next check");
    expect(seedCheckSummary(1, 0)).toBe("1 finished torrent · none would be removed now");
    expect(seedCheckSummary(14, 13, 3)).toBe("14 finished torrents · 13 would be removed: 10 on the next check, the rest on later ones");
  });
});

describe("an indexer's seed mode", () => {
  it("reads 0/0 as seed forever, never as the global default", () => {
    expect(seedModeOf({ seed_ratio: 0, seed_hours: 0 })).toBe("forever");
    expect(seedModeOf({ seed_ratio: 0, seed_hours: 0, seed_use_global: true })).toBe("global");
    expect(seedModeOf({ seed_ratio: 2, seed_hours: 0 })).toBe("own");
  });
  it("sends only what the mode means", () => {
    expect(seedBody(true, "global", 2, 336)).toEqual({ seed_enabled: true, seed_use_global: true, seed_ratio: 0, seed_hours: 0 });
    expect(seedBody(true, "own", 2, 336)).toEqual({ seed_enabled: true, seed_use_global: false, seed_ratio: 2, seed_hours: 336 });
    expect(seedBody(true, "forever", 2, 336)).toEqual({ seed_enabled: true, seed_use_global: false, seed_ratio: 0, seed_hours: 0 });
    expect(seedBody(false, "global", 2, 336)).toEqual({ seed_enabled: false, seed_use_global: false, seed_ratio: 0, seed_hours: 0 });
  });
  it("names the global default it would use", () => {
    expect(globalGoalText({ ratio: 2, hours: 0 })).toBe("ratio 2");
    expect(globalGoalText({ ratio: 0, hours: 0 })).toMatch(/none set yet — the standard 14 days/);
    expect(globalGoalText(null)).toBe("set in Settings → Downloads");
  });
});

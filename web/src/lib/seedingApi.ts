import { req, type SeedSource } from "./api";

// The global seed goal and its read-only check (the Seeding card on Settings → Downloads). Its own
// module, like plexApi.ts, so none of it lands in a requester's first download.

/** What the next seed check will do with a finished torrent (internal/automation/seedgoal.go). */
export type SeedOutcome = "remove" | "remove_keep_files" | "seeding" | "seeds_forever" | "kept_unmatched" | "not_imported" | "held_for_review" | "not_ours";

/** global_unset: on the global goal while none is set, so it seeds for the standard window. */
export interface SeedGoal { enabled: boolean; ratio: number; hours: number; source: SeedSource; global_unset?: boolean }

export interface SeedCheckRow {
  hash: string;
  name: string;
  indexer?: string;
  /** The computed upload ratio; ratio_known is false when the client gave no honest figure. */
  ratio: number;
  ratio_known: boolean;
  seeding_seconds: number;
  goal: SeedGoal;
  outcome: SeedOutcome;
  /** Why the goal counts as met, when it is. */
  reason?: string;
  /** A removal past the per-check limit: it goes on a later check. */
  later?: boolean;
}

// The same bounds the server holds the global goal and an indexer's to.
export const MAX_SEED_RATIO = 100;
export const MAX_SEED_HOURS = 5 * 365 * 24;

export const seedingApi = {
  /** The check under the saved global goal, or under preview (unsaved) values. */
  check: (preview?: { ratio: number; hours: number }) => {
    const q = preview ? `?ratio=${encodeURIComponent(String(preview.ratio))}&hours=${encodeURIComponent(String(preview.hours))}` : "";
    return req<{ torrents: SeedCheckRow[] }>(`/api/v1/seeding/check${q}`).then((r) => r.torrents ?? []);
  },
};

/** seedRatioProblem says what's wrong with a typed seed ratio, or null. Blank is 0 (none). */
export function seedRatioProblem(v: string): string | null {
  const t = v.trim();
  if (t === "") return null;
  const n = Number(t);
  if (!Number.isFinite(n)) return "The seed ratio must be a number, like 1.5.";
  if (n < 0) return "The seed ratio can't be negative.";
  if (n > MAX_SEED_RATIO) return `The seed ratio can be at most ${MAX_SEED_RATIO}.`;
  return null;
}

/** seedHoursProblem says what's wrong with a typed seeding time in hours, or null. Blank is 0 (none). */
export function seedHoursProblem(v: string): string | null {
  const t = v.trim();
  if (t === "") return null;
  const n = Number(t);
  if (!Number.isInteger(n) || n < 0) return "The seeding time is a whole number of hours, 0 or more.";
  if (n > MAX_SEED_HOURS) return `The seeding time can be at most ${MAX_SEED_HOURS} hours (five years).`;
  return null;
}

/** checkBlockedBy says why the check can't run with these field values, or null. It never
 *  falls back to the saved goal: a check of something other than what's typed would mislead. */
export function checkBlockedBy(ratio: string, hours: string): string | null {
  const problem = seedRatioProblem(ratio) ?? seedHoursProblem(hours);
  return problem ? `Fix the goal above to check it: ${problem}` : null;
}

/** fmtSeedHours reads a goal in hours as days where it divides evenly ("14 days", "36 hours"). */
export function fmtSeedHours(h: number): string {
  if (h > 0 && h % 24 === 0) return `${h / 24} day${h === 24 ? "" : "s"}`;
  return `${h} hour${h === 1 ? "" : "s"}`;
}

/** describeGoal is a goal in words: "ratio 2 or 14 days", "ratio 1.5", "none". */
export function describeGoal(ratio: number, hours: number): string {
  const parts: string[] = [];
  if (ratio > 0) parts.push(`ratio ${ratio}`);
  if (hours > 0) parts.push(fmtSeedHours(hours));
  return parts.length ? parts.join(" or ") : "none";
}

/** goalSourceLabel says where a torrent's goal comes from: the global default, read live
 *  (so a change to it reaches the torrent), or a goal recorded when it was grabbed. */
export function goalSourceLabel(row: Pick<SeedCheckRow, "goal" | "indexer">): string {
  switch (row.goal.source) {
    case "global": return row.goal.global_unset ? "Global default (live) — none set, so the standard 14 days" : "Global default (live)";
    case "off": return row.indexer ? `${row.indexer}: seeding off` : "Seeding off";
    case "none": return "Seeds forever (recorded when grabbed)";
    case "default": return "Standard window (recorded when grabbed)";
    default: return row.indexer && row.indexer !== "manual" ? `${row.indexer}'s goal (recorded when grabbed)` : "Recorded when grabbed";
  }
}

/** outcomeLabel is what will happen to a torrent, in words. */
export function outcomeLabel(o: SeedOutcome): string {
  switch (o) {
    case "remove": return "Removed on the next check, with its data (the library keeps its copy)";
    case "remove_keep_files": return "Removed from the client on the next check; its files stay in the downloads folder";
    case "seeding": return "Keeps seeding";
    case "seeds_forever": return "Seeds until you remove it";
    case "kept_unmatched": return "Goal met, but its import left files it couldn't match — keeps seeding so they aren't deleted with it";
    case "not_imported": return "Not imported yet — never removed before it is";
    case "held_for_review": return "Waiting on a decision in Review — left alone until then";
    case "not_ours": return "Not grabbed by Arrmada — never touched";
  }
}

/** seedProgress is how far a torrent is toward its goal, in words ("ratio 0.40 of 2 · 3d 4h of 14 days"). */
export function seedProgress(row: SeedCheckRow): string {
  const parts: string[] = [];
  const ratio = row.ratio_known ? row.ratio.toFixed(2) : "unknown";
  parts.push(row.goal.ratio > 0 ? `ratio ${ratio} of ${row.goal.ratio}` : `ratio ${ratio}`);
  const seeded = fmtSeconds(row.seeding_seconds);
  parts.push(row.goal.hours > 0 ? `${seeded} of ${fmtSeedHours(row.goal.hours)}` : `seeded ${seeded}`);
  return parts.join(" · ");
}

function fmtSeconds(sec: number): string {
  const h = Math.floor(Math.max(0, sec) / 3600);
  const d = Math.floor(h / 24), rem = h % 24;
  if (d > 0) return rem > 0 ? `${d}d ${rem}h` : `${d}d`;
  return `${h}h`;
}

// seedCheckSummary is the line beside the button once the check has run. later is how many
// of the removals wait for a later check (10 go per check).
export function seedCheckSummary(total: number, removing: number, later = 0): string {
  if (total === 0) return "No finished torrents in the client";
  const t = `${total} finished torrent${total === 1 ? "" : "s"}`;
  if (removing === 0) return `${t} · none would be removed now`;
  const now = removing - later;
  return later > 0
    ? `${t} · ${removing} would be removed: ${now} on the next check, the rest on later ones`
    : `${t} · ${removing} would be removed on the next check`;
}

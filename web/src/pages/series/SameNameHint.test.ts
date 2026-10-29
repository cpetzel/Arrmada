import { describe, expect, it } from "vitest";
import type { SearchAttempt } from "../../lib/api";
import { sameNameSkipped } from "./SameNameHint";

const attempt = (over: Partial<SearchAttempt>): SearchAttempt => ({
  id: 1, media_type: "series", media_id: 4, scope: "", trigger: "sweep", started_at: 1000, duration_ms: 900,
  returned: 0, wrong_title: 0, blocklisted: 0, pending: 0, out_of_scope: 0, rejected: 0, eligible: 0, grabbed: 0,
  reasons: {}, top_reason: "", example: "", grabbed_titles: [], indexer_errors: {}, outcome: "none_suitable", reason: "no-releases",
  ...over,
});

describe("sameNameSkipped (#81)", () => {
  it("shows when the latest search of a scope skipped same-name releases and grabbed nothing", () => {
    expect(sameNameSkipped([attempt({ reasons: { same_name: 2 } })])).toBe(true);
    expect(sameNameSkipped([attempt({ scope: "S02", reasons: { same_name: 1 } }), attempt({ id: 2, started_at: 2000, reasons: { wrong_title: 3 } })])).toBe(true);
  });

  it("stays quiet once something was grabbed, or when nothing was skipped for it", () => {
    expect(sameNameSkipped([attempt({ outcome: "grabbed", grabbed: 1, reasons: { same_name: 2 } })])).toBe(false);
    expect(sameNameSkipped([attempt({ reasons: { wrong_title: 2 } })])).toBe(false);
    // An older search skipped some; the newest one of that scope didn't.
    expect(sameNameSkipped([attempt({ reasons: { same_name: 2 } }), attempt({ id: 2, started_at: 2000, reasons: {} })])).toBe(false);
    expect(sameNameSkipped([])).toBe(false);
  });
});

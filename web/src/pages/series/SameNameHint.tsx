import type { SearchAttempt } from "../../lib/api";
import { useSearchAttempts } from "../../components/LastSearch";
import { latestPerScope } from "../../lib/searchOutcome";

// Why a show that shares its name with another library show never grabs a release that
// doesn't say which one it is, and what to do instead (#81). Staff only, on the series
// page: kept out of the shared search wording so the requester's first load doesn't carry it.
export const SAME_NAME_HINT =
  "Another show in your library has the same name, and releases that don't say which one they are aren't grabbed automatically — pick one in Choose release.";

// sameNameSkipped reports whether the latest search of some scope grabbed nothing and
// skipped releases that could be a same-named show's.
export function sameNameSkipped(attempts: SearchAttempt[]): boolean {
  return latestPerScope(attempts, 50).some((a) => a.grabbed === 0 && (a.reasons?.same_name ?? 0) > 0);
}

export function SameNameHint({ seriesId }: { seriesId: number }) {
  if (!sameNameSkipped(useSearchAttempts("series", seriesId))) return null;
  return (
    <div className="mt-1 text-sm text-ink-dim" data-testid="same-name-hint">
      {SAME_NAME_HINT}
    </div>
  );
}

import { useState } from "react";
import { Link } from "react-router-dom";
import { AutoText } from "../../components/settings/auto";
import { Note, Section } from "../../components/settings/ui";
import { LINKS } from "../../lib/links";
import { describeGoal, goalSourceLabel, outcomeLabel, seedCheckSummary, seedHoursProblem, seedingApi, seedProgress, seedRatioProblem, type SeedCheckRow, type SeedOutcome } from "../../lib/seedingApi";
import { useAutoSave } from "../../lib/useAutoSave";
import { useLoadedSettings } from "../../lib/useSettings";

// SeedingSection is the global seed goal (Settings → Downloads): the ratio and seeding time
// a torrent seeds to when its indexer sets neither of its own. Both 0 — the default — is
// how Arrmada behaved before there was one: such a torrent seeds until someone removes it.
// Under it, a read-only check of every finished torrent against the goal that applies.
export function SeedingSection() {
  const { s, commit } = useLoadedSettings();
  const numEqual = (a: string, b: string) => a.trim() !== "" && b.trim() !== "" && Number(a) === Number(b);
  // Blank is 0 (none), so clearing a field switches that half of the goal off.
  const ratio = useAutoSave(String(s.downloads_seed_ratio ?? 0), (v) => commit({ downloads_seed_ratio: Number(v.trim() || 0) }), { validate: seedRatioProblem, equal: numEqual });
  const hours = useAutoSave(String(s.downloads_seed_hours ?? 0), (v) => commit({ downloads_seed_hours: Number(v.trim() || 0) }), { validate: seedHoursProblem, equal: numEqual });
  return (
    <Section
      id="seeding"
      title="Seeding"
      subtitle="The goal every torrent seeds to when its indexer doesn't set one of its own. An indexer with its own ratio or time keeps it; one with both at 0 uses this. A torrent is removed when it reaches either limit, whichever comes first — and only once Arrmada has imported it, so the library keeps its own copy. Torrents Arrmada didn't grab are never touched."
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <AutoText label="Seed ratio" save={ratio} mono={false} inputMode="decimal" placeholder="0" filter={(v) => v.replace(/[^0-9.]/g, "").slice(0, 6)}>
          <span className="text-xs text-ink-faint">0 = no ratio goal. Uploaded ÷ downloaded, worked out from the byte counts.</span>
        </AutoText>
        <AutoText label="Seeding time (hours)" save={hours} mono={false} inputMode="numeric" placeholder="0" filter={(v) => v.replace(/[^0-9]/g, "").slice(0, 5)}>
          <span className="text-xs text-ink-faint">0 = no time goal. 336 = 14 days.</span>
        </AutoText>
      </div>
      <Note tone="info">
        Both 0 keeps things as they were: a torrent whose indexer sets no goal seeds until you remove it. Private trackers often require a minimum seeding time — a time goal can never remove a torrent before it. Each indexer's own goal is on{" "}
        <Link to={LINKS.indexers} style={{ color: "var(--accent-text)" }}>Settings → Connections → Indexers</Link>.
      </Note>
      <SeedCheck ratio={ratio.value} hours={hours.value} />
    </Section>
  );
}

// SeedCheck lists every finished torrent with its ratio and seeding time against the goal
// that applies, where that goal comes from, and what the next seed check will do. It reads
// with the values in the fields, saved or not, so a goal can be tried before it's kept.
// Read-only: nothing here removes anything.
function SeedCheck({ ratio, hours }: { ratio: string; hours: string }) {
  const [rows, setRows] = useState<SeedCheckRow[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const valid = seedRatioProblem(ratio) === null && seedHoursProblem(hours) === null;
  const run = async () => {
    setBusy(true); setError(null);
    try {
      setRows(await seedingApi.check(valid ? { ratio: Number(ratio.trim() || 0), hours: Number(hours.trim() || 0) } : undefined));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const removing = rows?.filter((r) => r.outcome === "remove" || r.outcome === "remove_keep_files").length ?? 0;
  return (
    <div className="flex flex-col gap-3" data-testid="seed-check">
      <div className="flex flex-wrap items-center gap-3">
        <button onClick={run} disabled={busy} className="rounded-lg px-4 py-2 text-base font-semibold disabled:opacity-50" style={{ border: "1px solid var(--line)", color: "var(--ink)" }}>
          {busy ? "Checking…" : rows ? "Check again" : "Check finished torrents"}
        </button>
        <span className="text-sm text-ink-dim">
          {rows ? seedCheckSummary(rows.length, removing) : "See what the goal above would do to what's seeding now. Nothing is changed."}
        </span>
      </div>
      {error && <p className="m-0 text-sm" style={{ color: "var(--reject-text)" }}>{error}</p>}
      {rows && rows.length > 0 && (
        <div className="rounded-lg" style={{ border: "1px solid var(--line)" }}>
          {rows.map((r, i) => (
            <div key={r.hash || r.name} className="flex flex-col gap-0.5 px-3 py-2" style={i > 0 ? { borderTop: "1px solid var(--line-soft)" } : undefined} data-testid="seed-check-row">
              <div className="truncate text-base font-medium" title={r.name}>{r.name}</div>
              {r.outcome !== "not_ours" && (
                <div className="text-xs text-ink-faint">
                  {goalSourceLabel(r)}{r.goal.enabled ? ` · goal ${describeGoal(r.goal.ratio, r.goal.hours)}` : ""} · {seedProgress(r)}
                </div>
              )}
              <div className="text-sm" style={{ color: OUTCOME_TONE[r.outcome] }}>
                {outcomeLabel(r.outcome)}{r.reason ? ` — ${r.reason}` : ""}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

const OUTCOME_TONE: Record<SeedOutcome, string> = {
  remove: "var(--avoid-text)",
  remove_keep_files: "var(--avoid-text)",
  seeding: "var(--good-text)",
  seeds_forever: "var(--ink-dim)",
  not_imported: "var(--ink-dim)",
  held_for_review: "var(--ink-dim)",
  not_ours: "var(--ink-faint)",
};

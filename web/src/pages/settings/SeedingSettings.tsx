import { useId, useState } from "react";
import { Link } from "react-router-dom";
import { Note, Section, input, inputStyle } from "../../components/settings/ui";
import { Button } from "../../ui/Button";
import { Field } from "../../ui/Field";
import { LINKS } from "../../lib/links";
import { checkBlockedBy, describeGoal, goalSourceLabel, outcomeLabel, seedCheckSummary, seedHoursProblem, seedingApi, seedProgress, seedRatioProblem, type SeedCheckRow, type SeedOutcome } from "../../lib/seedingApi";
import { useLoadedSettings } from "../../lib/useSettings";

// SeedingSection is the global seed goal (Settings → Downloads): the ratio and seeding time
// a torrent seeds to when its indexer is set to "use the global default". Both 0 — the
// default — means none is set. Under it, a read-only check of every finished torrent.
//
// The fields are drafts, saved only by Save. The other settings save when a field is left,
// but leaving a field is exactly what clicking Check does, so a value would be live (and
// removing torrents) before its preview was ever shown.
export function SeedingSection() {
  const { s, commit } = useLoadedSettings();
  const savedRatio = String(s.downloads_seed_ratio ?? 0);
  const savedHours = String(s.downloads_seed_hours ?? 0);
  const [ratio, setRatio] = useState(savedRatio);
  const [hours, setHours] = useState(savedHours);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [savedNote, setSavedNote] = useState(false);
  const ratioProblem = seedRatioProblem(ratio);
  const hoursProblem = seedHoursProblem(hours);
  const num = (v: string) => Number(v.trim() || 0);
  const dirty = num(ratio) !== num(savedRatio) || num(hours) !== num(savedHours);
  const save = async () => {
    setSaving(true); setSaveError(null); setSavedNote(false);
    try {
      await commit({ downloads_seed_ratio: num(ratio), downloads_seed_hours: num(hours) });
      setSavedNote(true);
    } catch (e) {
      setSaveError((e as Error).message);
    } finally {
      setSaving(false);
    }
  };
  return (
    <Section
      id="seeding"
      title="Seeding"
      subtitle="The goal torrents seed to when their indexer is set to use the global default. Indexers you already have keep their own goal, or seed forever, until you switch them on Settings → Connections. A torrent is removed at whichever limit it reaches first, and only once Arrmada has imported it — with its data, since the library keeps its own copy. One you dismissed in Review is removed with its files kept. At most 10 go per check, and checks run every 10 minutes. Torrents Arrmada didn't grab are never touched."
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <DraftField label="Seed ratio" value={ratio} onChange={(v) => { setRatio(v.replace(/[^0-9.]/g, "").slice(0, 6)); setSavedNote(false); }} problem={ratioProblem} inputMode="decimal">
          0 = no ratio goal. Uploaded ÷ downloaded, worked out from the byte counts.
        </DraftField>
        <DraftField label="Seeding time (hours)" value={hours} onChange={(v) => { setHours(v.replace(/[^0-9]/g, "").slice(0, 5)); setSavedNote(false); }} problem={hoursProblem} inputMode="numeric">
          0 = no time goal. 336 = 14 days.
        </DraftField>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <Button onClick={save} disabled={!dirty || saving || ratioProblem !== null || hoursProblem !== null}>{saving ? "Saving…" : "Save"}</Button>
        <span className="text-sm text-ink-dim" role="status">
          {saveError ? <span style={{ color: "var(--reject-text)" }}>{saveError}</span> : dirty ? "Not saved yet — nothing changes until you save." : savedNote ? "Saved." : null}
        </span>
      </div>
      <Note tone="info">
        Whichever limit comes first ends the seed, so a ratio goal can remove a torrent before its seeding time is up. If a private tracker requires a minimum seeding time, set a time goal alone: then nothing goes before it. Indexers added from now on, and torrents you add by hand or upload, follow the global goal only when it includes a time — a ratio alone could end a private tracker's torrent within hours, so without a time they keep the standard 14 days. Both 0 means no global goal: torrents on it seed for the standard 14 days. Each indexer's choice is on{" "}
        <Link to={LINKS.indexers} style={{ color: "var(--accent-text)" }}>Settings → Connections → Indexers</Link>.
      </Note>
      <SeedCheck ratio={ratio} hours={hours} dirty={dirty} />
    </Section>
  );
}

function DraftField({ label, value, onChange, problem, inputMode, children }: {
  label: string; value: string; onChange: (v: string) => void; problem: string | null;
  inputMode: React.HTMLAttributes<HTMLInputElement>["inputMode"]; children: React.ReactNode;
}) {
  const id = useId();
  return (
    <Field label={label} controlId={id}>
      <input
        id={id}
        aria-describedby={`${id}-help`}
        aria-invalid={problem !== null || undefined}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        inputMode={inputMode}
        placeholder="0"
        className={input.replace("font-mono ", "")}
        style={problem ? { ...inputStyle, border: "1px solid var(--reject)" } : inputStyle}
      />
      <span id={`${id}-help`} className="text-xs" style={{ color: problem ? "var(--reject-text)" : "var(--ink-faint)" }}>{problem ?? children}</span>
    </Field>
  );
}

// SeedCheck lists every finished torrent with its ratio and seeding time against the goal
// that applies, where that goal comes from, and what the next seed check will do. It runs
// with the values in the fields, saved or not, so a goal can be tried before it's kept; with
// a field that isn't a valid goal it can't run, and says why rather than quietly checking
// the saved values. Read-only: nothing here removes anything.
function SeedCheck({ ratio, hours, dirty }: { ratio: string; hours: string; dirty: boolean }) {
  const [rows, setRows] = useState<SeedCheckRow[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ran, setRan] = useState<{ ratio: number; hours: number; unsaved: boolean } | null>(null);
  const blocked = checkBlockedBy(ratio, hours);
  const run = async () => {
    if (blocked) return;
    const goal = { ratio: Number(ratio.trim() || 0), hours: Number(hours.trim() || 0) };
    setBusy(true); setError(null);
    try {
      setRows(await seedingApi.check(goal));
      setRan({ ...goal, unsaved: dirty });
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const removing = rows?.filter((r) => r.outcome === "remove" || r.outcome === "remove_keep_files").length ?? 0;
  const later = rows?.filter((r) => r.later).length ?? 0;
  return (
    <div className="flex flex-col gap-3" data-testid="seed-check">
      <div className="flex flex-wrap items-center gap-3">
        <button onClick={run} disabled={busy || blocked !== null} className="rounded-lg px-4 py-2 text-base font-semibold disabled:opacity-50" style={{ border: "1px solid var(--line)", color: "var(--ink)" }}>
          {busy ? "Checking…" : rows ? "Check again" : "Check finished torrents"}
        </button>
        <span className="text-sm text-ink-dim">
          {blocked ?? (rows && ran
            ? `${seedCheckSummary(rows.length, removing, later)} — with a global goal of ${describeGoal(ran.ratio, ran.hours)}${ran.unsaved ? " (not saved)" : ""}.`
            : "See what the goal above would do to what's seeding now, before you save it. Nothing is changed.")}
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
                {outcomeLabel(r.outcome)}{r.later ? " (on a later check: 10 go per check)" : ""}{r.reason ? ` — ${r.reason}` : ""}
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
  kept_unmatched: "var(--ink-dim)",
  not_imported: "var(--ink-dim)",
  held_for_review: "var(--ink-dim)",
  not_ours: "var(--ink-faint)",
};

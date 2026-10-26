import { useCallback, useEffect, useState } from "react";
import { req, type Movie, type MovieOtherFile, type MovieOtherFiles, type MovieVersion } from "../../lib/api";
import { qualityApi } from "../../lib/qualityApi";
import { Button, Modal, useToast } from "../../ui";

// The other files in a film's folder: videos Arrmada doesn't use (another cut, an old copy,
// a second film left there). The film's page shows a "+N other files" chip by its file;
// the chip opens the list, looked at again on the server. Read-only: the one thing it
// offers is recording a file, where it is, as an extra version. Kept here, in the staff
// pages' chunks, not in lib/api.ts, which every page (the requester's first load) pays for.

export const otherFilesApi = {
  list: (movieId: number) => req<MovieOtherFiles>(`/api/v1/movies/${movieId}/other-files`),
  addAsVersion: (movieId: number, body: { path: string; label?: string; quality_profile?: string }) =>
    req<MovieVersion>(`/api/v1/movies/${movieId}/other-files/version`, { method: "POST", body: JSON.stringify(body) }),
  // Stop tracking (keep the file): the version's record goes, its file stays on disk.
  untrackVersion: (movieId: number, versionId: number) =>
    req<void>(`/api/v1/movies/${movieId}/versions/${versionId}/untrack`, { method: "POST" }),
};

/** "+1 other file", "+3 other files". */
export function otherFilesLabel(n: number): string {
  return `+${n} other file${n === 1 ? "" : "s"}`;
}

function fmtSize(bytes: number): string {
  if (bytes <= 0) return "—";
  const gb = bytes / 1024 ** 3;
  return gb >= 1 ? `${gb.toFixed(2)} GB` : `${(bytes / 1024 ** 2).toFixed(0)} MB`;
}

// OtherFilesChip sits by the film's file: how many other videos its folder holds, as the
// last scan or refresh found them. Nothing shows when there are none.
export function OtherFilesChip({ movie, onChange }: { movie: Movie; onChange: () => void }) {
  const [open, setOpen] = useState(false);
  const n = movie.other_files?.count ?? 0;
  if (!movie.has_file || n <= 0) return null;
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-haspopup="dialog"
        title="Videos in this film's folder that Arrmada doesn't use — show them"
        className="inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-semibold"
        style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink-dim)" }}
      >
        {otherFilesLabel(n)}
      </button>
      {open && <OtherFilesModal movie={movie} onClose={() => setOpen(false)} onChange={onChange} />}
    </>
  );
}

// OtherFilesModal lists the folder's other videos, looked at now.
export function OtherFilesModal({ movie, onClose, onChange }: { movie: Movie; onClose: () => void; onChange: () => void }) {
  const [data, setData] = useState<MovieOtherFiles | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState<MovieOtherFile | null>(null);

  const load = useCallback(() => {
    setError(null);
    otherFilesApi.list(movie.id).then(setData).catch((e: Error) => setError(e.message));
  }, [movie.id]);
  useEffect(load, [load]);

  const files = data?.files ?? [];
  return (
    <Modal
      onClose={onClose}
      title="Other files in this folder"
      size="md"
      footer={<Button variant="ghost" onClick={onClose}>Close</Button>}
    >
      <p className="mb-3 mt-0 text-sm text-ink-dim">
        Videos in the film's folder that Arrmada doesn't use. It only lists them: nothing here moves, renames or deletes a file.
      </p>
      {data?.shared && (
        <p className="mb-3 mt-0 rounded-lg p-2.5 text-sm text-ink-dim" style={{ background: "var(--panel-2)" }}>
          This folder isn't only this film's, so only its top level is listed, and only files no film in your library uses.
        </p>
      )}
      {error && <div className="mb-2 text-sm" style={{ color: "var(--reject-text)" }} role="alert">{error}</div>}
      {!data && !error && <div className="py-4 text-sm text-ink-dim" role="status">Looking in the folder…</div>}
      {data && files.length === 0 && <div className="py-4 text-sm text-ink-dim" role="status">No other video files are in the folder now.</div>}
      {files.length > 0 && (
        <ul className="m-0 flex list-none flex-col gap-2 p-0" aria-label="Other files">
          {files.map((f) => (
            <li key={f.path} className="rounded-xl p-3" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
              <div className="break-all text-base font-semibold">{f.name}</div>
              {f.rel_path !== f.name && <div className="mt-0.5 break-all font-mono text-xs text-ink-faint">{f.rel_path}</div>}
              <div className="mt-1.5 flex flex-wrap items-center gap-2">
                {f.resolution && (
                  <span className="rounded px-2 py-0.5 text-xs font-semibold" style={{ background: "var(--accent-soft)", color: "var(--accent-text)" }} title={f.probed ? "Read from the file" : "From the file's name"}>
                    {f.resolution}
                  </span>
                )}
                <span className="font-mono text-sm text-ink-dim">{fmtSize(f.size_bytes)}</span>
                <span className="flex-1" />
                {adding?.path !== f.path && (
                  <Button size="sm" variant="secondary" onClick={() => setAdding(f)}>Add as a version</Button>
                )}
              </div>
              {adding?.path === f.path && (
                <AddAsVersion
                  movie={movie}
                  file={f}
                  onCancel={() => setAdding(null)}
                  onDone={() => { setAdding(null); onChange(); load(); }}
                />
              )}
            </li>
          ))}
        </ul>
      )}
      {data?.truncated && <p className="mb-0 mt-2 text-xs text-ink-faint">The folder holds more than this list shows.</p>}
    </Modal>
  );
}

// AddAsVersion says what recording a file as a version does, and does it: always a new,
// unmonitored version (labelled, with a quality profile) — never an existing one, which
// may be monitored or have a grab in flight that would replace the file.
function AddAsVersion({ movie, file, onCancel, onDone }: { movie: Movie; file: MovieOtherFile; onCancel: () => void; onDone: () => void }) {
  const toast = useToast();
  const [label, setLabel] = useState(file.resolution || "Other copy");
  const [profile, setProfile] = useState("");
  const [profiles, setProfiles] = useState<{ key: string; name: string }[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    qualityApi.qualityProfiles("movie").then((r) => {
      setProfiles(r.profiles.map((p) => ({ key: p.key, name: p.name })));
      const def = r.profiles.find((p) => p.is_default) ?? r.profiles[0];
      if (def) setProfile(def.key);
    }).catch(() => {});
  }, []);

  const save = async () => {
    if (!label.trim()) {
      setError("Give the version a label (e.g. 4K, Extended).");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const v = await otherFilesApi.addAsVersion(movie.id, { path: file.path, label: label.trim(), quality_profile: profile });
      toast(`Recorded ${file.name} as the “${v.label}” version.`);
      onDone();
    } catch (e) {
      setError((e as Error).message);
      setBusy(false);
    }
  };

  const fieldStyle = { background: "var(--panel-2)", border: "1px solid var(--line)", color: "var(--ink)" };
  const id = `other-${movie.id}-${file.rel_path}`.replace(/[^A-Za-z0-9_-]/g, "_");
  return (
    <div className="mt-3 rounded-lg p-3" style={{ background: "var(--panel-2)" }}>
      <p className="mb-2 mt-0 text-sm text-ink-dim">
        Arrmada records this file where it is, under its own name, as a new version of the film. Nothing is moved, renamed or copied now.
        The version starts unmonitored, so no upgrade replaces this file; turn Monitor on later if you want upgrades.
      </p>
      <p className="mb-2 mt-0 text-sm text-ink-dim">
        From then on it's handled like any other file Arrmada tracks: Rename or Organize may move it to your naming scheme's path, and Convert may convert it if automatic converting is on.
      </p>
      <p className="mb-3 mt-0 text-sm text-ink-dim">
        To undo, use <strong>Stop tracking</strong> on the version: it keeps the file. <strong>Remove version</strong> moves the file to the recycle bin.
      </p>
      <label htmlFor={`${id}-label`} className="mb-1 block font-mono text-2xs font-bold uppercase text-accent-text">Label</label>
      <input id={`${id}-label`} value={label} onChange={(e) => setLabel(e.target.value)} placeholder="4K · Extended · Remux" className="mb-3 w-full rounded-lg px-3 py-2 text-base" style={fieldStyle} />
      <label htmlFor={`${id}-profile`} className="mb-1 block font-mono text-2xs font-bold uppercase text-accent-text">Quality profile</label>
      <select id={`${id}-profile`} value={profile} onChange={(e) => setProfile(e.target.value)} className="mb-3 w-full rounded-lg px-3 py-2 text-base" style={fieldStyle}>
        {profiles.map((p) => <option key={p.key} value={p.key}>{p.name}</option>)}
      </select>
      {error && <div className="mb-2 text-sm" style={{ color: "var(--reject-text)" }} role="alert">{error}</div>}
      <div className="flex flex-wrap justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onCancel} disabled={busy}>Cancel</Button>
        <Button size="sm" variant="primary" onClick={save} busy={busy} busyLabel="Adding…">Add as a version</Button>
      </div>
    </div>
  );
}

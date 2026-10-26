import { useEffect, useState } from "react";
import { req, type Movie, type MovieOtherFiles } from "../../lib/api";
import { Button, Modal } from "../../ui";

// The other files in a film's folder: videos that are there but that Arrmada doesn't track
// (another cut, an old copy, a second film left there). The film's page shows a
// "+N other files" chip by its file; the chip opens the list, looked at again on the
// server. Read-only, with no action on a file. Kept here, in the staff pages' chunks, not
// in lib/api.ts, which every page (the requester's first load) pays for.

export const otherFilesApi = {
  list: (movieId: number) => req<MovieOtherFiles>(`/api/v1/movies/${movieId}/other-files`),
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
export function OtherFilesChip({ movie }: { movie: Movie }) {
  const [open, setOpen] = useState(false);
  const n = movie.other_files?.count ?? 0;
  if (!movie.has_file || n <= 0) return null;
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-haspopup="dialog"
        title="Videos in this film's folder that Arrmada doesn't track — show them"
        className="inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-semibold"
        style={{ border: "1px solid var(--line)", background: "var(--panel-2)", color: "var(--ink-dim)" }}
      >
        {otherFilesLabel(n)}
      </button>
      {open && <OtherFilesModal movie={movie} onClose={() => setOpen(false)} />}
    </>
  );
}

// OtherFilesModal lists the folder's other videos, looked at now.
export function OtherFilesModal({ movie, onClose }: { movie: Movie; onClose: () => void }) {
  const [data, setData] = useState<MovieOtherFiles | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    otherFilesApi.list(movie.id)
      .then((d) => { if (live) setData(d); })
      .catch((e: Error) => { if (live) setError(e.message); });
    return () => { live = false; };
  }, [movie.id]);

  const files = data?.files ?? [];
  return (
    <Modal
      onClose={onClose}
      title="Other files in this folder"
      size="md"
      footer={<Button variant="ghost" onClick={onClose}>Close</Button>}
    >
      <p className="mb-3 mt-0 text-sm text-ink-dim">
        These video files are in the film's folder, but Arrmada doesn't track them.
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
              </div>
            </li>
          ))}
        </ul>
      )}
      {data?.truncated && <p className="mb-0 mt-2 text-xs text-ink-faint">The folder holds more than this list shows.</p>}
    </Modal>
  );
}

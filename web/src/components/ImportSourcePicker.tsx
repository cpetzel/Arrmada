import { useEffect, useId, useMemo, useState, type FormEvent } from "react";
import { Button, StatusChip, Tabs } from "../ui";
import { TabPanel } from "../ui/Tabs";
import { Input } from "../ui/Input";
import { formatBytes } from "../lib/format";
import { manualImportApi, type ImportFolder, type ImportKind, type ImportSearchHit, type ImportSuggestion } from "../lib/manualImportApi";

// ImportSourcePicker is where a manual import looks for files: the finished downloads that
// look like this title (one click), a folder browser over the folders Arrmada was given
// (downloads, the libraries), a name search across the downloads folders, and a typed
// path for whoever knows exactly where it is. path null is the default, the downloads
// folder; onPick hands the modal the folder or file to list.

type Mode = "browse" | "search" | "type";
const MODES = [
  { key: "browse" as const, label: "Browse" },
  { key: "search" as const, label: "Search downloads" },
  { key: "type" as const, label: "Type a path" },
];

// mediaCount says what a count counts: "3 videos", "1 book file".
export function mediaCount(kind: ImportKind, n: number, more?: boolean): string {
  const [one, many] = kind === "books" ? ["book file", "book files"] : ["video", "videos"];
  return `${n}${more ? "+" : ""} ${n === 1 && !more ? one : many}`;
}

// matchesFilter keeps the names holding every word typed, in any order ("dune 2160").
export function matchesFilter(name: string, filter: string): boolean {
  const words = filter.toLowerCase().split(/[^\p{L}\p{N}]+/u).filter(Boolean);
  const hay = name.toLowerCase();
  return words.every((w) => hay.includes(w));
}

export function ImportSourcePicker({ kind, id, path, onPick, disabled }: { kind: ImportKind; id: number; path: string | null; onPick: (path: string | null) => void; disabled?: boolean }) {
  const [suggestions, setSuggestions] = useState<ImportSuggestion[]>([]);
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState<Mode>("browse");
  const idPrefix = useId().replace(/:/g, "");
  const panelId = `${idPrefix}-picker`;

  // Suggestions are a convenience: a failure (no client answering) just shows none.
  useEffect(() => {
    manualImportApi.suggestions(kind, id).then((r) => setSuggestions(r.suggestions ?? [])).catch(() => setSuggestions([]));
  }, [kind, id]);

  const pick = (p: string | null) => { setOpen(false); onPick(p); };

  return (
    <div className="mb-3">
      {suggestions.length > 0 && (
        <section className="mb-3" aria-labelledby={`${idPrefix}-sugg`}>
          <h3 id={`${idPrefix}-sugg`} className="m-0 mb-1.5 text-xs font-semibold text-ink-dim">Suggested downloads</h3>
          <ul className="m-0 list-none p-0">
            {suggestions.map((s) => {
              const using = s.path === path;
              return (
                <li key={s.path} className="mb-1.5 flex items-center gap-3 rounded-lg p-2.5" style={{ border: `1px solid ${using ? "var(--accent-line)" : "var(--line)"}`, background: "var(--panel-2)" }}>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-base font-medium" title={s.path}>{s.name}</div>
                    <div className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-xs text-ink-faint">
                      <span>{s.in_client ? "Finished download" : "In a downloads folder"}</span>
                      {s.size_bytes ? <span>{formatBytes(s.size_bytes)}</span> : null}
                      {s.not_managed && <StatusChip tone="avoid">Not managed</StatusChip>}
                      {s.imported && <StatusChip tone="faint">Imported before</StatusChip>}
                    </div>
                  </div>
                  <Button size="sm" variant={using ? "secondary" : "primary"} disabled={disabled || using} onClick={() => pick(s.path)} aria-label={`${using ? "Using" : "Use"} ${s.name}`}>
                    {using ? "Using" : "Use this"}
                  </Button>
                </li>
              );
            })}
          </ul>
        </section>
      )}

      <div className="flex flex-wrap items-center gap-2 rounded-lg px-2.5 py-2" style={{ border: "1px solid var(--line)" }}>
        <span className="font-mono text-2xs font-bold uppercase text-accent-text">Looking in</span>
        <span className="min-w-0 flex-1 truncate font-mono text-sm text-ink-dim" title={path ?? undefined} data-testid="import-source">{path ?? "the downloads folder"}</span>
        {path !== null && <Button size="sm" variant="ghost" disabled={disabled} onClick={() => pick(null)}>Downloads folder</Button>}
        <Button size="sm" disabled={disabled} aria-expanded={open} aria-controls={open ? panelId : undefined} onClick={() => setOpen((o) => !o)}>
          {open ? "Close folder picker" : "Choose a folder…"}
        </Button>
      </div>

      {open && (
        <div id={panelId} className="mt-2 rounded-lg p-2.5" style={{ border: "1px solid var(--line)", background: "var(--panel)" }}>
          <Tabs tabs={MODES} value={mode} onChange={setMode} idPrefix={idPrefix} label="Ways to find the files" className="mb-2.5 border-b" />
          <TabPanel idPrefix={idPrefix} value={mode}>
            {mode === "browse" && <FolderBrowser kind={kind} start={path} onPick={pick} />}
            {mode === "search" && <DownloadsSearch kind={kind} onPick={pick} />}
            {mode === "type" && <TypedPath initial={path ?? ""} onPick={pick} />}
          </TabPanel>
        </div>
      )}
    </div>
  );
}

function FolderBrowser({ kind, start, onPick }: { kind: ImportKind; start: string | null; onPick: (p: string) => void }) {
  const [folder, setFolder] = useState<ImportFolder | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const filterId = useId();

  const go = (p?: string) => {
    setErr(null);
    setFilter("");
    manualImportApi.folder(kind, p).then(setFolder).catch((e: Error) => setErr(e.message));
  };
  // Open where the import is looking now; a file (a suggestion) or a folder that's gone
  // opens on the top level instead.
  useEffect(() => {
    if (!start) { go(); return; }
    manualImportApi.folder(kind, start).then(setFolder).catch(() => go());
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const dirs = useMemo(() => (folder?.dirs ?? []).filter((d) => matchesFilter(d.label ?? d.name, filter)), [folder, filter]);
  const files = useMemo(() => (folder?.files ?? []).filter((f) => matchesFilter(f.name, filter)), [folder, filter]);
  const top = folder !== null && folder.path === "";

  return (
    <div>
      <nav aria-label="Folder path" className="mb-2 flex flex-wrap items-center gap-1 text-sm">
        <button type="button" onClick={() => go()} className="rounded px-1.5 py-0.5 font-semibold hover:bg-[var(--panel-2)]" style={{ color: top ? "var(--ink)" : "var(--accent-text)" }} aria-current={top ? "page" : undefined}>
          All folders
        </button>
        {folder?.crumbs.map((c, i) => {
          const last = i === folder.crumbs.length - 1;
          return (
            <span key={c.path} className="flex min-w-0 items-center gap-1">
              <span aria-hidden="true" className="text-ink-faint">/</span>
              <button type="button" onClick={() => go(c.path)} className="max-w-[12rem] truncate rounded px-1.5 py-0.5 hover:bg-[var(--panel-2)]" style={{ color: last ? "var(--ink)" : "var(--accent-text)", fontWeight: last ? 600 : 400 }} aria-current={last ? "page" : undefined} title={c.path}>
                {c.name}
              </button>
            </span>
          );
        })}
      </nav>

      <label htmlFor={filterId} className="sr-only">Filter this folder</label>
      <Input id={filterId} type="search" value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter this folder…" className="mb-2 w-full px-3 py-1.5 text-base" />

      {err && <div className="mb-2 text-sm" style={{ color: "var(--reject-text)" }}>{err}</div>}

      <ul className="thin-scroll m-0 max-h-[36vh] list-none overflow-y-auto rounded-lg p-0" style={{ border: "1px solid var(--line)" }} aria-label="Folders and files">
        {folder === null ? (
          <li className="p-4 text-center text-sm text-ink-faint">Loading…</li>
        ) : dirs.length + files.length === 0 ? (
          <li className="p-4 text-center text-sm text-ink-faint">{filter ? "Nothing here matches." : top ? "No folders to import from. Set them in Settings → Library." : "No folders or importable files here."}</li>
        ) : (
          <>
            {dirs.map((d) => (
              <li key={d.path} className="flex items-center gap-1" style={{ borderTop: "1px solid var(--line-soft)" }}>
                <button type="button" onClick={() => go(d.path)} className="flex min-w-0 flex-1 items-center gap-2 px-3 py-2 text-left hover:bg-[var(--panel-2)]" title={d.path} aria-label={`Open ${d.label ?? d.name}`}>
                  <span aria-hidden="true" style={{ color: "var(--accent-text)" }}>📁</span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-base">{d.label ?? d.name}</span>
                    {top && <span className="block truncate font-mono text-xs text-ink-faint">{d.path}</span>}
                  </span>
                  {d.media !== undefined && (
                    <span className="flex-none font-mono text-xs" style={{ color: d.media > 0 ? "var(--good-text)" : "var(--ink-faint)" }}>{mediaCount(kind, d.media, d.more)}</span>
                  )}
                </button>
                {!top && (d.media ?? 0) > 0 && (
                  <Button size="sm" variant="ghost" className="mr-1.5 flex-none" onClick={() => onPick(d.path)} aria-label={`Use ${d.name}`}>Use</Button>
                )}
              </li>
            ))}
            {files.map((f) => (
              <li key={f.path} className="flex items-center gap-2 px-3 py-2" style={{ borderTop: "1px solid var(--line-soft)" }}>
                <span aria-hidden="true" className="text-ink-faint">{kind === "books" ? "📖" : "🎞"}</span>
                <span className="min-w-0 flex-1 truncate text-base" title={f.path}>{f.name}</span>
                <span className="flex-none font-mono text-xs text-ink-faint">{formatBytes(f.size_bytes)}</span>
                <Button size="sm" variant="ghost" className="flex-none" onClick={() => onPick(f.path)} aria-label={`Use ${f.name}`}>Use</Button>
              </li>
            ))}
          </>
        )}
      </ul>
      {folder?.truncated && <div className="mt-1.5 text-xs text-ink-faint">This folder is very large, so not everything is shown — filter, search, or open a sub-folder.</div>}

      <div className="mt-2.5 flex flex-wrap items-center justify-end gap-2">
        <span className="mr-auto text-xs text-ink-faint">{top ? "Open a folder to choose it." : "Open folders to find the download, then use it."}</span>
        <Button variant="primary" size="sm" disabled={!folder || top} onClick={() => folder && onPick(folder.path)}>Use this folder</Button>
      </div>
    </div>
  );
}

function DownloadsSearch({ kind, onPick }: { kind: ImportKind; onPick: (p: string) => void }) {
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<ImportSearchHit[] | null>(null);
  const [truncated, setTruncated] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const qId = useId();

  const run = (e: FormEvent) => {
    e.preventDefault();
    if (q.trim().length < 2) { setErr("Type at least two letters to search."); return; }
    setBusy(true);
    setErr(null);
    manualImportApi
      .search(kind, q.trim())
      .then((r) => { setHits(r.results ?? []); setTruncated(!!r.truncated); })
      .catch((e: Error) => setErr(e.message))
      .finally(() => setBusy(false));
  };

  return (
    <div>
      <form onSubmit={run} className="mb-2 flex gap-2" role="search">
        <label htmlFor={qId} className="sr-only">Search the downloads folders</label>
        <Input id={qId} type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Folder or file name…" className="min-w-0 flex-1 px-3 py-1.5 text-base" />
        <Button type="submit" size="sm" busy={busy} busyLabel="Searching…">Search</Button>
      </form>
      {err && <div className="mb-2 text-sm" style={{ color: "var(--reject-text)" }}>{err}</div>}
      {hits !== null && (
        <ul className="thin-scroll m-0 max-h-[36vh] list-none overflow-y-auto rounded-lg p-0" style={{ border: "1px solid var(--line)" }} aria-label="Search results">
          {hits.length === 0 ? (
            <li className="p-4 text-center text-sm text-ink-faint">Nothing in the downloads folders by that name.</li>
          ) : hits.map((h) => (
            <li key={h.path} className="flex items-center gap-2 px-3 py-2" style={{ borderTop: "1px solid var(--line-soft)" }}>
              <span aria-hidden="true" style={{ color: h.dir ? "var(--accent-text)" : "var(--ink-faint)" }}>{h.dir ? "📁" : kind === "books" ? "📖" : "🎞"}</span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-base" title={h.path}>{h.name}</span>
                <span className="block truncate font-mono text-xs text-ink-faint">{h.where} / {h.rel}{h.size_bytes ? ` · ${formatBytes(h.size_bytes)}` : ""}</span>
              </span>
              <Button size="sm" variant="ghost" className="flex-none" onClick={() => onPick(h.path)} aria-label={`Use ${h.name}`}>Use</Button>
            </li>
          ))}
        </ul>
      )}
      {truncated && <div className="mt-1.5 text-xs text-ink-faint">Showing the first matches — add a word to narrow it down.</div>}
      {hits === null && !err && <div className="text-xs text-ink-faint">Looks a few folders deep in every downloads folder, by name.</div>}
    </div>
  );
}

function TypedPath({ initial, onPick }: { initial: string; onPick: (p: string) => void }) {
  const [value, setValue] = useState(initial);
  const inputId = useId();
  return (
    <form onSubmit={(e) => { e.preventDefault(); if (value.trim()) onPick(value.trim()); }}>
      <label htmlFor={inputId} className="mb-1 block text-sm text-ink-dim">Folder or file path</label>
      <div className="flex gap-2">
        <Input id={inputId} value={value} onChange={(e) => setValue(e.target.value)} placeholder="/downloads/Some.Release.2024" spellCheck={false} autoCapitalize="off" autoCorrect="off" className="min-w-0 flex-1 px-3 py-1.5 font-mono text-base" />
        <Button type="submit" size="sm" disabled={!value.trim()}>Use this path</Button>
      </div>
      <div className="mt-1.5 text-xs text-ink-faint">It must be inside your downloads or library folders.</div>
    </form>
  );
}

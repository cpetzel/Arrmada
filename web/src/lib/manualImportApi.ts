import { req, type ManualImportList } from "./api";

// The manual-import folder picker's calls. They live here, not in api.ts, so the picker
// (staff only) adds nothing to a requester's first load.

export type ImportKind = "movies" | "series" | "books";

/** A finished download whose name reads as this title. */
export interface ImportSuggestion {
  name: string;
  path: string;
  size_bytes?: number;
  /** In a download client; otherwise found in a downloads folder. */
  in_client: boolean;
  /** In a client, but nothing in Arrmada grabbed it. */
  not_managed?: boolean;
  /** Imported before (Arrmada recorded its hash). */
  imported?: boolean;
  added_on?: number;
}

/** A sub-folder in the picker. media is absent on the top level (the import roots). */
export interface ImportFolderEntry { name: string; path: string; media?: number; more?: boolean; label?: string }
export interface ImportFileEntry { name: string; path: string; size_bytes: number }
/** One folder: path "" is the top level, the list of import roots. */
export interface ImportFolder {
  path: string;
  crumbs: { name: string; path: string }[];
  dirs: ImportFolderEntry[];
  files: ImportFileEntry[];
  truncated?: boolean;
}
export interface ImportSearchHit { name: string; path: string; dir: boolean; size_bytes?: number; where: string; rel: string }

const q = (path?: string | null) => (path ? `?path=${encodeURIComponent(path)}` : "");

export const manualImportApi = {
  /** The importable files in path (the downloads folder when none), as the import would see them. */
  list: <T>(kind: ImportKind, id: number, path?: string | null) => req<ManualImportList<T>>(`/api/v1/${kind}/${id}/manualimport${q(path)}`),
  suggestions: (kind: ImportKind, id: number) => req<{ suggestions: ImportSuggestion[] }>(`/api/v1/${kind}/${id}/manualimport/suggestions`),
  folder: (kind: ImportKind, path?: string) =>
    req<ImportFolder>(`/api/v1/manualimport/folders?kind=${kind}${path ? `&path=${encodeURIComponent(path)}` : ""}`),
  search: (kind: ImportKind, query: string) =>
    req<{ results: ImportSearchHit[]; truncated?: boolean }>(`/api/v1/manualimport/search?kind=${kind}&q=${encodeURIComponent(query)}`),
};

// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { Movie, MovieOtherFiles } from "../../lib/api";
import { OtherFilesChip, otherFilesApi, otherFilesLabel } from "./OtherFiles";

// The film page's "+N other files" chip: shown only when the last look found some, and
// opening a read-only list (looked at again) of the files in the folder Arrmada doesn't
// track. No action on a file.

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const movie = (over: Partial<Movie> = {}): Movie => ({
  id: 7, tmdb_id: 949, title: "Heat", year: 1995, monitored: true, quality_profile: "hd", min_availability: "released",
  has_file: true, versions: [], other_files: { count: 1, files: [] }, ...over,
});

const listed: MovieOtherFiles = {
  count: 1,
  files: [{ name: "Heat (1995) Extended.mkv", rel_path: "Cuts/Heat (1995) Extended.mkv", path: "/m/Heat (1995)/Cuts/Heat (1995) Extended.mkv", size_bytes: 8 * 1024 ** 3, resolution: "2160p" }],
};

describe("other files chip", () => {
  it("counts the files, and isn't there without any", () => {
    expect(otherFilesLabel(1)).toBe("+1 other file");
    expect(otherFilesLabel(3)).toBe("+3 other files");
    const { rerender } = render(<OtherFilesChip movie={movie()} />);
    expect(screen.getByRole("button", { name: "+1 other file" })).toBeTruthy();
    rerender(<OtherFilesChip movie={movie({ other_files: { count: 0, files: [] } })} />);
    expect(screen.queryByRole("button")).toBeNull();
    rerender(<OtherFilesChip movie={movie({ has_file: false })} />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("opens a read-only list: name, path, size and resolution", async () => {
    vi.spyOn(otherFilesApi, "list").mockResolvedValue(listed);
    render(<OtherFilesChip movie={movie()} />);
    fireEvent.click(screen.getByRole("button", { name: "+1 other file" }));
    const dialog = await screen.findByRole("dialog", { name: "Other files in this folder" });
    expect(await screen.findByText("Heat (1995) Extended.mkv")).toBeTruthy();
    expect(screen.getByText("Cuts/Heat (1995) Extended.mkv")).toBeTruthy();
    expect(screen.getByText("2160p")).toBeTruthy();
    expect(screen.getByText("8.00 GB")).toBeTruthy();
    expect(screen.getByText(/in the film's folder, but Arrmada doesn't track them/)).toBeTruthy();
    // The only button in the list is Close.
    expect(Array.from(dialog.querySelectorAll("button")).map((b) => b.textContent?.trim()).filter((t) => t !== "Close" && t !== "×" && t !== "")).toEqual([]);
  });

  it("says when the folder isn't only the film's", async () => {
    vi.spyOn(otherFilesApi, "list").mockResolvedValue({ ...listed, shared: true });
    render(<OtherFilesChip movie={movie()} />);
    fireEvent.click(screen.getByRole("button", { name: "+1 other file" }));
    expect(await screen.findByText(/isn't only this film's/)).toBeTruthy();
  });
});

// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { Movie, MovieOtherFiles } from "../../lib/api";
import { qualityApi } from "../../lib/qualityApi";
import { OtherFilesChip, otherFilesApi, otherFilesLabel } from "./OtherFiles";

// The film page's "+N other files" chip: shown only when the last look found some, and
// opening a list (looked at again) whose one action records a file as a version.

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
    const { rerender } = render(<OtherFilesChip movie={movie()} onChange={() => {}} />);
    expect(screen.getByRole("button", { name: "+1 other file" })).toBeTruthy();
    rerender(<OtherFilesChip movie={movie({ other_files: { count: 0, files: [] } })} onChange={() => {}} />);
    expect(screen.queryByRole("button")).toBeNull();
    rerender(<OtherFilesChip movie={movie({ has_file: false })} onChange={() => {}} />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("opens the list and records a file as a new version, where it is", async () => {
    vi.spyOn(otherFilesApi, "list").mockResolvedValue(listed);
    vi.spyOn(qualityApi, "qualityProfiles").mockResolvedValue({ profiles: [{ key: "uhd", name: "4K", is_default: true }] } as unknown as Awaited<ReturnType<typeof qualityApi.qualityProfiles>>);
    const add = vi.spyOn(otherFilesApi, "addAsVersion").mockResolvedValue({ id: 3, is_default: false, label: "2160p", quality_profile: "uhd", monitored: false, has_file: true });
    const changed = vi.fn();
    render(<OtherFilesChip movie={movie()} onChange={changed} />);
    fireEvent.click(screen.getByRole("button", { name: "+1 other file" }));
    expect(await screen.findByRole("dialog", { name: "Other files in this folder" })).toBeTruthy();
    expect(await screen.findByText("Heat (1995) Extended.mkv")).toBeTruthy();
    expect(screen.getByText("Cuts/Heat (1995) Extended.mkv")).toBeTruthy();
    expect(screen.getByText("2160p")).toBeTruthy();
    expect(screen.getByText("8.00 GB")).toBeTruthy();

    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Add as a version" })); });
    expect(screen.getByText(/Nothing is moved, renamed or copied/)).toBeTruthy();
    expect((screen.getByLabelText("Label") as HTMLInputElement).value).toBe("2160p");
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Add as a version" })); });
    expect(add).toHaveBeenCalledWith(7, { path: listed.files[0].path, label: "2160p", quality_profile: "uhd" });
    expect(changed).toHaveBeenCalled();
  });

  it("can fill an existing version that has no file yet", async () => {
    vi.spyOn(otherFilesApi, "list").mockResolvedValue(listed);
    vi.spyOn(qualityApi, "qualityProfiles").mockResolvedValue({ profiles: [] } as unknown as Awaited<ReturnType<typeof qualityApi.qualityProfiles>>);
    const add = vi.spyOn(otherFilesApi, "addAsVersion").mockResolvedValue({ id: 5, is_default: false, label: "Extended", quality_profile: "hd", monitored: true, has_file: true });
    const versions = [
      { id: 0, is_default: true, label: "Default", quality_profile: "hd", monitored: true, has_file: true },
      { id: 4, is_default: false, label: "4K", quality_profile: "uhd", monitored: true, has_file: true },
      { id: 5, is_default: false, label: "Extended", quality_profile: "hd", monitored: true, has_file: false },
    ];
    render(<OtherFilesChip movie={movie({ versions })} onChange={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "+1 other file" }));
    await screen.findByText("Heat (1995) Extended.mkv");
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Add as a version" })); });
    const pick = screen.getByLabelText("Version") as HTMLSelectElement;
    // Only the version with no file is offered: nothing is ever replaced.
    expect(Array.from(pick.options).map((o) => o.text)).toEqual(["A new version", "Extended (no file yet)"]);
    fireEvent.change(pick, { target: { value: "5" } });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Add as a version" })); });
    expect(add).toHaveBeenCalledWith(7, { path: listed.files[0].path, version_id: 5 });
  });

  it("says when the folder isn't only the film's", async () => {
    vi.spyOn(otherFilesApi, "list").mockResolvedValue({ ...listed, shared: true });
    render(<OtherFilesChip movie={movie()} onChange={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "+1 other file" }));
    expect(await screen.findByText(/isn't only this film's/)).toBeTruthy();
  });
});

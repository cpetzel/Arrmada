// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { seedingApi, type SeedCheckRow } from "../../lib/seedingApi";
import { SeedingSection } from "./SeedingSettings";

// The global seed goal sits on Settings → Downloads with a read-only check of what's
// seeding: the check runs with the values in the fields and offers nothing to apply.

const commit = vi.fn(async () => ({}));
vi.mock("../../lib/useSettings", () => ({
  useLoadedSettings: () => ({ s: { downloads_seed_ratio: 2, downloads_seed_hours: 0 }, commit }),
}));

const rows: SeedCheckRow[] = [
  { hash: "a", name: "Show.S01.1080p", indexer: "TL", ratio: 2.4, ratio_known: true, seeding_seconds: 3600, goal: { enabled: true, ratio: 2, hours: 0, source: "global" }, outcome: "remove", reason: "seed goal met: ratio 2.40 reached the global goal of 2" },
  { hash: "b", name: "Film.2020.2160p", indexer: "TL", ratio: 0.3, ratio_known: true, seeding_seconds: 7200, goal: { enabled: true, ratio: 5, hours: 0, source: "indexer" }, outcome: "seeding" },
  { hash: "c", name: "Something.Added.By.Hand", ratio: 9, ratio_known: true, seeding_seconds: 99999, goal: { enabled: false, ratio: 0, hours: 0, source: "none" }, outcome: "not_ours" },
];

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("SeedingSection", () => {
  it("shows the saved global goal and checks what's seeding against it, read-only", async () => {
    const check = vi.spyOn(seedingApi, "check").mockResolvedValue(rows);
    render(<MemoryRouter><SeedingSection /></MemoryRouter>);
    expect((screen.getByLabelText("Seed ratio") as HTMLInputElement).value).toBe("2");
    expect((screen.getByLabelText("Seeding time (hours)") as HTMLInputElement).value).toBe("0");

    fireEvent.click(screen.getByRole("button", { name: "Check finished torrents" }));
    await waitFor(() => expect(screen.getAllByTestId("seed-check-row")).toHaveLength(3));
    expect(check).toHaveBeenCalledWith({ ratio: 2, hours: 0 });
    expect(screen.getByText(/3 finished torrents · 1 would be removed/)).toBeTruthy();
    expect(screen.getByText(/Global default · goal ratio 2/)).toBeTruthy();
    expect(screen.getByText(/Indexer \(TL\) · goal ratio 5/)).toBeTruthy();
    expect(screen.getByText(/Not grabbed by Arrmada — never touched/)).toBeTruthy();
    // Nothing on the check acts on a torrent.
    expect(screen.queryByRole("button", { name: /apply|remove/i })).toBeNull();
    expect(commit).not.toHaveBeenCalled();
  });

  it("keeps a ratio that isn't a number from being saved, and checks with the saved goal meanwhile", async () => {
    const check = vi.spyOn(seedingApi, "check").mockResolvedValue([]);
    render(<MemoryRouter><SeedingSection /></MemoryRouter>);
    const ratio = screen.getByLabelText("Seed ratio") as HTMLInputElement;
    // The field drops a typed minus sign, so a negative can't even be entered.
    fireEvent.change(ratio, { target: { value: "-3" } });
    expect(ratio.value).toBe("3");
    fireEvent.change(ratio, { target: { value: "1.2.3" } });
    fireEvent.blur(ratio);
    await waitFor(() => expect(screen.getByText(/must be a number/)).toBeTruthy());
    expect(commit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Check finished torrents" }));
    await waitFor(() => expect(check).toHaveBeenCalledWith(undefined));
    expect(await screen.findByText("No finished torrents in the client.")).toBeTruthy();
  });
});

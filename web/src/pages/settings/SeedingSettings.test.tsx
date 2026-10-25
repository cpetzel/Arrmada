// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { seedingApi, type SeedCheckRow } from "../../lib/seedingApi";
import { SeedingSection } from "./SeedingSettings";

// The global seed goal sits on Settings → Downloads with a read-only check of what's
// seeding. The fields are drafts: nothing is saved until Save, so the check previews a goal
// before it can remove anything, and with an invalid field the check won't run at all.

const commit = vi.fn(async (p: Record<string, unknown>) => p);
vi.mock("../../lib/useSettings", () => ({
  useLoadedSettings: () => ({ s: { downloads_seed_ratio: 0, downloads_seed_hours: 0 }, commit }),
}));

const rows: SeedCheckRow[] = [
  { hash: "a", name: "Show.S01.1080p", indexer: "TL", ratio: 2.4, ratio_known: true, seeding_seconds: 3600, goal: { enabled: true, ratio: 2, hours: 0, source: "global" }, outcome: "remove", reason: "seed goal met: ratio 2.40 reached the global goal of 2" },
  { hash: "b", name: "Film.2020.2160p", indexer: "TL", ratio: 0.3, ratio_known: true, seeding_seconds: 7200, goal: { enabled: true, ratio: 5, hours: 0, source: "indexer" }, outcome: "seeding" },
  { hash: "c", name: "Something.Added.By.Hand", ratio: 9, ratio_known: true, seeding_seconds: 99999, goal: { enabled: false, ratio: 0, hours: 0, source: "none" }, outcome: "not_ours" },
];

afterEach(() => {
  cleanup();
  commit.mockClear();
  vi.restoreAllMocks();
});

// clickAway is what a real click on a button does to a focused field: the mouse goes down
// on the button, focus (and blur) moves to it, then the click lands.
function clickAway(field: HTMLElement, button: HTMLElement) {
  fireEvent.pointerDown(button);
  fireEvent.mouseDown(button);
  act(() => { button.focus(); });
  fireEvent.blur(field);
  fireEvent.focusOut(field);
  fireEvent.pointerUp(button);
  fireEvent.mouseUp(button);
  fireEvent.click(button);
}

describe("SeedingSection", () => {
  it("previews a typed goal without saving it, even though leaving the field is part of the click", async () => {
    const check = vi.spyOn(seedingApi, "check").mockResolvedValue(rows);
    render(<MemoryRouter><SeedingSection /></MemoryRouter>);
    const ratio = screen.getByLabelText("Seed ratio") as HTMLInputElement;
    act(() => { ratio.focus(); });
    fireEvent.change(ratio, { target: { value: "2" } });
    clickAway(ratio, screen.getByRole("button", { name: "Check finished torrents" }));

    await waitFor(() => expect(screen.getAllByTestId("seed-check-row")).toHaveLength(3));
    expect(check).toHaveBeenCalledWith({ ratio: 2, hours: 0 });
    expect(commit).not.toHaveBeenCalled();
    expect(screen.getByText(/1 would be removed on the next check — with a global goal of ratio 2 \(not saved\)/)).toBeTruthy();
    expect(screen.getByText(/Not saved yet/)).toBeTruthy();
    expect(screen.getByText(/Global default \(live\) · goal ratio 2/)).toBeTruthy();
    expect(screen.getByText(/TL's goal \(recorded when grabbed\) · goal ratio 5/)).toBeTruthy();
    expect(screen.getByText(/Not grabbed by Arrmada — never touched/)).toBeTruthy();
    // Nothing on the check acts on a torrent.
    expect(screen.queryByRole("button", { name: /apply|remove/i })).toBeNull();

    // Only Save saves, both values together.
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(commit).toHaveBeenCalledWith({ downloads_seed_ratio: 2, downloads_seed_hours: 0 }));
  });

  it("won't check or save a goal that isn't valid, and says why", async () => {
    const check = vi.spyOn(seedingApi, "check").mockResolvedValue([]);
    render(<MemoryRouter><SeedingSection /></MemoryRouter>);
    const ratio = screen.getByLabelText("Seed ratio") as HTMLInputElement;
    // The field drops a typed minus sign, so a negative can't even be entered.
    fireEvent.change(ratio, { target: { value: "-3" } });
    expect(ratio.value).toBe("3");
    fireEvent.change(ratio, { target: { value: "1.2.3" } });
    const button = screen.getByRole("button", { name: "Check finished torrents" }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    expect(screen.getByText(/Fix the goal above to check it: .*must be a number/)).toBeTruthy();
    clickAway(ratio, button);
    expect((screen.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
    expect(check).not.toHaveBeenCalled();
    expect(commit).not.toHaveBeenCalled();
  });
});

// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { SeedingRules, type SeedMode } from "./IndexerEditor";

// The indexer form's seed rules offer three choices: the global default (naming it), a goal
// of its own, or seed forever. A new indexer isn't offered "forever".

vi.mock("../../lib/useSettings", () => ({
  useOptionalSettings: () => ({ downloads_seed_ratio: 2, downloads_seed_hours: 336 }),
}));

afterEach(cleanup);

function Rules({ start, creating }: { start: SeedMode; creating?: boolean }) {
  const [mode, setMode] = useState<SeedMode>(start);
  return <SeedingRules enabled mode={mode} ratio={0} time={0} unit="days" creating={creating} onEnabled={() => {}} onMode={setMode} onRatio={() => {}} onTime={() => {}} onUnit={() => {}} />;
}

describe("SeedingRules", () => {
  it("offers the global default, an own goal or seed forever, and names the global goal", () => {
    render(<Rules start="forever" />);
    expect(screen.getByTestId("seed-mode-note").textContent).toMatch(/Seeds until you remove it/);
    fireEvent.click(screen.getByRole("button", { name: "Global default" }));
    expect(screen.getByTestId("seed-mode-note").textContent).toMatch(/Uses the global default \(ratio 2 or 14 days\)/);
    expect(screen.queryByLabelText("Seed ratio (0 = none)")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Own goal" }));
    expect(screen.getByLabelText("Seed ratio (0 = none)")).toBeTruthy();
    expect(screen.getByText(/a ratio can end the seed before the time/)).toBeTruthy();
  });

  it("doesn't offer seed forever on a new indexer", () => {
    render(<Rules start="global" creating />);
    expect(screen.getByRole("button", { name: "Global default" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Seed forever" })).toBeNull();
  });
});

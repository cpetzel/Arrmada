import { describe, expect, it } from "vitest";
import { downloadPercent } from "./percent";

describe("downloadPercent", () => {
  it("never says 100% before the download is complete (#73)", () => {
    expect(downloadPercent(0.996)).toBe("99.6%");
    expect(downloadPercent(0.9999)).toBe("99.9%");
    expect(downloadPercent(1 - 1e-12)).toBe("99.9%");
    expect(downloadPercent(1)).toBe("100%");
    expect(downloadPercent(1.2)).toBe("100%");
  });
  it("shows one decimal in between, rounded down", () => {
    expect(downloadPercent(0.42)).toBe("42.0%");
    expect(downloadPercent(0.4237)).toBe("42.3%");
    expect(downloadPercent(0.58)).toBe("58.0%");
    expect(downloadPercent(0.0004)).toBe("0.0%");
  });
  it("is a whole 0% for nothing yet or nothing known", () => {
    expect(downloadPercent(0)).toBe("0%");
    expect(downloadPercent(-0.1)).toBe("0%");
    expect(downloadPercent(undefined)).toBe("0%");
    expect(downloadPercent(NaN)).toBe("0%");
  });
});

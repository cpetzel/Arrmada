// downloadPercent is a download's progress (0..1) as text: "0%", "42.3%", "99.6%", "100%".
// Rounding to a whole number read 99.6% as "100%" while the client was still fetching
// the last pieces (#73), so below complete it keeps one decimal and rounds down: it
// never says 100% until it's done. Kept out of lib/format so it doesn't add to the
// requester pages' first load.
export function downloadPercent(progress: number | null | undefined): string {
  const p = progress ?? 0;
  if (!Number.isFinite(p) || p <= 0) return "0%";
  if (p >= 1) return "100%";
  // In tenths of a percent; the nudge keeps 0.58 (579.999… in floating point) at 58.0.
  const tenths = Math.min(999, Math.floor(p * 1000 + 1e-6));
  return `${(tenths / 10).toFixed(1)}%`;
}

// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueuePanel } from "./Downloads";
import { api, type ActivityFeed } from "../lib/api";
import { downloadClientApi } from "../lib/downloadClientApi";
import { clearQueryCache } from "../lib/query";
import { resetLiveForTests } from "../lib/live";
import { ToastProvider } from "../ui";

// FE-13: a failed action on a download says so. act() used to leave it to the next poll,
// which only showed that nothing had changed.

const feed: ActivityFeed = {
  downloads: [{
    hash: "aaaa", name: "Driftwood.2025.2160p.WEB-DL", state: "downloading", progress: 0.42, size_bytes: 1e9,
    down_speed: 1e6, up_speed: 0, eta_seconds: 900, ratio: 0, quality_profile: "uhd", media_type: "movie",
  }],
  totals: { down_speed: 1e6, up_speed: 0, active: 1 },
  free_gb: 100,
  disk_path: "/downloads",
  clients: { configured: 1, enabled: 1, ok: true },
};

beforeEach(() => {
  clearQueryCache();
  resetLiveForTests();
  vi.stubGlobal("WebSocket", class { close() {} });
  vi.spyOn(downloadClientApi, "downloadClients").mockResolvedValue([]);
  vi.spyOn(api, "activity").mockResolvedValue(feed);
  vi.spyOn(api, "wanted").mockResolvedValue({ searching: [], upcoming: [] } as unknown as Awaited<ReturnType<typeof api.wanted>>);
});
afterEach(() => {
  cleanup();
  resetLiveForTests();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("Queue actions", () => {
  it("shows an error toast when pausing fails", async () => {
    vi.spyOn(api, "pauseDownload").mockRejectedValue(new Error("qBittorrent isn't answering"));
    render(<ToastProvider><MemoryRouter><QueuePanel /></MemoryRouter></ToastProvider>);
    const pause = await screen.findByRole("button", { name: "Pause" });
    await act(async () => { fireEvent.click(pause); });
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("qBittorrent isn't answering");
  });
});

// #71: one qBittorrent registered as two download clients (an owner's own row beside the
// bundled one, or a movies and a TV card on the same client) lists every torrent twice,
// once per client, under the same hash. Keyed by hash alone, React matched the wrong
// cards: filtering left other titles on screen, and each clear-and-retype of the search
// added another copy until a reload.
describe("Queue search with a torrent in two clients", () => {
  const row = (hash: string, name: string, title: string, client: number) => ({
    hash, name, state: "downloading", progress: 0.5, size_bytes: 1e9, down_speed: 1e6, up_speed: 0, eta_seconds: 900, ratio: 0,
    quality_profile: "uhd", media_type: "movie", client_id: client, client_name: `qBittorrent ${client}`,
    managed: true, item: { kind: "movie" as const, id: hash.charCodeAt(0), title },
  });
  const torrents = [
    ["aaaa", "Slow.Horses.S05.1080p.WEB-DL", "Slow Horses"],
    ["bbbb", "Brothers.2024.1080p.WEB-DL", "Brothers"],
    ["cccc", "Brothers.2024.2160p.WEB-DL", "Brothers"],
  ] as const;
  const twice: ActivityFeed = {
    ...feed,
    downloads: [1, 2].flatMap((c) => torrents.map(([h, n, t]) => row(h, n, t, c))),
    clients: { configured: 2, enabled: 2, ok: true },
  };
  const releases = () => screen.queryAllByTestId("transfer-release").map((e) => e.textContent);

  it("shows only the matching rows, and the same rows after clearing and typing again", async () => {
    vi.spyOn(api, "activity").mockResolvedValue(twice);
    render(<ToastProvider><MemoryRouter><QueuePanel /></MemoryRouter></ToastProvider>);
    await screen.findAllByTestId("transfer-card");
    expect(releases()).toHaveLength(6);
    const box = screen.getByRole("textbox", { name: "Search this list" });
    for (let i = 0; i < 3; i++) {
      fireEvent.change(box, { target: { value: "Slow.Horses" } });
      expect(releases()).toEqual(["Slow.Horses.S05.1080p.WEB-DL", "Slow.Horses.S05.1080p.WEB-DL"]);
      fireEvent.change(box, { target: { value: "" } });
      expect(releases()).toHaveLength(6);
    }
    fireEvent.change(box, { target: { value: "brothers" } });
    expect(releases().sort()).toEqual(["Brothers.2024.1080p.WEB-DL", "Brothers.2024.1080p.WEB-DL", "Brothers.2024.2160p.WEB-DL", "Brothers.2024.2160p.WEB-DL"]);
  });
});

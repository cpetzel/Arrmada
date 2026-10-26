import type { Page } from "@playwright/test";
import { test, expect } from "./mockApi";
import { movies as moviesFixture } from "./fixtures/media";
import type { Movie, MovieFile, MovieOtherFiles } from "../src/lib/api";

// Other files in a film's folder: the film's page shows "+1 other file" by its file, the
// chip opens the list (looked at again), and "Add as a version" records the file where it
// is. The Movies library has a "Has other files" filter and a quiet poster badge. On a
// phone too, with nothing wider than the screen.

const ID = 88;

const main: MovieFile = {
  path: "/media/movies/The Fellowship (2001)/The Fellowship (2001) - 1080p BluRay.mkv",
  filename: "The Fellowship (2001) - 1080p BluRay.mkv", size_bytes: 12e9, quality: "1080p BluRay", codec: "x264", probed: true, missing: false,
};

const listed: MovieOtherFiles = {
  count: 1,
  folder: "/media/movies/The Fellowship (2001)",
  files: [{
    name: "The Fellowship (2001) Extended Edition 2160p.mkv",
    rel_path: "Cuts/The Fellowship (2001) Extended Edition 2160p.mkv",
    path: "/media/movies/The Fellowship (2001)/Cuts/The Fellowship (2001) Extended Edition 2160p.mkv",
    size_bytes: 61e9, resolution: "2160p",
  }],
};

const movie: Movie = {
  id: ID, tmdb_id: 120, title: "The Fellowship", year: 2001, monitored: true, quality_profile: "hd-1080p",
  min_availability: "released", has_file: true, movie_file_path: main.path, file: main,
  versions: [{ id: 0, is_default: true, label: "Default", quality_profile: "hd-1080p", monitored: true, has_file: true, file_path: main.path, file: main }],
  other_files: { count: 1, files: listed.files },
};

interface Seen { method: string; path: string; body: unknown }

async function serve(page: Page): Promise<Seen[]> {
  const seen: Seen[] = [];
  const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) });
  await page.route((u) => u.pathname.startsWith(`/api/v1/movies/${ID}`) || u.pathname === "/api/v1/recycle/mode", (route) => {
    const req = route.request();
    const url = new URL(req.url());
    let body: unknown;
    try { body = req.postDataJSON(); } catch { body = undefined; }
    seen.push({ method: req.method(), path: url.pathname, body });
    const p = url.pathname.replace(`/api/v1/movies/${ID}`, "");
    if (url.pathname === "/api/v1/recycle/mode") return route.fulfill(json({ enabled: true, dirs: [], retention_days: 30, max_gb: 0 }));
    if (p === "" && req.method() === "GET") return route.fulfill(json(movie));
    if (p === "/other-files") return route.fulfill(json(listed));
    if (p === "/other-files/version") return route.fulfill(json({ id: 9, is_default: false, label: "2160p", quality_profile: "hd-1080p", monitored: false, has_file: true, file_path: listed.files[0].path }, 201));
    if (p === "/history") return route.fulfill(json({ events: [] }));
    if (p === "/blocklist") return route.fulfill(json({ blocklist: [] }));
    if (p === "/watch-stats") return route.fulfill(json({ plays: 0, watchers: [] }));
    if (p === "/collection") return route.fulfill(json({ name: "", members: [] }));
    return route.fulfill(json({ message: `e2e movie-other-files: no answer for ${req.method()} ${url.pathname}` }, 501));
  });
  return seen;
}

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("the chip opens the list, and Add as a version records the file where it is", async ({ page, api }) => {
    const seen = await serve(page);
    await page.goto(`/movies/${ID}`);
    await api.quiet();

    // Keyboard: the chip is a button; Enter opens the list, Escape closes it and gives
    // focus back.
    const chip = page.getByRole("button", { name: "+1 other file" });
    await expect(chip).toBeVisible();
    await chip.focus();
    await page.keyboard.press("Enter");
    const dialog = page.getByRole("dialog", { name: "Other files in this folder" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText("The Fellowship (2001) Extended Edition 2160p.mkv", { exact: true })).toBeVisible();
    await expect(dialog.getByText("Cuts/The Fellowship (2001) Extended Edition 2160p.mkv")).toBeVisible();
    await expect(dialog.getByText("2160p", { exact: true })).toBeVisible();
    await expect(dialog.getByText("56.81 GB")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(chip).toBeFocused();

    // The page is no wider than the screen, with the list open too.
    await chip.click();
    await expect(dialog).toBeVisible();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);

    await dialog.getByRole("button", { name: "Add as a version" }).click();
    await expect(dialog.getByText(/Nothing is moved, renamed or copied/)).toBeVisible();
    await expect(dialog.getByLabel("Label")).toHaveValue("2160p");
    await expect(dialog.getByLabel("Quality profile")).toHaveValue("hd-1080p");
    await dialog.getByRole("button", { name: "Add as a version" }).click();
    await expect.poll(() => seen.filter((s) => s.method === "POST" && s.path.endsWith("/other-files/version")).length).toBe(1);
    expect(seen.find((s) => s.method === "POST")?.body).toEqual({ path: listed.files[0].path, label: "2160p", quality_profile: "hd-1080p" });
    // Nothing else on the film was asked to change.
    expect(seen.filter((s) => s.method !== "GET" && !s.path.endsWith("/other-files/version"))).toEqual([]);
  });

  test("the Movies library filters to films with other files, and badges their posters", async ({ page }) => {
    // The filter pills are the desktop toolbar's; a phone has the same filters in a menu.
    test.skip(test.info().project.name === "phone", "desktop filter pills");
    await page.route((u) => u.pathname === "/api/v1/movies", (route) => route.fulfill({
      status: 200, contentType: "application/json",
      body: JSON.stringify({ ...moviesFixture, movies: moviesFixture.movies.map((m) => (m.id === 4 ? { ...m, other_files: 2 } : m)) }),
    }));
    await page.goto("/movies");
    await expect(page.getByRole("img", { name: "2 other video files in its folder" })).toBeVisible();
    await page.getByRole("button", { name: /^Has other files 1$/ }).click();
    await expect(page.getByText("Lanterns Over the Northern Sea").first()).toBeVisible();
    await expect(page.getByText("Anchor Point")).toHaveCount(0);
  });
});

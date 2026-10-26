import type { Page, Route } from "@playwright/test";
import { test, expect } from "./mockApi";
import { mockLibrary } from "./fixtures/booksLibrary";

// Manual import's folder picker: the finished downloads that match the title one click
// away, a folder browser over the import roots (breadcrumbs, counts, a filter), a name
// search over the downloads folders, and a typed path. Whatever is picked is what the
// file list then shows, through the same listing the import uses.

const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });

const ROOTS = { path: "", crumbs: [], files: [], dirs: [
  { name: "downloads", path: "/downloads", label: "Downloads" },
  { name: "ebooks", path: "/library/ebooks", label: "Ebooks" },
] };
const DOWNLOADS = {
  path: "/downloads",
  crumbs: [{ name: "Downloads", path: "/downloads" }],
  dirs: [
    { name: "Frank.Herbert.Children.of.Dune.EPUB", path: "/downloads/Frank.Herbert.Children.of.Dune.EPUB", media: 1 },
    { name: "Some.Other.Book", path: "/downloads/Some.Other.Book", media: 3 },
    { name: "empty", path: "/downloads/empty", media: 0 },
  ],
  files: [{ name: "loose.epub", path: "/downloads/loose.epub", size_bytes: 1_500_000 }],
};
const INSIDE = {
  path: "/downloads/Frank.Herbert.Children.of.Dune.EPUB",
  crumbs: [{ name: "Downloads", path: "/downloads" }, { name: "Frank.Herbert.Children.of.Dune.EPUB", path: "/downloads/Frank.Herbert.Children.of.Dune.EPUB" }],
  dirs: [],
  files: [{ name: "Children of Dune.epub", path: "/downloads/Frank.Herbert.Children.of.Dune.EPUB/Children of Dune.epub", size_bytes: 900_000 }],
};

// serve answers the picker's calls and records which folder each listing asked for.
async function serve(page: Page): Promise<string[]> {
  const listed: string[] = [];
  await page.route((u) => u.pathname === "/api/v1/books/13/manualimport/suggestions", (route) => json(route, { suggestions: [
    { name: "Frank.Herbert.Children.of.Dune.EPUB", path: "/downloads/Frank.Herbert.Children.of.Dune.EPUB", size_bytes: 900_000, in_client: true, not_managed: true },
  ] }));
  await page.route((u) => u.pathname === "/api/v1/books/13/manualimport", (route) => {
    const p = new URL(route.request().url()).searchParams.get("path") ?? "";
    listed.push(p);
    if (p === "/elsewhere") return json(route, { message: "Pick a folder inside your downloads or library folders (Settings → Library)" }, 400);
    const file = p ? `${p.replace(/\.epub$/, "")}/x.epub` : "/downloads/x.epub";
    return json(route, { path: p || "/downloads", candidates: [{ path: file, filename: file.split("/").pop(), edition: "ebook", format: "EPUB", size_bytes: 900_000 }] });
  });
  await page.route((u) => u.pathname === "/api/v1/manualimport/folders", (route) => {
    const p = new URL(route.request().url()).searchParams.get("path") ?? "";
    return json(route, p === "" ? ROOTS : p === "/downloads" ? DOWNLOADS : INSIDE);
  });
  await page.route((u) => u.pathname === "/api/v1/manualimport/search", (route) => json(route, { results: [
    { name: "Children.of.Dune.Audiobook", path: "/downloads/old/Children.of.Dune.Audiobook", dir: true, where: "Downloads", rel: "old/Children.of.Dune.Audiobook" },
  ] }));
  return listed;
}

async function openImport(page: Page) {
  await mockLibrary(page);
  const listed = await serve(page);
  await page.goto("/books/13");
  await page.getByRole("button", { name: "Add files", exact: true }).click();
  await page.getByRole("menuitem", { name: "Manual import…" }).click();
  const dialog = page.getByRole("dialog", { name: "Manual import" });
  await expect(dialog).toBeVisible();
  return { dialog, listed };
}

test.describe("manual import picker", () => {
  test.use({ persona: "manager" });

  test("a suggestion, the browser, the search and a typed path each choose where to look", async ({ page }) => {
    const { dialog, listed } = await openImport(page);
    const source = dialog.getByTestId("import-source");
    await expect(source).toHaveText("the downloads folder");
    await expect.poll(() => listed).toEqual([""]);

    // The suggested download, one click.
    await expect(dialog.getByText("Not managed")).toBeVisible();
    await dialog.getByRole("button", { name: "Use Frank.Herbert.Children.of.Dune.EPUB" }).click();
    await expect(source).toHaveText("/downloads/Frank.Herbert.Children.of.Dune.EPUB");
    await expect.poll(() => listed[listed.length - 1]).toBe("/downloads/Frank.Herbert.Children.of.Dune.EPUB");
    await expect(dialog.getByRole("button", { name: "Using Frank.Herbert.Children.of.Dune.EPUB" })).toBeDisabled();

    // The browser: the roots, then into Downloads by keyboard, with counts and a filter.
    await dialog.getByRole("button", { name: "Downloads folder", exact: true }).click();
    await expect(source).toHaveText("the downloads folder");
    const toggle = dialog.getByRole("button", { name: "Choose a folder…" });
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(dialog.getByRole("tab", { name: "Browse" })).toHaveAttribute("aria-selected", "true");
    const openDownloads = dialog.getByRole("button", { name: "Open Downloads" });
    await openDownloads.focus();
    await page.keyboard.press("Enter");
    await expect(dialog.getByRole("navigation", { name: "Folder path" }).getByRole("button", { name: "Downloads" })).toHaveAttribute("aria-current", "page");
    await expect(dialog.getByText("3 book files")).toBeVisible();
    await expect(dialog.getByText("0 book files")).toBeVisible();
    // An empty folder has nothing to use.
    await expect(dialog.getByRole("button", { name: "Use empty" })).toHaveCount(0);
    await dialog.getByLabel("Filter this folder").fill("dune");
    await expect(dialog.getByRole("button", { name: "Open Some.Other.Book" })).toHaveCount(0);
    await dialog.getByRole("button", { name: "Open Frank.Herbert.Children.of.Dune.EPUB" }).click();
    await expect(dialog.getByText("Children of Dune.epub")).toBeVisible();
    // The breadcrumb walks back up.
    await dialog.getByRole("navigation", { name: "Folder path" }).getByRole("button", { name: "Downloads" }).click();
    await expect(dialog.getByText("loose.epub")).toBeVisible();
    await dialog.getByRole("button", { name: "Use this folder" }).click();
    await expect(source).toHaveText("/downloads");
    await expect.poll(() => listed[listed.length - 1]).toBe("/downloads");
    await expect(dialog.getByRole("tablist")).toHaveCount(0); // the picker closes on a pick

    // The search across the downloads folders.
    await dialog.getByRole("button", { name: "Choose a folder…" }).click();
    await dialog.getByRole("tab", { name: "Search downloads" }).click();
    await dialog.getByLabel("Search the downloads folders").fill("children dune");
    await page.keyboard.press("Enter");
    await expect(dialog.getByText("Downloads / old/Children.of.Dune.Audiobook")).toBeVisible();
    await dialog.getByRole("button", { name: "Use Children.of.Dune.Audiobook" }).click();
    await expect.poll(() => listed[listed.length - 1]).toBe("/downloads/old/Children.of.Dune.Audiobook");

    // A typed path outside the roots: the server's refusal shows, nothing is listed.
    await dialog.getByRole("button", { name: "Choose a folder…" }).click();
    await dialog.getByRole("tab", { name: "Type a path" }).click();
    await dialog.getByLabel("Folder or file path").fill("/elsewhere");
    await dialog.getByRole("button", { name: "Use this path" }).click();
    await expect(dialog.getByText("Pick a folder inside your downloads or library folders")).toBeVisible();
    await expect(dialog.getByRole("button", { name: /x\.epub/ })).toHaveCount(0);
  });

  test("the picker fits a phone without sideways scrolling", async ({ page, isMobile }) => {
    test.skip(!isMobile, "phone project only");
    const { dialog } = await openImport(page);
    await dialog.getByRole("button", { name: "Choose a folder…" }).click();
    await dialog.getByRole("button", { name: "Open Downloads" }).click();
    await expect(dialog.getByText("loose.epub")).toBeVisible();
    for (const tab of ["Browse", "Search downloads", "Type a path"]) {
      await dialog.getByRole("tab", { name: tab }).click();
      const over = await dialog.evaluate((el) => el.scrollWidth - el.clientWidth);
      expect(over, `${tab}: the dialog scrolls sideways`).toBeLessThanOrEqual(0);
      const doc = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(doc, `${tab}: the page scrolls sideways`).toBeLessThanOrEqual(0);
    }
  });
});

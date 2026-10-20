import type { Page } from "@playwright/test";
import { test, expect, type MockedApi } from "./mockApi";
import { settings } from "./fixtures/system";
import type { MockRoute } from "./fixtures/routes";

// GH #56: "Add the year only when it's needed" — a new show folder keeps {year} only when
// another show shares its title, like Sonarr's TheTVDB names ("Dark Matter (2024)" next to
// "Dark Matter"). The preview shows both outcomes once it's on.

// override answers the given endpoints before the default fixtures, recording the calls.
async function override(page: Page, api: MockedApi, extra: MockRoute[]) {
  await page.route((u) => u.pathname.startsWith("/api/"), async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    for (const r of extra) {
      if (r.method !== req.method() || url.pathname !== r.path) continue;
      let body: unknown;
      try { body = req.postDataJSON(); } catch { body = req.postData(); }
      api.calls.push({ method: req.method(), path: url.pathname + url.search, body });
      const out = r.respond ? r.respond({ url, params: [], body }) : r.body;
      return route.fulfill({ status: r.status ?? 200, contentType: "application/json", body: JSON.stringify(out ?? {}) });
    }
    return route.fallback();
  });
}

const withNaming = (extra: Record<string, unknown>) => ({ ...settings, naming_series_folder: "{title} ({year})", ...extra });
const getSettings = (extra: Record<string, unknown> = {}): MockRoute => ({ method: "GET", path: "/api/v1/settings", body: withNaming(extra) });
const putSettings: MockRoute = { method: "PUT", path: "/api/v1/settings", respond: ({ body }) => withNaming(body as Record<string, unknown>) };

test.describe("manager", () => {
  test.use({ persona: "manager" });

  test("it's off by default; turning it on saves it and previews both outcomes", async ({ page, api }) => {
    await override(page, api, [getSettings(), putSettings]);
    await page.goto("/settings/media#series-naming");
    const card = page.locator("#series-naming");
    const toggle = card.getByRole("switch", { name: "Add the year only when it’s needed" });
    await expect(toggle).not.toBeChecked();
    await expect(card.getByText("The Bear (2022)", { exact: true })).toBeVisible(); // today's single preview

    await toggle.click();
    await expect(toggle).toBeChecked();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([{ naming_series_year_when_needed: true }]);
    const examples = card.getByRole("group", { name: "Series folder examples" });
    await expect(examples.getByText("Breaking Bad", { exact: true })).toBeVisible();
    await expect(examples.getByText("The Office (2005)", { exact: true })).toBeVisible();
    await expect(examples.getByText("another show is called The Office")).toBeVisible();

    // The preview follows the template as it's typed, ids included.
    await card.getByRole("textbox", { name: "Series folder" }).fill("{title} ({year}) {tvdb-{tvdbid}}");
    await expect(examples.getByText("Breaking Bad {tvdb-81189}", { exact: true })).toBeVisible();
    await expect(examples.getByText("The Office (2005) {tvdb-73244}", { exact: true })).toBeVisible();
  });

  test("a template with no {year} says the setting changes nothing", async ({ page, api }) => {
    await override(page, api, [getSettings({ naming_series_folder: "{title}", naming_series_year_when_needed: true })]);
    await page.goto("/settings/media#series-naming");
    const card = page.locator("#series-naming");
    await expect(card.getByRole("switch", { name: "Add the year only when it’s needed" })).toBeChecked();
    await expect(card.getByText(/this template has no \{year\}/)).toBeVisible();
    await expect(card.getByRole("group", { name: "Series folder examples" })).toHaveCount(0);
  });

  test("turning it off goes back to the single preview", async ({ page, api }) => {
    await override(page, api, [getSettings({ naming_series_year_when_needed: true }), putSettings]);
    await page.goto("/settings/media#series-naming");
    const card = page.locator("#series-naming");
    await expect(card.getByRole("group", { name: "Series folder examples" })).toBeVisible();
    await card.getByRole("switch", { name: "Add the year only when it’s needed" }).click();
    await expect.poll(() => api.callsTo("PUT", "/api/v1/settings").map((c) => c.body)).toEqual([{ naming_series_year_when_needed: false }]);
    await expect(card.getByRole("group", { name: "Series folder examples" })).toHaveCount(0);
    await expect(card.getByText("The Bear (2022)", { exact: true })).toBeVisible();
  });
});

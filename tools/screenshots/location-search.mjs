import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

export async function verifyLocationSearch(browser) {
  const context = await browser.newContext({ viewport: { width: 900, height: 700 } });
  const page = await context.newPage();
  const [script, css] = await Promise.all([
    readFile(new URL("../../web/static/js/app.js", import.meta.url), "utf8"),
    readFile(new URL("../../web/static/css/app.css", import.meta.url), "utf8"),
  ]);
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  let calls = 0, writes = 0, gate, release, empty = false, failure = false, rejectFailure = false;
  const id = "11111111-1111-4111-8111-111111111111";
  const proposalID = "22222222-2222-4222-8222-222222222222";
  const result = { display_name: "Skyline Restaurant, Hamburg, Germany", name: "Skyline Restaurant", lat: 53.55, lng: 10 };
  await context.addCookies([{ name: "csrf_token", value: "location-token", url: "http://locations.test" }]);
  await page.route("**/*", async route => {
    const url = new URL(route.request().url());
    if (url.pathname === "/app.js") return route.fulfill({ contentType: "text/javascript", body: script });
    if (url.pathname === "/app.css") return route.fulfill({ contentType: "text/css", body: css });
    if (url.pathname === "/api/geocode") {
      calls++;
      assert.equal(url.searchParams.get("places"), "1");
      assert.equal(url.searchParams.get("category"), "Restaurant");
      assert.equal(url.searchParams.get("lat"), "55");
      const query = url.searchParams.get("q");
      if (gate && query === "First pending") await gate;
      return route.fulfill({ status: failure ? 503 : 200, contentType: "application/json",
        body: JSON.stringify({ results: empty ? [] : [{ ...result, display_name: query === "First pending" ? "Old result" : result.display_name }] }) });
    }
    if (url.pathname.endsWith("/location-suggestion/reject")) {
      writes++;
      assert.equal(url.pathname, `/items/${id}/location-suggestion/reject`);
      assert.equal(route.request().headers()["x-csrf-token"], "location-token");
      assert.equal(new URLSearchParams(route.request().postData()).get("suggestion_id"), proposalID);
      return route.fulfill({ status: rejectFailure ? 503 : 204, body: "" });
    }
    assert.equal(url.pathname, "/");
    const saved = url.searchParams.get("saved");
    const located = saved === "located";
    return route.fulfill({ contentType: "text/html", body: `<!doctype html><html lang="en"><head><link rel="stylesheet" href="/app.css"></head>
      <body data-location-loading="Searching for places" data-location-error="Search failed" data-location-empty="No place found">
      <form class="form" style="margin:30px;max-width:700px">
      <select name="category"><option>Restaurant</option></select>
      <input name="title" value="${saved ? "Restaurant Skylien Hamburg" : ""}">
      <div class="location-picker__field">
      <input name="location" maxlength="200" data-geo-lite data-geo-places data-geo-suggest-title
             data-geo-near-lat="55" data-geo-near-lng="9" value="${located ? "Existing place" : ""}">
      <div class="suggest" data-geo-lite-list hidden></div>
      <p role="status" data-geo-lite-status data-error="Search failed" data-empty="No place found" hidden></p>
      <div class="location-proposal" data-location-proposal hidden data-item-id="${id}"
           data-accepted="Save the form to keep it" data-rejected="Rejected without changing your entry"
           data-reject-error="Rejection failed" ${saved && !located ? `data-label="${result.display_name}" data-lat="53.55" data-lng="10"
           data-suggestion-id="${proposalID}" data-rejected-saved="${saved === "rejected"}"` : ""}>
        <label>Suggested place<input readonly data-location-preview tabindex="-1"></label>
        <button type="button" data-location-accept aria-label="Use place">✓</button>
        <button type="button" data-location-reject aria-label="Reject place">✕</button>
      </div></div>
      <input type="hidden" name="latitude" data-geo-lite-lat value="${located ? "53" : ""}">
      <input type="hidden" name="longitude" data-geo-lite-lng value="${located ? "10" : ""}">
      </form><script src="/app.js"></script></body></html>` });
  });
  const location = page.locator('[name="location"]');
  const latitude = page.locator('[name="latitude"]');
  const title = page.locator('[name="title"]');
  const proposal = page.locator("[data-location-proposal]");
  const spinner = page.locator(".location-search__spinner");
  const status = page.locator("[data-geo-lite-status]");
  try {
    await page.goto("http://locations.test");
    await title.fill("Restaurant Skylien Hamburg");
    await proposal.waitFor({ state: "visible" });
    assert.equal(await location.inputValue(), "");
    assert.equal(await latitude.inputValue(), "");
    assert.equal(await title.inputValue(), "Restaurant Skylien Hamburg");
    assert.equal(writes, 0, "Looking up a place must never save it");
    await page.locator("[data-location-reject]").click();
    assert.equal(await location.inputValue(), "");
    assert.equal(await latitude.inputValue(), "");
    assert.equal(await status.getAttribute("class"), "is-location-rejected");
    gate = new Promise(resolve => { release = resolve; });
    await location.fill("First pending");
    await spinner.waitFor({ state: "visible" });
    assert.equal(await location.getAttribute("aria-busy"), "true");
    assert.equal(await spinner.evaluate(el => getComputedStyle(el).borderTopWidth), "2px");
    assert.equal(await spinner.evaluate(el => getComputedStyle(el).animationName), "spin");
    await page.emulateMedia({ reducedMotion: "reduce" });
    assert.equal(await spinner.evaluate(el => getComputedStyle(el).animationName), "none");
    await page.emulateMedia({ reducedMotion: "no-preference" });
    await location.fill("Restaurant Skyline Hamburg");
    await page.locator(".suggest__item").waitFor();
    release();
    gate = undefined;
    await spinner.waitFor({ state: "hidden" });
    assert.equal(await page.locator(".suggest__item").textContent(), result.display_name);
    assert.equal(await latitude.inputValue(), "");
    await page.locator(".suggest__item").click();
    assert.equal(await location.inputValue(), "Restaurant Skyline Hamburg", "Preview must not silently replace the query");
    assert.equal(await latitude.inputValue(), "");
    await page.locator("[data-location-accept]").click();
    assert.equal(await location.inputValue(), result.display_name);
    assert.equal(await latitude.inputValue(), "53.55");
    assert.equal(await title.inputValue(), "Restaurant Skylien Hamburg", "Place selection must preserve the activity title");
    assert.equal(writes, 0, "Confirmation in the editor waits for Save");
    failure = true;
    await location.fill("Provider error");
    await page.waitForFunction(() => document.querySelector("[data-geo-lite-status]").textContent === "Search failed");
    assert.equal(await latitude.inputValue(), "");
    await spinner.waitFor({ state: "hidden" });
    failure = false;
    empty = true;
    await location.fill("Unknown restaurant");
    await page.waitForFunction(() => document.querySelector("[data-geo-lite-status]").textContent === "No place found");
    assert.equal(await proposal.isVisible(), false);
    empty = false;
    await location.fill("ab");
    assert.equal(await spinner.isVisible(), false);
    const before = calls;
    await page.goto("http://locations.test/?saved=pending");
    await proposal.waitFor({ state: "visible" });
    await page.setViewportSize({ width: 390, height: 800 });
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "Suggestion controls overflow mobile viewport");
    assert.equal(calls, before, "Cached proposals must not trigger another provider lookup");
    rejectFailure = true;
    await page.locator("[data-location-reject]").click();
    await page.waitForFunction(() => document.querySelector("[data-geo-lite-status]").textContent === "Rejection failed");
    assert.equal(await proposal.isVisible(), true);
    rejectFailure = false;
    await page.locator("[data-location-reject]").click();
    await proposal.waitFor({ state: "hidden" });
    assert.equal(writes, 2);
    assert.equal(await location.inputValue(), "");
    await page.goto("http://locations.test/?saved=rejected");
    assert.equal(calls, before);
    assert.equal(await proposal.isVisible(), false);
    assert.equal(await status.textContent(), "Rejected without changing your entry");
    await page.goto("http://locations.test/?saved=located");
    await title.fill("An edited activity title");
    assert.equal(calls, before, "Existing coordinates must not be replaced by title suggestions");
    assert.equal(await location.inputValue(), "Existing place");
    assert.equal(await latitude.inputValue(), "53");
    assert.deepEqual(errors, []);
  } finally {
    if (release) release();
    await context.close();
  }
}

import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

// Exercise the real application's event listeners, not the static demo script.
// Every request is fulfilled locally; no application server or provider is used.
export async function verifyCalendarRegionUpdates(browser) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const errors = [];
  const script = await readFile(new URL("../../web/static/js/app.js", import.meta.url), "utf8");
  const firstDay = "2026-10-09";
  const week = "2026-10-05";
  let calls = 0;
  let fail = false;
  let geocoderFail = false;
  let geocoderEmpty = false;
  let payload = {
    days: { [firstDay]: { label: "Southern Denmark", title: "First guesthouse" } },
    weeks: { [week]: [{ label: "Southern Denmark", title: "First guesthouse", start: 4, span: 3 }] },
  };
  page.on("pageerror", error => errors.push(error.message));
  await page.route("**/*", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/app.js") {
      await route.fulfill({ contentType: "text/javascript", body: script });
    } else if (path === "/api/geocode") {
      await route.fulfill({ status: geocoderFail ? 503 : 200, contentType: "application/json", body: JSON.stringify({
        results: geocoderEmpty ? [] : [{ display_name: "Ribe, Southern Denmark", lat: 55.328, lng: 8.762 }],
      }) });
    } else if (path === "/regions") {
      calls++;
      await route.fulfill({
        status: fail ? 503 : 200,
        contentType: "application/json",
        body: JSON.stringify(payload),
      });
    } else if (path === "/") {
      await route.fulfill({ contentType: "text/html", body: `<!doctype html>
        <html lang="en"><body>
        <div data-tabs>
          <button type="button" data-tab="overview">Overview</button>
          <button type="button" data-tab="tagesplan" class="is-active">Day plan</button>
          <section data-tab-panel="overview"></section>
          <section data-tab-panel="tagesplan" class="is-active">
            <div data-tagesplan data-calendar-regions-url="/regions">
              <input id="draft" value="Unchanged draft">
              <div id="item-error" role="alert"></div>
              <details id="week-visibility"><summary>Week</summary>
                <div data-region-week="${week}"><span>Unknown</span></div>
              </details>
              <div data-region-day="${firstDay}"><span>Unknown</span></div>
              <p data-calendar-regions-error data-error="Unable to refresh" hidden></p>
              <form data-geography-refresh></form>
              <form id="idea-editor">
                <input name="title" value="My original idea">
                <div class="location-picker__field">
                  <input name="location" data-geo-lite>
                  <div data-geo-lite-list hidden></div>
                  <p data-geo-lite-status data-error="Location search failed" data-empty="No matching place" hidden></p>
                </div>
                <input type="hidden" name="latitude" data-geo-lite-lat>
                <input type="hidden" name="longitude" data-geo-lite-lng>
              </form>
              <p data-planner-geography-status data-error="Lookup failed" data-pending="Resolving" hidden></p>
            </div>
          </section>
        </div>
        <script src="/app.js"></script></body></html>` });
    } else {
      errors.push(`Unexpected live regression request: ${path}`);
      await route.abort();
    }
  });
  try {
    await page.goto("http://127.0.0.1/");
    await page.locator("#draft").focus();
    await page.evaluate(() => {
      window.savedDraftNode = document.querySelector("#draft");
      window.savedWeekNode = document.querySelector("#week-visibility");
      document.body.dispatchEvent(new CustomEvent("geographyChanged"));
    });
    await page.waitForFunction(() => document.querySelector("[data-region-day]").textContent === "Southern Denmark");
    assert.equal(await page.locator("[data-region-week] span").evaluate(node => node.style.gridColumn), "6 / span 3");
    assert.deepEqual(await page.evaluate(() => ({
      draft: document.querySelector("#draft") === window.savedDraftNode,
      week: document.querySelector("#week-visibility") === window.savedWeekNode,
      value: document.querySelector("#draft").value,
      focus: document.activeElement.id,
      open: document.querySelector("#week-visibility").open,
      active: document.querySelector('[data-tab-panel="tagesplan"]').classList.contains("is-active"),
    })), { draft: true, week: true, value: "Unchanged draft", focus: "draft", open: false, active: true });
    payload = {
      days: { [firstDay]: { label: "Southern Denmark · Zealand", title: "Guesthouse; apartment" } },
      weeks: { [week]: [{ label: "Southern Denmark · Zealand", title: "Guesthouse; apartment", start: 4, span: 3 }] },
    };
    await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("itemsChanged")));
    await page.waitForFunction(() => document.querySelector("[data-region-day]").textContent.includes("Zealand"));
    const saved = await page.locator("[data-region-week]").innerHTML();
    payload = { days: { [firstDay]: { label: "Bad partial update", title: "Invalid" } }, weeks: {} };
    await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("infoChanged")));
    await page.locator("[data-calendar-regions-error]").waitFor();
    assert.equal(await page.locator("[data-region-week]").innerHTML(), saved);
    assert.equal(await page.locator("[data-region-day]").innerText(), "Southern Denmark · Zealand",
      "Malformed responses must not apply partial updates");
    fail = true;
    await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("geographyChanged")));
    await page.waitForFunction(() => document.querySelector("[data-calendar-regions-error]").textContent === "Unable to refresh");
    fail = false;
    payload = {
      days: { [firstDay]: { label: "Zealand", title: "Apartment" } },
      weeks: { [week]: [{ label: "Zealand", title: "Apartment", start: 4, span: 3 }] },
    };
    await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("geographyChanged")));
    await page.waitForFunction(() => document.querySelector("[data-region-day]").textContent === "Zealand");
    assert.equal(await page.locator("[data-calendar-regions-error]").isHidden(), true);
    await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("htmx:afterRequest", {
      detail: { elt: document.querySelector("[data-geography-refresh]"), successful: false },
    })));
    assert.equal(await page.locator("[data-planner-geography-status]").innerText(), "Lookup failed");
    await page.locator('#idea-editor [name="location"]').fill("Ribe");
    await page.locator("#idea-editor .suggest__item").click();
    assert.equal(await page.locator('#idea-editor [name="location"]').inputValue(), "Ribe, Southern Denmark");
    assert.equal(await page.locator('#idea-editor [name="latitude"]').inputValue(), "55.328");
    assert.equal(await page.locator('#idea-editor [name="longitude"]').inputValue(), "8.762");
    assert.equal(await page.locator('#idea-editor [name="title"]').inputValue(), "My original idea");
    geocoderFail = true;
    await page.locator('#idea-editor [name="location"]').fill("Different location");
    await page.waitForFunction(() => document.querySelector("[data-geo-lite-status]").textContent === "Location search failed");
    assert.equal(await page.locator('#idea-editor [name="latitude"]').inputValue(), "",
      "Typing a different location clears stale coordinates");
    geocoderFail = false;
    geocoderEmpty = true;
    await page.locator('#idea-editor [name="location"]').fill("Unknown location");
    await page.waitForFunction(() => document.querySelector("[data-geo-lite-status]").textContent === "No matching place");
    assert.deepEqual(await page.evaluate(() => {
      const detail = { target: document.querySelector("#item-error"), xhr: { status: 422 }, shouldSwap: false, isError: true };
      document.body.dispatchEvent(new CustomEvent("htmx:beforeSwap", { detail }));
      return { shouldSwap: detail.shouldSwap, isError: detail.isError };
    }), { shouldSwap: true, isError: true }, "Invalid location submissions must display their server error");
    assert.ok(calls >= 4, "Region completion and edit events must read updated saved values");
    assert.deepEqual(errors, []);
    console.log("Validated live calendar region updates, retained planner state, and explicit error handling.");
  } finally {
    await context.close();
  }
}

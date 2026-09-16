import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

// Exercise the real application's event listeners, not the static demo script.
// Every request is fulfilled locally; no application server or provider is used.
export async function verifyCalendarRegionUpdates(browser) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const errors = [];
  const script = await readFile(new URL("../../web/static/js/app.js", import.meta.url), "utf8");
  const htmx = await readFile(new URL("../../web/static/vendor/htmx/htmx.min.js", import.meta.url), "utf8");
  const ideaID = "11111111-1111-4111-8111-111111111111";
  const firstDay = "2026-10-09";
  const week = "2026-10-05";
  let calls = 0;
  let fail = false;
  let geocoderFail = false;
  let geocoderEmpty = false;
  let backgroundActive = false;
  let backgroundFailed = false;
  let payload = {
    days: { [firstDay]: { label: "Southern Denmark", title: "First guesthouse" } },
    weeks: { [week]: [{ label: "Southern Denmark", title: "First guesthouse", start: 4, span: 3 }] },
  };
  page.on("pageerror", error => errors.push(error.message));
  await page.route("**/*", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/app.js") {
      await route.fulfill({ contentType: "text/javascript", body: script });
    } else if (path === "/htmx.js") {
      await route.fulfill({ contentType: "text/javascript", body: htmx });
    } else if (path === "/background-status") {
      if (backgroundFailed) {
        await route.abort("failed");
      } else {
        await route.fulfill({ contentType: "text/html", body: backgroundActive
          ? '<div class="background-status__content" data-state="active" role="status">Updating data<progress value="3" max="10"></progress></div>'
          : '<div class="background-status__content" data-state="idle" role="status">No active requests</div>' });
      }
    } else if (path === `/items/${ideaID}/edit`) {
      await route.fulfill({ contentType: "text/html", body: `<li class="item-row is-editing" id="item-${ideaID}">
        <form><input name="title" value="Unresolved idea"><input name="cost" value="25">
        <input name="location" value="Choose this location"></form></li>` });
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
        <div id="background-status" data-unavailable-short="Status unavailable" data-unavailable="Background status could not be loaded."
             hx-get="/background-status" hx-trigger="load, refresh" hx-swap="innerHTML">
          <div data-state="checking">Checking status</div>
        </div>
        <div data-tabs>
          <button type="button" data-tab="overview" class="tabs__tab">Overview</button>
          <button type="button" data-tab="tagesplan" class="tabs__tab is-active">Day plan</button>
          <button type="button" data-tab="ideen" class="tabs__tab">Ideas</button>
          <section data-tab-panel="overview" class="tab-panel"></section>
          <section data-tab-panel="tagesplan" class="tab-panel is-active">
            <div data-tagesplan data-calendar-regions-url="/regions">
              <input id="draft" value="Unchanged draft">
              <div id="item-error" role="alert" data-edit-error="Cannot open editor"></div>
              <a href="#idea-location-${ideaID}" data-idea-location-edit="${ideaID}" draggable="false">Check location</a>
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
          <section data-tab-panel="ideen" class="tab-panel"><ul id="ideen-list"><li class="item-row" id="item-${ideaID}">Unresolved idea</li></ul></section>
        </div>
        <script src="/htmx.js"></script><script src="/app.js"></script></body></html>` });
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
    await page.locator("[data-idea-location-edit]").click();
    await page.waitForFunction(() => document.activeElement?.getAttribute("name") === "location");
    assert.equal(await page.locator('[data-tab-panel="ideen"]').evaluate(element => element.classList.contains("is-active")), true);
    assert.equal(await page.locator(`#item-${ideaID} [name="location"]`).inputValue(), "Choose this location");
    assert.ok(page.url().endsWith(`#idea-location-${ideaID}`));
    await page.locator('#background-status [data-state="idle"]').waitFor();
    backgroundActive = true;
    await page.evaluate(() => htmx.trigger("#background-status", "refresh"));
    await page.locator("#background-status progress").waitFor();
    backgroundActive = false;
    await page.evaluate(() => htmx.trigger("#background-status", "refresh"));
    await page.locator('#background-status [data-state="idle"]').waitFor();
    assert.equal(await page.locator("#background-status progress").count(), 0);
    assert.ok((await page.locator("#background-status").boundingBox()).height > 0, "Idle status must remain visible");
    backgroundFailed = true;
    await page.evaluate(() => htmx.trigger("#background-status", "refresh"));
    await page.locator('#background-status [data-state="unavailable"]').waitFor();
    assert.ok((await page.locator("#background-status").innerText()).includes("Status unavailable"));
    backgroundFailed = false;
    await page.evaluate(() => htmx.trigger("#background-status", "refresh"));
    await page.locator('#background-status [data-state="idle"]').waitFor();
    assert.deepEqual(errors, []);
    console.log("Validated live calendar updates, direct editing and persistent active/idle/unavailable background status.");
  } finally {
    await context.close();
  }
}

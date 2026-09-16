import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

export async function verifyAISearchCenters(browser) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const web = new URL("../../web/static/", import.meta.url);
  const app = await readFile(new URL("js/app.js", web), "utf8");
  const errors = [];
  let fail = false;
  let requests = 0;
  let releaseReverse;
  let delayedReverse;
  let centers = [
    { key: "destination", label: "Denmark (default)", name: "Denmark", lat: 55, lng: 10, zoom: 6 },
    { key: "region:west", label: "Jutland", name: "Jutland", lat: 56, lng: 8, zoom: 9 },
    { key: "custom", label: "Custom place", name: "", lat: null, lng: null, zoom: 9 },
  ];
  page.on("pageerror", error => errors.push(error.message));
  await page.route("**/*", async route => {
    const url = new URL(route.request().url());
    if (url.origin !== "http://127.0.0.1") {
      errors.push(`Unexpected external request: ${url}`);
      await route.abort();
      return;
    }
    if (url.pathname === "/app.js") {
      await route.fulfill({ contentType: "text/javascript", body: app });
    } else if (url.pathname === "/map-hooks.js") {
      await route.fulfill({ contentType: "text/javascript", body: `
        window.testMaps = [];
        const createMap = L.map;
        L.map = function(...args) { const map = createMap(...args); testMaps.push(map); return map; };
        L.tileLayer = function() { return { addTo() { return this; } }; };
      ` });
    } else if (/^\/static\/vendor\/leaflet\/(leaflet\.(js|css)|images\/marker-(icon(-2x)?|shadow)\.png)$/.test(url.pathname)) {
      await route.fulfill({
        contentType: url.pathname.endsWith(".css") ? "text/css" : url.pathname.endsWith(".js") ? "text/javascript" : "image/png",
        body: await readFile(new URL(url.pathname.slice("/static/".length), web)),
      });
    } else if (url.pathname === "/centers") {
      requests++;
      await route.fulfill({ status: fail ? 503 : 200, contentType: "application/json", body: JSON.stringify(centers) });
    } else if (url.pathname === "/api/reverse-geocode") {
      if (delayedReverse) await delayedReverse;
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ display_name: "Clicked custom place" }) });
    } else if (url.pathname === "/api/geocode") {
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({
        results: [{ display_name: "Typed custom place", lat: 53, lng: 7, type: "city" }],
      }) });
    } else if (url.pathname === "/") {
      await route.fulfill({ contentType: "text/html", body: `<!doctype html><html lang="en">
        <head><link rel="stylesheet" href="/static/vendor/leaflet/leaflet.css"></head><body>
        <form id="recommendations">
          <input name="interests" value="Museums"><input name="radius" type="range" min="1" max="300" value="50">
          <select name="count"><option value="10" selected>10</option></select>
          <div class="location-picker" data-geocode-reverse>
            <select data-ai-center name="ai_center" data-ai-centers-url="/centers">
              <option value="destination" data-name="Denmark" data-lat="55" data-lng="10" data-zoom="6">Denmark</option>
              <option value="region:west" data-name="Jutland" data-lat="56" data-lng="8" data-zoom="9">Jutland</option>
              <option value="custom">Custom place</option>
            </select>
            <div data-ai-custom-location hidden><input data-geocode-input name="ai_location" value="Denmark">
              <div data-geocode-list hidden></div></div>
            <input type="hidden" data-geocode-lat name="ai_lat" value="55">
            <input type="hidden" data-geocode-lng name="ai_lng" value="10">
            <div data-geocode-map style="width:640px;height:360px"></div>
            <p data-ai-centers-error data-error="Refresh failed" data-unavailable="Select another center" hidden></p>
          </div>
          <div id="ai-error"></div>
        </form>
        <script src="/static/vendor/leaflet/leaflet.js"></script><script src="/map-hooks.js"></script>
        <script src="/app.js"></script></body></html>` });
    } else {
      errors.push(`Unexpected request: ${url.pathname}`);
      await route.abort();
    }
  });
  try {
    await page.goto("http://127.0.0.1/", { waitUntil: "networkidle" });
    const select = page.locator("[data-ai-center]");
    const location = page.locator('[name="ai_location"]');
    const latitude = page.locator('[name="ai_lat"]');
    const longitude = page.locator('[name="ai_lng"]');
    await select.selectOption("region:west");
    assert.equal(await location.inputValue(), "Jutland");
    assert.equal(await latitude.inputValue(), "56.000000");
    assert.equal(await longitude.inputValue(), "8.000000");
    assert.equal(await page.locator("[data-ai-custom-location]").isHidden(), true);
    await page.waitForFunction(() => {
      const point = testMaps[0].getCenter();
      return Math.abs(point.lat - 56) < 1e-6 && Math.abs(point.lng - 8) < 1e-6;
    });
    assert.deepEqual(await page.evaluate(() => {
      const point = testMaps[0].getCenter();
      return [Math.round(point.lat), Math.round(point.lng)];
    }), [56, 8], "Dropdown moves the real Leaflet map");
    centers[1] = { ...centers[1], lat: 57, lng: 9 };
    await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("geographyChanged")));
    await page.waitForFunction(() => document.querySelector('[name="ai_lat"]').value === "57.000000");
    assert.equal(await select.inputValue(), "region:west");
    centers = centers.filter(center => center.key !== "region:west");
    await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("itemsChanged")));
    await page.waitForFunction(() => !document.querySelector("[data-ai-center]").validity.valid);
    assert.equal(await select.inputValue(), "region:west", "Removed region must not silently reset to the country");
    assert.equal(await latitude.inputValue(), "57.000000");
    await select.selectOption("destination");
    assert.equal(await select.evaluate(element => element.validity.valid), true);
    assert.equal(await latitude.inputValue(), "55.000000");
    assert.equal(await longitude.inputValue(), "10.000000");
    delayedReverse = new Promise(resolve => { releaseReverse = resolve; });
    await page.locator("[data-geocode-map]").click({ position: { x: 190, y: 130 } });
    assert.equal(await select.inputValue(), "custom");
    assert.equal(await page.locator("[data-ai-custom-location]").isVisible(), true);
    await select.selectOption("destination");
    releaseReverse();
    await page.waitForLoadState("networkidle");
    assert.equal(await location.inputValue(), "Denmark", "Late reverse lookup must not overwrite a newly selected preset");
    delayedReverse = undefined;
    await select.selectOption("custom");
    await location.fill("Ribe");
    await page.locator("[data-geocode-list] button").click();
    assert.equal(await select.inputValue(), "custom");
    assert.equal(await latitude.inputValue(), "53.000000");
    const custom = await location.inputValue();
    await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("geographyChanged")));
    await page.waitForLoadState("networkidle");
    assert.equal(await location.inputValue(), custom, "Background option refresh must preserve manual search text");
    assert.equal(await longitude.inputValue(), "7.000000");
    fail = true;
    await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("infoChanged")));
    await page.waitForFunction(() => document.querySelector("[data-ai-centers-error]").textContent === "Refresh failed");
    assert.equal(await select.inputValue(), "custom");
    assert.equal(await page.locator('[name="radius"]').inputValue(), "50");
    assert.equal(await page.locator('[name="count"]').inputValue(), "10");
    assert.equal(await page.locator('[name="interests"]').inputValue(), "Museums");
    assert.ok(requests >= 4);
    assert.deepEqual(errors, []);
    console.log("Validated real AI midpoint dropdown, Leaflet coordinates, background refresh and custom-point preservation.");
  } finally {
    if (releaseReverse) releaseReverse();
    await context.close();
  }
}

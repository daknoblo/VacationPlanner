import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

export async function verifyIdeasMap(browser) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const web = new URL("../../web/static/", import.meta.url);
  const script = await readFile(new URL("js/ideas-map.js", web), "utf8");
  const errors = [];
  const calls = [];
  let failMap = false;
  let failRoute = false;
  let gate;
  let release;
  const data = {
    routing: true,
    lodgings: [
      { id: "a", title: "Apartment <em>A</em>", date_range: "01.05.2027 – 03.05.2027", lat: 55, lng: 8 },
      { id: "b", title: "Campsite B", date_range: "03.05.2027 – 05.05.2027", lat: 56, lng: 9 },
      { id: "c", title: "Unlocated stay", date_range: "", lat: null, lng: null },
    ],
    ideas: [
      { id: "one", title: "Saved <script>unsafe()</script>", lat: 55.1, lng: 8.1 },
      { id: "two", title: "Scheduled idea", day: "02.05.2027", lat: 55.2, lng: 8.2 },
      { id: "three", title: "Unlocated idea", lat: null, lng: null },
    ],
  };
  page.on("pageerror", error => errors.push(error.message));
  await page.route("**/*", async route => {
    const url = new URL(route.request().url());
    if (url.origin !== "http://127.0.0.1") {
      errors.push(`External map request: ${url}`);
      return route.abort();
    }
    if (url.pathname === "/ideas-map.js") return route.fulfill({ contentType: "text/javascript", body: script });
    if (url.pathname === "/hooks.js") return route.fulfill({ contentType: "text/javascript", body: `
      const createMap = L.map;
      L.map = function(...args) { window.testMap = createMap(...args); return testMap; };
      L.tileLayer = function() { return {addTo() {return this;}}; };
    ` });
    if (/^\/leaflet\.(js|css)$/.test(url.pathname)) {
      return route.fulfill({
        contentType: url.pathname.endsWith(".js") ? "text/javascript" : "text/css",
        body: await readFile(new URL("vendor/leaflet" + url.pathname, web)),
      });
    }
    if (url.pathname === "/map") return route.fulfill({ status: failMap ? 503 : 200, json: data });
    if (url.pathname === "/drive") {
      calls.push({ origin: url.searchParams.get("lodging"), idea: url.searchParams.get("item") });
      const wait = gate;
      if (wait) await wait;
      return route.fulfill({ json: failRoute ? { status: "unavailable" } : {
        status: "ready", distance: url.searchParams.get("lodging") === "a" ? "12.3 km" : "24.6 km", duration: "1 h 16 min",
      } });
    }
    if (url.pathname === "/") return route.fulfill({ contentType: "text/html", body: `<!doctype html>
      <html><head><link rel="stylesheet" href="/leaflet.css"></head><body>
      <section id="tab" style="display:none"><section data-ideas-map-panel data-map-url="/map" data-route-url="/drive"
        data-loading="Loading" data-error="Map failed" data-missing="Missing location" data-no-origin="Choose origin"
        data-removed="Origin removed" data-pending="Pending" data-unavailable="Route unavailable"
        data-disabled="Routing disabled" data-ready="Ready" data-choose="Choose" data-no-ideas="Empty">
        <select data-ideas-map-origin></select><button data-ideas-map-retry>Retry</button>
        <div id="ideas-map" style="height:400px;width:640px"></div><p data-ideas-map-status></p>
        <table><tbody data-ideas-map-rows></tbody></table>
      </section></section>
      <script src="/leaflet.js"></script><script src="/hooks.js"></script><script src="/ideas-map.js"></script>
      </body></html>` });
    errors.push(`Unexpected map request: ${url}`);
    return route.abort();
  });
  const status = page.locator("[data-ideas-map-status]");
  const rows = page.locator("[data-ideas-map-rows]");
  const select = page.locator("[data-ideas-map-origin]");
  async function waitText(locator, value) {
    await locator.getByText(value, { exact: true }).first().waitFor();
  }
  async function refresh() {
    await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("itemsChanged")));
  }
  try {
    await page.goto("http://127.0.0.1/", { waitUntil: "networkidle" });
    assert.equal(calls.length, 0, "Hidden Ideas tab must not route");
    await page.evaluate(() => { document.querySelector("#tab").style.display = "block"; });
    await waitText(status, "Ready");
    assert.equal(await page.locator("#ideas-map .lodging-marker").count(), 2);
    assert.equal(await page.locator("#ideas-map .idea-map-marker").count(), 2);
    assert.equal(await rows.locator("tr").count(), 3, "Unlocated and scheduled ideas retained");
    assert.deepEqual(calls, [{ origin: "a", idea: "one" }, { origin: "a", idea: "two" }]);
    assert.equal(await rows.locator("script, em").count(), 0, "Saved names must not become HTML");
    assert.equal(await select.locator('option[value="c"]').isDisabled(), true);
    await waitText(rows, "12.3 km");
    await waitText(rows, "1 h 16 min");
    await rows.locator("button").first().click();
    await page.locator(".leaflet-popup-content").waitFor();
    assert.ok((await page.locator(".leaflet-popup-content").innerText()).includes("Saved <script>unsafe()</script>"));
    assert.ok((await page.locator(".leaflet-popup-content").innerText()).includes("12.3 km"));
    await select.selectOption("b");
    await waitText(status, "Ready");
    await waitText(rows, "24.6 km");
    assert.equal(calls.length, 4);
    await page.evaluate(() => testMap.setView([40, 7], 7, { animate: false }));
    data.ideas[0].title = "Renamed idea";
    await refresh();
    await waitText(rows, "1. Renamed idea");
    assert.equal(await select.inputValue(), "b");
    const center = await page.evaluate(() => testMap.getCenter());
    assert.ok(Math.abs(center.lat - 40) < 0.01 && Math.abs(center.lng - 7) < 0.01, "Metadata refresh preserves manual map view");
    assert.equal(calls.length, 4, "Refresh reuses successful coordinate-specific routes");

    data.ideas[0].lat = 55.4;
    gate = new Promise(resolve => { release = resolve; });
    const heldRequest = page.waitForRequest(request => request.url().includes("/drive"));
    await refresh();
    await page.waitForFunction(() => document.querySelector('[data-idea-id="one"]').textContent.includes("Pending"));
    // Wait for the held A/B request, then switch origin while it is in flight.
    await heldRequest;
    await select.selectOption("a");
    release();
    gate = undefined;
    await waitText(status, "Ready");
    await waitText(rows, "12.3 km");
    assert.equal(await select.inputValue(), "a", "Obsolete response must not restore prior origin");
    assert.ok(!(await rows.innerText()).includes("24.6 km"));

    data.ideas[0].lat = 55.5;
    data.ideas[1].lat = 55.3;
    gate = new Promise(resolve => { release = resolve; });
    const hiddenRequest = page.waitForRequest(request => request.url().includes("/drive"));
    await refresh();
    await hiddenRequest;
    await page.evaluate(() => { document.querySelector("#tab").style.display = "none"; });
    await page.waitForFunction(() => !document.querySelector("#ideas-map").getBoundingClientRect().width);
    const beforeHidden = calls.length;
    release();
    gate = undefined;
    await new Promise(resolve => setTimeout(resolve, 1900));
    assert.equal(calls.length, beforeHidden, "Hidden map must not request the next idea");
    await page.evaluate(() => { document.querySelector("#tab").style.display = "block"; });
    await waitText(status, "Ready");

    data.ideas[0].lat = 55.6;
    failRoute = true;
    await refresh();
    await waitText(status, "Route unavailable");
    assert.ok((await rows.innerText()).includes("Route unavailable"));
    failRoute = false;
    await page.locator("[data-ideas-map-retry]").click();
    await waitText(status, "Ready");
    data.routing = false;
    const beforeDisabled = calls.length;
    await refresh();
    await waitText(status, "Routing disabled");
    assert.equal(calls.length, beforeDisabled);
    assert.ok(!(await rows.innerText()).includes("12.3 km"), "Disabled routing must not show stale driving values");
    data.lodgings = data.lodgings.filter(lodging => lodging.id !== "a");
    await refresh();
    await waitText(status, "Choose origin");
    assert.equal(await select.inputValue(), "a", "Removed origin must not silently switch");
    failMap = true;
    await refresh();
    await waitText(status, "Map failed");
    assert.equal(await rows.locator("tr").count(), 0);
    failMap = false;
    await page.locator("[data-ideas-map-retry]").click();
    await waitText(status, "Choose origin");
    assert.deepEqual(errors, []);
  } finally {
    if (release) release();
    await context.close();
  }
}

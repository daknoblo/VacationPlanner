import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

export async function verifyIdeasMap(browser) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const web = new URL("../../web/static/", import.meta.url);
  const script = await readFile(new URL("js/ideas-map.js", web), "utf8");
  const errors = [];
  let requests = 0;
  let retries = 0;
  let failMap = false;
  let failRetry = false;
  let gate, release;
  const data = {
    routing: true, progress: { total: 4, completed: 4 }, progress_label: "4 saved / 0 failed / 0 pending",
    lodgings: [
      { id: "a", title: "House <em>A</em>", date_range: "01.05.2027 – 03.05.2027", lat: 55, lng: 8 },
      { id: "b", title: "Campsite B", date_range: "03.05.2027 – 05.05.2027", lat: 56, lng: 9 },
      { id: "c", title: "Unlocated stay", date_range: "", lat: null, lng: null },
    ],
    ideas: [
      { id: "one", title: "Saved <script>unsafe()</script>", lat: 55.1, lng: 8.1 },
      { id: "two", title: "Scheduled idea", day: "02.05.2027", lat: 55.2, lng: 8.2 },
      { id: "three", title: "Unlocated idea", lat: null, lng: null },
    ],
  };
  const routes = {};
  for (const origin of data.lodgings.slice(0, 2)) {
    routes[origin.id] = {};
    for (const idea of data.ideas.slice(0, 2)) routes[origin.id][idea.id] = {
      status: "ready", distance: origin.id === "a" ? "12.3 km" : "24.6 km", duration: "1 h 16 min",
      geometry: [[origin.lat, origin.lng], [55.05, 8.3], [idea.lat, idea.lng]],
    };
  }
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
    if (url.pathname === "/map") {
      requests++;
      const json = JSON.stringify({ ...data, routes: routes[url.searchParams.get("lodging")] || {} });
      if (gate && url.searchParams.get("lodging") === "a") await gate;
      return route.fulfill({ status: failMap ? 503 : 200, contentType: "application/json", body: json });
    }
    if (url.pathname === "/retry") {
      assert.equal(route.request().method(), "POST");
      assert.equal(new URLSearchParams(route.request().postData()).get("csrf_token"), "test-csrf");
      retries++;
      return route.fulfill({ status: failRetry ? 503 : 200, json: { status: "queued" } });
    }
    if (url.pathname === "/") return route.fulfill({ contentType: "text/html", body: `<!doctype html>
      <html><head><meta name="csrf-token" content="test-csrf"><link rel="stylesheet" href="/leaflet.css"></head><body>
      <section id="tab" style="display:none"><section data-ideas-map-panel data-map-url="/map" data-retry-url="/retry"
        data-loading="Loading" data-error="Map failed" data-missing="Missing location" data-no-origin="Choose origin"
        data-removed="Origin removed" data-pending="Pending" data-unavailable="Route unavailable"
        data-disabled="Routing disabled" data-ready="Ready" data-choose="Overview" data-overview="All accommodations and ideas"
        data-no-geometry="Road geometry missing" data-retry-error="Retry failed" data-no-ideas="Empty">
        <select data-ideas-map-origin></select><button data-ideas-map-refresh>Refresh map</button><button data-ideas-map-retry>Retry</button>
        <div id="ideas-map" style="height:400px;width:640px"></div><p data-ideas-map-status></p>
        <p data-ideas-map-cache-status></p>
        <table><tbody data-ideas-map-rows></tbody></table>
      </section></section>
      <script src="/leaflet.js"></script><script src="/hooks.js"></script><script src="/ideas-map.js"></script>
      </body></html>` });
    errors.push(`Unexpected request (GET must not start routing): ${url}`);
    return route.abort();
  });
  const status = page.locator("[data-ideas-map-status]");
  const rows = page.locator("[data-ideas-map-rows]");
  const select = page.locator("[data-ideas-map-origin]");
  async function waitText(locator, value) {
    await locator.getByText(value, { exact: false }).first().waitFor();
  }
  async function refresh() {
    await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("itemsChanged")));
  }
  async function lines() {
    return page.evaluate(() => {
      const lines = [];
      testMap.eachLayer(layer => { if (layer instanceof L.Polyline) lines.push(layer.getLatLngs().map(p => [p.lat, p.lng])); });
      return lines;
    });
  }
  try {
    await page.goto("http://127.0.0.1/", { waitUntil: "networkidle" });
    assert.equal(requests, 0);
    await page.evaluate(() => { document.querySelector("#tab").style.display = "block"; });
    await waitText(status, "All accommodations and ideas");
    assert.equal(await select.inputValue(), "", "Overview must be the default");
    assert.equal(await page.locator("#ideas-map .lodging-marker").count(), 2);
    assert.equal(await page.locator("#ideas-map .idea-map-marker").count(), 2);
    assert.equal(await rows.locator("tr").count(), 3);
    assert.deepEqual(await lines(), [], "Overview must not guess an origin");
    assert.equal(await rows.locator("script, em").count(), 0);
    assert.equal(await select.locator('option[value="c"]').isDisabled(), true);
    assert.ok(await page.evaluate(() => testMap.getBounds().contains([55, 8]) && testMap.getBounds().contains([56, 9])));

    await select.selectOption("a");
    await waitText(status, "Ready");
    assert.deepEqual(await page.evaluate(() => [testMap.getCenter().lat, testMap.getCenter().lng, testMap.getZoom()]), [55, 8, 12]);
    assert.deepEqual(await lines(), [routes.a.one.geometry, routes.a.two.geometry], "All provider road vertices must be used, not straight lines");
    await waitText(page.locator("[data-ideas-map-cache-status]"), "4 saved / 0 failed / 0 pending");
    const refreshed = page.waitForResponse(response => response.url().includes("/map?lodging=a"));
    await page.locator("[data-ideas-map-refresh]").click();
    await refreshed;
    assert.equal(retries, 0, "Refreshing the map must only read cached data");
    await waitText(rows, "12.3 km");
    await rows.locator("button").first().click();
    await page.locator(".leaflet-popup-content").waitFor();
    assert.ok((await page.locator(".leaflet-popup-content").innerText()).includes("Saved <script>unsafe()</script>"));
    await select.selectOption("b");
    await waitText(status, "Ready");
    await waitText(rows, "24.6 km");
    assert.deepEqual(await page.evaluate(() => [testMap.getCenter().lat, testMap.getCenter().lng, testMap.getZoom()]), [56, 9, 12]);
    assert.deepEqual(await lines(), [routes.b.one.geometry, routes.b.two.geometry]);
    await page.evaluate(() => testMap.setView([40, 7], 7, { animate: false }));
    data.ideas[0].title = "Renamed idea";
    await refresh();
    await waitText(rows, "1. Renamed idea");
    assert.equal(await select.inputValue(), "b");
    const center = await page.evaluate(() => testMap.getCenter());
    assert.ok(Math.abs(center.lat - 40) < 0.01 && Math.abs(center.lng - 7) < 0.01, "Background refresh must preserve manual view");
    await select.selectOption("");
    await waitText(status, "All accommodations and ideas");
    assert.deepEqual(await lines(), []);
    assert.ok(await page.evaluate(() => testMap.getBounds().contains([55, 8]) && testMap.getBounds().contains([56, 9])));

    gate = new Promise(resolve => { release = resolve; });
    const held = page.waitForRequest(request => request.url().includes("/map?lodging=a"));
    await select.selectOption("a");
    await held;
    await select.selectOption("b");
    await waitText(status, "Ready");
    release();
    gate = undefined;
    assert.equal(await select.inputValue(), "b");
    assert.deepEqual(await lines(), [routes.b.one.geometry, routes.b.two.geometry], "Obsolete origin response must not replace roads");

    const saved = routes.b.one;
    delete routes.b.one;
    data.progress.completed = 3;
    await refresh();
    await waitText(status, "Pending");
    assert.equal((await lines()).length, 1);
    routes.b.one = saved;
    data.progress.completed = 4;
    await waitText(status, "Ready"); // Read-only polling discovers completed background work.
    assert.equal((await lines()).length, 2);
    routes.b.one = { status: "unavailable" };
    data.progress_label = "3 saved / 1 failed / 0 pending";
    await refresh();
    await waitText(status, "Route unavailable");
    await waitText(page.locator("[data-ideas-map-cache-status]"), "3 saved / 1 failed / 0 pending");
    assert.equal((await lines()).length, 1, "Failed route must never become a straight-line fallback");
    failRetry = true;
    await page.locator("[data-ideas-map-retry]").click();
    await waitText(status, "Retry failed");
    failRetry = false;
    routes.b.one = saved;
    data.progress_label = "4 saved / 0 failed / 0 pending";
    await page.locator("[data-ideas-map-retry]").click();
    await waitText(status, "Ready");
    assert.equal(retries, 2);
    data.routing = false;
    await refresh();
    await waitText(status, "Routing disabled");
    assert.deepEqual(await lines(), []);
    assert.ok(!(await rows.innerText()).includes("24.6 km"));
    data.lodgings = data.lodgings.filter(lodging => lodging.id !== "b");
    await refresh();
    await waitText(status, "Choose origin");
    assert.equal(await select.inputValue(), "b");
    failMap = true;
    await refresh();
    await waitText(status, "Map failed");
    assert.equal(await rows.locator("tr").count(), 0);
    assert.deepEqual(await lines(), []);
    failMap = false;
    await page.locator("[data-ideas-map-refresh]").click();
    await waitText(status, "Choose origin");
    assert.equal(retries, 2, "Recovering a cache read must not retry provider work");
    await page.evaluate(() => { document.querySelector("#tab").style.display = "none"; });
    await new Promise(resolve => setTimeout(resolve, 100));
    const before = requests;
    await new Promise(resolve => setTimeout(resolve, 3200));
    assert.equal(requests, before, "Hidden tabs must stop polling (not the independent server worker)");
    assert.deepEqual(errors, []);
    console.log("Validated overview framing, accommodation zoom, all road geometries and read-only background polling.");
  } finally {
    if (release) release();
    await context.close();
  }
}

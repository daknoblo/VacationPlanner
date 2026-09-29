import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

export async function verifyIdeasMap(browser) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const web = new URL("../../web/static/", import.meta.url);
  const script = await readFile(new URL("js/ideas-map.js", web), "utf8");
  const errors = [];
  let requests = 0;
  let failMap = false;
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
    if (url.pathname === "/app.css") return route.fulfill({ contentType: "text/css", body: await readFile(new URL("css/app.css", web)) });
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
      assert.equal(route.request().method(), "GET", "Map interactions must only read the saved cache");
      requests++;
      const json = JSON.stringify({ ...data, routes: routes[url.searchParams.get("lodging")] || {} });
      if (gate && url.searchParams.get("lodging") === "a") await gate;
      return route.fulfill({ status: failMap ? 503 : 200, contentType: "application/json", body: json });
    }
    if (url.pathname === "/") return route.fulfill({ contentType: "text/html", body: `<!doctype html>
      <html><head><meta charset="utf-8"><link rel="stylesheet" href="/leaflet.css"><link rel="stylesheet" href="/app.css"></head><body>
      <section id="tab" style="display:none"><section data-ideas-map-panel data-map-url="/map"
        data-loading="Loading" data-error="Map failed" data-missing="Missing location" data-no-origin="Choose origin"
        data-removed="Origin removed" data-pending="Pending" data-unavailable="Route unavailable"
        data-disabled="Routing disabled" data-ready="Ready" data-choose="Overview" data-overview="All accommodations and ideas"
        data-no-geometry="Road geometry missing" data-no-ideas="Empty" data-location-warning="Location missing - open editor">
        <select data-ideas-map-origin></select>
        <div id="ideas-map" style="height:400px;width:640px"></div><p data-ideas-map-status></p>
        <p data-ideas-map-cache-status></p>
        <div class="ideas-map-table" style="height:65px"><table><tbody data-ideas-map-rows></tbody></table></div>
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
    assert.equal(await rows.locator(".idea-location-warning").count(), 1);
    assert.equal(await rows.locator(".idea-location-warning").getAttribute("data-idea-location-edit"), "three");
    assert.equal(await rows.locator(".idea-location-warning").getAttribute("aria-label"), "Location missing - open editor");
    assert.deepEqual(await rows.locator(".idea-location-warning").evaluate(el => {
      const style = getComputedStyle(el);
      const rect = el.getBoundingClientRect();
      return [style.backgroundColor, style.borderRadius, Math.round(rect.width) === Math.round(rect.height)];
    }), ["rgb(254, 240, 138)", "50%", true], "Missing location must be shown as a small yellow circle");
    assert.deepEqual(await lines(), [], "Overview must not guess an origin");
    assert.equal(await rows.locator("script, em").count(), 0);
    assert.equal(await select.locator('option[value="c"]').isDisabled(), true);
    assert.ok(await page.evaluate(() => testMap.getBounds().contains([55, 8]) && testMap.getBounds().contains([56, 9])));

    await select.selectOption("a");
    await waitText(status, "Ready");
    const nearZoom = await page.evaluate(() => testMap.getZoom());
    assert.ok(await page.evaluate(() => [[55, 8], [55.1, 8.1], [55.2, 8.2], [55.05, 8.3]].every(p => testMap.getBounds().contains(p))),
      "Selected view must contain the origin, ideas and provider detours");
    assert.ok(await page.evaluate(() => !testMap.getBounds().contains([56, 9])), "Unrelated accommodation must not force the selected view out");
    assert.deepEqual(await lines(), [routes.a.one.geometry, routes.a.two.geometry], "All provider road vertices must be used, not straight lines");
    await page.locator("#ideas-map").evaluate(el => { el.style.width = "320px"; });
    await page.waitForFunction(() => testMap.getSize().x === 320 && testMap.getBounds().contains([55.05, 8.3]));
    assert.ok(await page.evaluate(() => [[55, 8], [55.2, 8.2]].every(p => testMap.getBounds().contains(p))), "Resizing must preserve automatic framing");
    await page.locator("#ideas-map").evaluate(el => { el.style.width = "640px"; });
    await page.waitForFunction(() => testMap.getSize().x === 640);
    routes.a.one.geometry[1] = [55.05, 9];
    await refresh();
    await page.waitForFunction(() => testMap.getBounds().contains([55.05, 9]), null, { timeout: 5000 });
    routes.a.one.geometry[1] = [55.05, 8.3];
    await refresh();
    await page.waitForFunction(() => !testMap.getBounds().contains([55.05, 9]), null, { timeout: 5000 });
    await waitText(page.locator("[data-ideas-map-cache-status]"), "4 saved / 0 failed / 0 pending");
    await page.waitForFunction(() => document.querySelectorAll(".idea-route-distance").length === 2);
    assert.deepEqual(await page.locator(".idea-route-distance").allTextContents(), ["1 · 12.3 km", "2 · 12.3 km"]);
    const pageY = await page.evaluate(() => scrollY);
    await page.locator('.idea-driving-route[data-idea-id="two"]').dispatchEvent("mouseover");
    assert.equal(await rows.locator("tr.is-route-active").getAttribute("data-idea-id"), "two");
    assert.equal(await rows.locator("tr.is-route-active td").first().evaluate(el => getComputedStyle(el).backgroundColor), "rgb(239, 246, 255)");
    assert.ok(await page.locator(".ideas-map-table").evaluate(el => {
      const row = el.querySelector('[data-idea-id="two"]').getBoundingClientRect();
      const bounds = el.getBoundingClientRect();
      return el.scrollTop > 0 && row.top >= bounds.top - 1 && row.bottom <= bounds.bottom + 1;
    }), "Hover must reveal the correct row inside the table");
    assert.equal(await page.evaluate(() => scrollY), pageY, "Route hover must not scroll the page away from the pointer");
    await page.locator('.idea-driving-route[data-idea-id="two"]').dispatchEvent("mouseout");
    assert.equal(await rows.locator("tr.is-route-active").count(), 0);
    await page.locator('.idea-driving-route[data-idea-id="one"]').focus();
    assert.equal(await rows.locator("tr.is-route-active").getAttribute("data-idea-id"), "one", "Keyboard focus must also highlight the destination");
    await waitText(rows, "12.3 km");
    await rows.locator("button").first().click();
    await page.locator(".leaflet-popup-content").waitFor();
    assert.ok((await page.locator(".leaflet-popup-content").innerText()).includes("Saved <script>unsafe()</script>"));
    await select.selectOption("b");
    await waitText(status, "Ready");
    await waitText(rows, "24.6 km");
    const farZoom = await page.evaluate(() => testMap.getZoom());
    assert.ok(farZoom < nearZoom, `Distant accommodation must use a wider zoom than the near accommodation (${farZoom} vs ${nearZoom})`);
    assert.ok(await page.evaluate(() => [[56, 9], [55.1, 8.1], [55.2, 8.2], [55.05, 8.3]].every(p => testMap.getBounds().contains(p))));
    assert.deepEqual(await lines(), [routes.b.one.geometry, routes.b.two.geometry]);
    await page.evaluate(() => testMap.setView([40, 7], 7, { animate: false }));
    data.ideas[0].title = "Renamed idea";
    routes.b.one.geometry[1] = [60, 12];
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
    routes.b.one = saved;
    data.progress_label = "4 saved / 0 failed / 0 pending";
    await waitText(status, "Ready");
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
    await select.selectOption("a");
    await waitText(status, "Routing disabled");
    await page.evaluate(() => { document.querySelector("#tab").style.display = "none"; });
    await new Promise(resolve => setTimeout(resolve, 100));
    const before = requests;
    await new Promise(resolve => setTimeout(resolve, 3200));
    assert.equal(requests, before, "Hidden tabs must stop polling (not the independent server worker)");
    assert.deepEqual(errors, []);
    await verifyRouteRetryForm(context);
    console.log("Validated adaptive route bounds, saved distance labels, hover/keyboard table highlights and read-only polling.");
  } finally {
    if (release) release();
    await context.close();
  }

  async function verifyRouteRetryForm(context) {
    const page = await context.newPage();
    const web = new URL("../../web/static/", import.meta.url);
    const errors = [];
    let code = 200;
    let posts = 0;
    page.on("pageerror", error => errors.push(error.message));
    await page.route("**/*", async route => {
      const url = new URL(route.request().url());
      if (url.origin !== "http://127.0.0.1") {
        errors.push(`Unexpected external request: ${url}`);
        return route.abort();
      }
      if (url.pathname === "/app.js" || url.pathname === "/htmx.js") {
        return route.fulfill({ contentType: "text/javascript",
          body: await readFile(new URL(url.pathname === "/app.js" ? "js/app.js" : "vendor/htmx/htmx.min.js", web)) });
      }
      if (url.pathname === "/settings/route/retry") {
        assert.equal(route.request().method(), "POST");
        const values = new URLSearchParams(route.request().postData());
        assert.equal(values.get("vacation_id"), "chosen-trip");
        assert.equal(values.get("csrf_token"), "test-token");
        posts++;
        return route.fulfill({ status: code, contentType: "text/html",
          headers: code === 422 ? { "HX-Retarget": "#route-retry-status", "HX-Reswap": "innerHTML" } : {},
          body: code === 200 ? "<p>Queued for selected trip</p>" : code === 422 ? "<div>Choose an existing trip</div>" : "Server error" });
      }
      if (url.pathname === "/settings") return route.fulfill({ contentType: "text/html", body: `<!doctype html>
        <html><head><meta charset="utf-8"></head><body>
        <form method="post" action="/settings/route/retry" hx-post="/settings/route/retry"
          hx-target="#route-retry-status" hx-swap="innerHTML" hx-disabled-elt="find button"
          data-route-retry-form data-pending="Preparing" data-error="Retry failed">
          <input type="hidden" name="csrf_token" value="test-token">
          <select name="vacation_id" required><option value="">Choose</option><option value="chosen-trip">Trip</option></select>
          <button>Retry</button><div id="route-retry-status" role="status"></div>
        </form><script src="/htmx.js"></script><script src="/app.js"></script></body></html>` });
      errors.push(`Unexpected request: ${url}`);
      return route.abort();
    });
    try {
      await page.goto("http://127.0.0.1/settings");
      await page.locator("select").selectOption("chosen-trip");
      for (const [status, message] of [[200, "Queued for selected trip"], [422, "Choose an existing trip"], [500, "Retry failed"]]) {
        code = status;
        await page.locator("button").click();
        await page.locator("#route-retry-status").getByText(message, { exact: true }).waitFor();
        assert.equal(await page.locator("select").inputValue(), "chosen-trip", "A status update must preserve trip selection");
      }
      assert.equal(posts, 3);
      assert.deepEqual(errors, []);
    } finally {
      await page.close();
    }
  }
}

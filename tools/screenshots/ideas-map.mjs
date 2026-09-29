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
  let failSchedule = false;
  let scheduleGate, finishSchedule;
  const schedules = [];
  let gate, release;
  const data = {
    routing: true, progress: { total: 4, completed: 4 }, progress_label: "4 saved / 0 failed / 0 pending",
    days: [{ value: "2027-05-01", label: "01.05.2027" }, { value: "2027-05-02", label: "02.05.2027" }],
    lodgings: [
      { id: "a", title: "House <em>A</em>", date_range: "01.05.2027 – 03.05.2027", lat: 55, lng: 8 },
      { id: "b", title: "Campsite B", date_range: "03.05.2027 – 05.05.2027", lat: 56, lng: 9 },
      { id: "c", title: "Unlocated stay", date_range: "", lat: null, lng: null },
    ],
    ideas: [
      { id: "one", title: "Saved <script>unsafe()</script>", description: "A museum with <b>historic boats</b>.",
        scheduled_day: "2027-05-02", day: "02.05.2027", lat: 55.1, lng: 8.1 },
      { id: "two", title: "Scheduled idea", day: "02.05.2027", scheduled_day: "2027-05-02", lat: 55.2, lng: 8.2 },
      { id: "three", title: "Unlocated idea", lat: null, lng: null },
    ],
  };
  const routes = {};
  for (const origin of data.lodgings.slice(0, 2)) {
    routes[origin.id] = {};
    for (const idea of data.ideas.slice(0, 2)) routes[origin.id][idea.id] = {
      status: "ready", distance: origin.id === "a" ? "12.3 km" : "24.6 km", duration: "1 h 16 min",
      distance_m: origin.id === "a" ? 12300 : 24600, duration_s: 4560, lodging_id: origin.id,
      geometry: [[origin.lat, origin.lng], [55.05, 8.3], [idea.lat, idea.lng]],
    };
  }
  Object.assign(routes.b.two, { distance: "9.0 km", distance_m: 9000, duration: "1 h 30 min", duration_s: 5400 });
  page.on("pageerror", error => errors.push(error.message));
  await page.route("**/*", async route => {
    const url = new URL(route.request().url());
    if (url.origin !== "http://127.0.0.1") {
      errors.push(`External map request: ${url}`);
      return route.abort();
    }
    if (url.pathname === "/ideas-map.js") return route.fulfill({ contentType: "text/javascript", body: script });
    if (url.pathname === "/app.js") return route.fulfill({ contentType: "text/javascript", body: await readFile(new URL("js/app.js", web)) });
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
      const overview = {};
      for (const idea of data.ideas) {
        const candidates = Object.values(routes).map(saved => saved[idea.id]).filter(value => value?.status === "ready")
          .sort((a, b) => a.distance_m - b.distance_m || a.duration_s - b.duration_s);
        if (candidates.length) overview[idea.id] = { ...candidates[0], geometry: undefined };
      }
      const origin = url.searchParams.get("lodging");
      const json = JSON.stringify({ ...data, routes: origin ? routes[origin] || {} : overview });
      if (gate && url.searchParams.get("lodging") === "a") await gate;
      return route.fulfill({ status: failMap ? 503 : 200, contentType: "application/json", body: json });
    }
    if (url.pathname === "/items/one/schedule") {
      assert.equal(route.request().method(), "POST");
      assert.equal(route.request().headers()["x-csrf-token"], "test-csrf");
      const values = new URLSearchParams(route.request().postData());
      assert.equal(values.get("day_only"), "1");
      assert.equal(values.has("start"), false, "Date-only scheduling must not invent times");
      schedules.push(values.get("day"));
      if (scheduleGate) await scheduleGate;
      if (failSchedule) return route.fulfill({ status: 500, body: "Save failed" });
      data.ideas[0].scheduled_day = values.get("day");
      data.ideas[0].day = data.days.find(day => day.value === values.get("day")).label;
      return route.fulfill({ status: 200, contentType: "text/html",
        body: '<div class="planner-block" data-id="one">10:00 — Saved idea</div>' });
    }
    if (url.pathname === "/") return route.fulfill({ contentType: "text/html", body: `<!doctype html>
      <html><head><meta charset="utf-8"><link rel="stylesheet" href="/leaflet.css"><link rel="stylesheet" href="/app.css"></head><body>
      <section id="tab" style="display:none"><section data-ideas-map-panel data-map-url="/map"
        data-loading="Loading" data-error="Map failed" data-missing="Missing location" data-no-origin="Choose origin"
        data-removed="Origin removed" data-pending="Pending" data-unavailable="Route unavailable"
        data-disabled="Routing disabled" data-ready="Ready" data-choose="Overview" data-overview="All accommodations and ideas"
        data-no-geometry="Road geometry missing" data-no-ideas="Empty" data-location-warning="Location missing - open editor"
        data-focus-route="Show the entire route" data-route-from="From:" data-schedule="Plan for day" data-unscheduled="Choose day"
        data-schedule-error="Could not save the day" data-saving="Saving">
        <select data-ideas-map-origin></select>
        <div id="ideas-map" class="ideas-map" style="height:400px;width:640px"></div><p data-ideas-map-status></p>
        <p data-ideas-map-cache-status></p>
        <div class="ideas-map-table" style="height:65px"><table><thead><tr>
          <th>Idea</th><th aria-sort="none"><button data-ideas-sort="distanceM">Distance <span aria-hidden="true">↕</span></button></th>
          <th aria-sort="none"><button data-ideas-sort="durationS">Time <span aria-hidden="true">↕</span></button></th><th>Plan</th>
        </tr></thead><tbody data-ideas-map-rows></tbody></table></div>
      </section></section>
      <div hidden>
        <div data-planner-grid data-day="2027-05-01"></div>
        <div data-planner-grid data-day="2027-05-02"><div class="planner-block" data-id="one">Old block</div></div>
      </div>
      <script src="/leaflet.js"></script><script src="/hooks.js"></script><script src="/app.js"></script><script src="/ideas-map.js"></script>
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
  async function waitRoads(count) {
    await page.waitForFunction(expected => document.querySelectorAll(".idea-driving-route").length === expected, count);
  }
  async function assertLabels(count) {
    await page.waitForFunction(expected => [...document.querySelectorAll(".idea-route-distance")]
      .filter(el => getComputedStyle(el).visibility === "visible").length === expected, count, { timeout: 5000 }).catch(async error => {
      console.error("Unexpected label layout", JSON.stringify(await page.evaluate(() => ({
        size: testMap.getSize(),
        obstacles: [...document.querySelectorAll("#ideas-map .leaflet-control, #ideas-map .leaflet-marker-icon, #ideas-map .leaflet-popup")]
          .map(el => ({ class: el.className, opacity: getComputedStyle(el).opacity, rect: el.getBoundingClientRect().toJSON() })),
        labels: [...document.querySelectorAll(".idea-route-distance")].slice(0, 20).map(el => ({
          text: el.textContent, width: el.offsetWidth, height: el.offsetHeight,
          visibility: getComputedStyle(el).visibility,
          x: el.getBoundingClientRect().x, y: el.getBoundingClientRect().y,
        })),
      }))));
      throw error;
    });
    const layout = await page.evaluate(() => {
      const map = document.querySelector("#ideas-map").getBoundingClientRect();
      const labels = [...document.querySelectorAll(".idea-route-distance")]
        .filter(el => getComputedStyle(el).visibility === "visible").map(el => el.getBoundingClientRect());
      return {
        inside: labels.every(r => r.left >= map.left && r.top >= map.top && r.right <= map.right && r.bottom <= map.bottom),
        overlaps: labels.some((a, i) => labels.slice(i + 1).some(b =>
          a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top)),
      };
    });
    assert.equal(layout.inside, true, "Every visible distance label must fit inside the map");
    assert.equal(layout.overlaps, false, "Distance labels must never overlap");
  }
  try {
    await context.addCookies([{ name: "csrf_token", value: "test-csrf", url: "http://127.0.0.1" }]);
    await page.goto("http://127.0.0.1/", { waitUntil: "networkidle" });
    assert.equal(requests, 0);
    await page.evaluate(() => { document.querySelector("#tab").style.display = "block"; });
    await page.waitForFunction(() => document.querySelectorAll("[data-ideas-map-rows] tr").length === 3);
    assert.equal(await status.isVisible(), false, "No redundant success paragraph");
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
    assert.deepEqual(await lines(), [], "Overview keeps the full map without drawing mixed-origin roads");
    assert.equal(await rows.locator('[data-idea-id="one"] td').nth(1).textContent(), "12.3 km");
    assert.equal(await rows.locator('[data-idea-id="two"] td').nth(1).textContent(), "9.0 km");
    assert.equal(await rows.locator('[data-idea-id="two"] .ideas-map-start').textContent(), "From: Campsite B");
    assert.equal(await rows.locator('[data-idea-id="one"] .ideas-map-start').textContent(), "From: House <em>A</em>");
    const distanceSort = page.locator('[data-ideas-sort="distanceM"]');
    const durationSort = page.locator('[data-ideas-sort="durationS"]');
    const order = () => rows.locator("tr").evaluateAll(elements => elements.map(el => el.dataset.ideaId));
    const view = await page.evaluate(() => [testMap.getCenter(), testMap.getZoom()]);
    await distanceSort.click();
    assert.deepEqual(await order(), ["two", "one", "three"], "Sort numeric meters, not the formatted label");
    assert.equal(await distanceSort.locator("..").getAttribute("aria-sort"), "ascending");
    await distanceSort.press("Enter");
    assert.deepEqual(await order(), ["one", "two", "three"], "Descending sort still keeps missing values last");
    await durationSort.click();
    assert.deepEqual(await order(), ["one", "two", "three"]);
    await durationSort.press("Space");
    assert.deepEqual(await order(), ["two", "one", "three"]);
    assert.equal(await distanceSort.locator("..").getAttribute("aria-sort"), "none");
    assert.equal(await rows.locator("tr").first().locator(".ideas-map-link").textContent(), "2. Scheduled idea");
    assert.deepEqual(await page.evaluate(() => [testMap.getCenter(), testMap.getZoom()]), view,
      "Table sorting must not move the map or renumber markers");
    const original = { ...routes.a.one };
    Object.assign(routes.a.one, { distance: "0 m", distance_m: 0, duration: "0 min", duration_s: 0 });
    await refresh();
    await waitText(rows, "0 m");
    assert.deepEqual(await order(), ["two", "one", "three"], "Background refresh retains the current sort");
    await durationSort.click();
    assert.deepEqual(await order(), ["one", "two", "three"], "Zero duration is valid, not missing");
    Object.assign(routes.a.one, original);
    await refresh();
    await waitText(rows, "12.3 km");
    assert.equal(await rows.locator("script, em").count(), 0);
    assert.equal(await select.locator('option[value="c"]').isDisabled(), true);
    assert.ok(await page.evaluate(() => testMap.getBounds().contains([55, 8]) && testMap.getBounds().contains([56, 9])));
    await page.locator(".idea-map-marker").first().click();
    await page.locator(".leaflet-popup-content").waitFor();
    assert.equal(await select.inputValue(), "", "Without a saved route, a number must not guess an origin");
    assert.deepEqual(await lines(), []);
    await page.evaluate(() => testMap.closePopup());

    await select.selectOption("a");
    await waitRoads(2);
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
    assert.deepEqual(await page.locator(".idea-route-label").evaluateAll(elements => elements.map(el =>
      [el.querySelector(".idea-route-number").textContent, ...[...el.querySelectorAll(".idea-route-metrics > span")].map(span => span.textContent)])),
    [["1", "12.3 km", "1 h 16 min"], ["2", "12.3 km", "1 h 16 min"]]);
    assert.ok(await page.locator(".idea-route-number").evaluateAll(elements => elements.every(el => {
      const rect = el.getBoundingClientRect();
      return rect.width === rect.height && getComputedStyle(el).borderRadius === "50%";
    })), "Label numbers must be circles, not plain text");
    await assertLabels(2);
    await page.mouse.move(1100, 20);
    const pageY = await page.evaluate(() => scrollY);
    await page.locator('.idea-driving-route[data-idea-id="two"]').dispatchEvent("mouseover");
    assert.equal(await page.locator('.idea-driving-route[data-idea-id="two"]').evaluate(el => getComputedStyle(el).stroke), "rgb(192, 38, 211)",
      "Hovered road must change from blue to a clearly different magenta");
    assert.equal(await rows.locator("tr.is-route-active").getAttribute("data-idea-id"), "two");
    assert.equal(await rows.locator("tr.is-route-active td").first().evaluate(el => getComputedStyle(el).backgroundColor), "rgb(239, 246, 255)");
    assert.ok(await page.locator(".ideas-map-table").evaluate(el => {
      const row = el.querySelector('[data-idea-id="two"]').getBoundingClientRect();
      const bounds = el.getBoundingClientRect();
      return el.scrollTop > 0 && row.top >= bounds.top - 1 && row.bottom <= bounds.bottom + 1;
    }), "Hover must reveal the correct row inside the table");
    assert.equal(await page.evaluate(() => scrollY), pageY, "Route hover must not scroll the page away from the pointer");
    await page.locator('.idea-driving-route[data-idea-id="two"]').dispatchEvent("mouseout");
    assert.equal(await page.locator('.idea-driving-route[data-idea-id="two"]').evaluate(el => getComputedStyle(el).stroke), "rgb(37, 99, 235)");
    assert.equal(await rows.locator("tr.is-route-active").count(), 0);

    const secondRoad = routes.a.two.geometry;
    routes.a.two.geometry = [[55, 8], [57, 10], [55.2, 8.2]];
    await refresh();
    await page.waitForFunction(() => testMap.getBounds().contains([57, 10]));
    const allRoadZoom = await page.evaluate(() => testMap.getZoom());
    await page.locator('.idea-driving-route[data-idea-id="one"]').dispatchEvent("click");
    assert.ok(await page.evaluate(() => [[55, 8], [55.05, 8.3], [55.1, 8.1]].every(point => testMap.getBounds().contains(point))),
      "Clicking a road must fit all its real geometry, including detours");
    assert.ok(await page.evaluate(() => testMap.getZoom()) > allRoadZoom, "Click must focus the chosen road, not all routes");
    const focusedView = await page.evaluate(() => [testMap.getCenter().lat, testMap.getCenter().lng, testMap.getZoom()]);
    data.ideas[1].title = "Scheduled idea updated";
    await refresh();
    await waitText(rows, "Scheduled idea updated");
    assert.deepEqual(await page.evaluate(() => [testMap.getCenter().lat, testMap.getCenter().lng, testMap.getZoom()]), focusedView,
      "Cached polling must preserve the clicked route's viewport");
    await page.evaluate(() => testMap.setView([55.1, 8.1], 13, { animate: false }));
    await page.locator(".idea-map-marker").first().click();
    assert.deepEqual(await page.evaluate(() => [testMap.getCenter().lat, testMap.getCenter().lng, testMap.getZoom()]), focusedView,
      "Clicking the blue number must fit exactly the same road as clicking its line");
    await page.evaluate(() => testMap.closePopup());
    const secondLabel = page.locator('.idea-route-label[data-idea-id="two"]');
    await secondLabel.waitFor({ state: "visible" });
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const labelBounds = await secondLabel.boundingBox();
    await secondLabel.hover();
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert.deepEqual(await secondLabel.boundingBox(), labelBounds, "Hover must not move the clicked label away from the pointer");
    assert.deepEqual(await secondLabel.evaluate(el => ({
      hovered: el.matches(":hover"),
      tooltipHovered: el.closest(".idea-route-distance").matches(":hover"),
      activeRow: document.querySelector("tr.is-route-active")?.dataset.ideaId,
      stroke: getComputedStyle(document.querySelector('.idea-driving-route[data-idea-id="two"]')).stroke,
    })), { hovered: true, tooltipHovered: true, activeRow: "two", stroke: "rgb(192, 38, 211)" });
    await secondLabel.click();
    assert.ok(await page.evaluate(() => testMap.getBounds().contains([57, 10])), "Clicking a label must fit its entire route");
    const firstLabel = page.locator('.idea-route-label[data-idea-id="one"]');
    await firstLabel.waitFor({ state: "visible" });
    await firstLabel.focus();
    await firstLabel.press("Enter");
    assert.deepEqual(await page.evaluate(() => [testMap.getCenter().lat, testMap.getCenter().lng, testMap.getZoom()]), focusedView,
      "Keyboard activation of a label must focus the same complete road");
    await secondLabel.waitFor({ state: "visible" });
    await secondLabel.focus();
    await secondLabel.press("Space");
    assert.ok(await page.evaluate(() => testMap.getBounds().contains([57, 10])), "Space must activate route labels too");
    await page.locator('.idea-driving-route[data-idea-id="one"]').focus();
    assert.equal(await rows.locator("tr.is-route-active").getAttribute("data-idea-id"), "one", "Keyboard focus must also highlight the destination");
    await page.locator('.idea-driving-route[data-idea-id="two"]').focus();
    await page.locator('.idea-driving-route[data-idea-id="two"]').press("Enter");
    assert.ok(await page.evaluate(() => testMap.getBounds().contains([57, 10])), "Keyboard activation must fit the entire chosen road");
    routes.a.two.geometry = secondRoad;
    await page.evaluate(() => testMap.closePopup());
    await page.mouse.move(1100, 20);
    await select.selectOption("");
    await select.selectOption("a");
    for (let index = 0; index < 12; index++) {
      const id = `cluster-${index}`;
      data.ideas.push({ id, title: `Clustered idea ${index}`, lat: 55.1, lng: 8.1 });
      routes.a[id] = { ...routes.a.one, distance: `${100 + index}.4 km` };
    }
    await refresh();
    await assertLabels(14);
    assert.ok(await page.locator(".idea-route-guides line").count() > 0, "Shifted labels need distinguishable leader lines");
    await page.locator("#ideas-map").evaluate(el => { el.style.width = "320px"; });
    await page.waitForFunction(() => testMap.getSize().x === 320);
    await assertLabels(14);
    await page.evaluate(() => testMap.setZoom(testMap.getZoom() - 1, { animate: false }));
    await assertLabels(14);
    await page.evaluate(() => testMap.panBy([12, 8], { animate: false }));
    await assertLabels(14);
    for (let index = 12; index < 40; index++) {
      const id = `cluster-${index}`;
      data.ideas.push({ id, title: `Clustered idea ${index}`, lat: 55.1, lng: 8.1 });
      routes.a[id] = { ...routes.a.one, distance: `${100 + index}.4 km` };
    }
    await page.locator("#ideas-map").evaluate(el => { el.style.height = "160px"; });
    await refresh();
    await page.waitForFunction(() => {
      const shown = [...document.querySelectorAll(".idea-route-distance")]
        .filter(el => getComputedStyle(el).visibility === "visible").length;
      return document.querySelectorAll("[data-ideas-map-rows] tr").length === 43 && shown > 0 && shown < 42;
    });
    await page.locator('.idea-driving-route[data-idea-id="cluster-39"]').dispatchEvent("mouseover");
    await page.waitForFunction(() => [...document.querySelectorAll(".idea-route-distance.is-route-active")]
      .some(el => getComputedStyle(el).visibility === "visible" && el.textContent.includes("139.4 km")));
    await assertLabels(await page.locator(".idea-route-distance").evaluateAll(elements =>
      elements.filter(el => getComputedStyle(el).visibility === "visible").length));
    data.ideas.splice(3);
    for (const id of Object.keys(routes.a)) if (id.startsWith("cluster-")) delete routes.a[id];
    await page.locator("#ideas-map").evaluate(el => { el.style.width = "640px"; el.style.height = "400px"; });
    await select.selectOption("");
    await select.selectOption("a");
    await assertLabels(2);
    await waitText(rows, "12.3 km");
    await rows.locator("button").first().click();
    await page.locator(".leaflet-popup-content").waitFor();
    assert.ok((await page.locator(".leaflet-popup-content").innerText()).includes("Saved <script>unsafe()</script>"));
    await select.selectOption("b");
    await waitRoads(2);
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
    await waitRoads(0);
    assert.deepEqual(await lines(), []);
    assert.ok(await page.evaluate(() => testMap.getBounds().contains([55, 8]) && testMap.getBounds().contains([56, 9])));

    gate = new Promise(resolve => { release = resolve; });
    const held = page.waitForRequest(request => request.url().includes("/map?lodging=a"));
    await select.selectOption("a");
    await held;
    await select.selectOption("b");
    await waitRoads(2);
    release();
    gate = undefined;
    assert.equal(await select.inputValue(), "b");
    assert.deepEqual(await lines(), [routes.b.one.geometry, routes.b.two.geometry], "Obsolete origin response must not replace roads");

    const saved = routes.b.one;
    delete routes.b.one;
    data.progress.completed = 3;
    await refresh();
    await waitRoads(1);
    assert.equal((await lines()).length, 1);
    routes.b.one = saved;
    data.progress.completed = 4;
    await waitRoads(2); // Read-only polling discovers completed background work.
    assert.equal((await lines()).length, 2);
    routes.b.one = { status: "unavailable" };
    data.progress_label = "3 saved / 1 failed / 0 pending";
    await refresh();
    await waitText(status, "Route unavailable");
    await waitText(page.locator("[data-ideas-map-cache-status]"), "3 saved / 1 failed / 0 pending");
    assert.equal((await lines()).length, 1, "Failed route must never become a straight-line fallback");
    routes.b.one = saved;
    data.progress_label = "4 saved / 0 failed / 0 pending";
    await waitRoads(2);
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
    const firstRow = rows.locator('tr[data-idea-id="one"]');
    assert.equal(await firstRow.locator("td").count(), 4, "Replace the status column with scheduling");
    assert.equal(await firstRow.locator(".ideas-map-description").innerText(), "A museum with <b>historic boats</b>.");
    assert.equal(await firstRow.locator(".ideas-map-description b").count(), 0, "Descriptions must be plain text");
    assert.equal(await rows.locator('[data-idea-schedule="two"]').inputValue(), "2027-05-02");
    const picker = firstRow.locator("[data-idea-schedule]");
    await picker.focus();
    await page.evaluate(() => { window.preservedPicker = document.activeElement; });
    const beforeEditing = requests;
    await new Promise(resolve => setTimeout(resolve, 3200));
    assert.equal(requests, beforeEditing, "Polling pauses while a day picker is focused");
    scheduleGate = new Promise(resolve => { finishSchedule = resolve; });
    await picker.selectOption("2027-05-01");
    await firstRow.getByText("Saving", { exact: true }).waitFor();
    assert.equal(await picker.isDisabled(), true);
    assert.ok(await page.evaluate(() => window.preservedPicker.isConnected));
    finishSchedule();
    scheduleGate = undefined;
    await page.waitForFunction(() => document.querySelector('[data-idea-schedule="one"]').value === "2027-05-01" &&
      !document.querySelector('[data-idea-schedule="one"]').disabled);
    assert.equal(await rows.locator("tr").count(), 3, "Scheduling reuses the original idea");
    assert.equal(await page.locator('[data-planner-grid][data-day="2027-05-02"] .planner-block').count(), 0);
    assert.equal(await page.locator('[data-planner-grid][data-day="2027-05-01"] .planner-block').count(), 1,
      "The daily time grid moves the existing activity without a page reload");
    failSchedule = true;
    await picker.selectOption("2027-05-02");
    await firstRow.getByRole("alert").waitFor();
    assert.equal(await picker.inputValue(), "2027-05-01", "Failed scheduling restores the saved day");
    assert.equal(await page.locator('[data-planner-grid][data-day="2027-05-01"] .planner-block').count(), 1,
      "A failed save must not move the existing time block");
    assert.deepEqual(schedules, ["2027-05-01", "2027-05-02"]);
    await picker.blur();
    await page.evaluate(() => { document.querySelector("#tab").style.display = "none"; });
    await new Promise(resolve => setTimeout(resolve, 100));
    const before = requests;
    await new Promise(resolve => setTimeout(resolve, 3200));
    assert.equal(requests, before, "Hidden tabs must stop polling (not the independent server worker)");
    assert.deepEqual(errors, []);
    await verifyRouteRetryForm(context);
    console.log("Validated circled labels with saved distance/time, marker/label/road route focus, collision handling and read-only polling.");
  } finally {
    if (release) release();
    if (finishSchedule) finishSchedule();
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

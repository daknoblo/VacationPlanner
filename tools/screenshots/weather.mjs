import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

export async function verifyWeather(browser) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const web = new URL("../../web/static/", import.meta.url);
  const errors = [];
  let failRead = false;
  let failWrite = false;
  let delayed, release;
  const posts = [];
  const entry = { Place: "Hotel <script>unsafe()</script>", Icon: "☀", Compact: "12–20 °C",
    Summary: "Clear · 12–20 °C", Detail: "Rain probability up to 30% · forecast times 09:00–21:00", Notice: "",
    Coverage: "Forecast times 09:00–21:00 (5 intervals)", Metrics: [
      { Label: "Rain probability (max.)", Value: "30%", Hint: "Maximum" },
      { Label: "Rain", Value: "0.2 mm", Hint: "Available intervals" },
      { Label: "Snow", Value: "0.0 mm", Hint: "Available intervals" },
      { Label: "Wind (max.)", Value: "11 km/h", Hint: "Maximum" },
    ] };
  let updated = "Data as of: 02.10.2026 10:00 UTC";
  let entries = [entry];
  let reads = 0;
  page.on("pageerror", error => errors.push(error.message));
  await page.route("**/*", async route => {
    const url = new URL(route.request().url());
    assert.equal(url.origin, "http://127.0.0.1", "Weather UI must never contact the provider");
    if (url.pathname === "/weather.js") return route.fulfill({
      contentType: "text/javascript", body: await readFile(new URL("js/weather.js", web)) });
    if (url.pathname === "/app.css") return route.fulfill({
      contentType: "text/css", body: await readFile(new URL("css/app.css", web)) });
    if (url.pathname === "/forecast") {
      assert.equal(route.request().method(), "GET");
      reads++;
      const json = JSON.stringify({ Updated: updated, Days: [{ Date: "2026-10-02", Label: "02.10.2026", Entries: entries }],
        ByDate: { "2026-10-02": entries } });
      if (delayed) await delayed;
      return route.fulfill({ status: failRead ? 500 : 200, contentType: "application/json", body: json });
    }
    if (url.pathname === "/settings/weather/status") {
      assert.equal(route.request().method(), "GET");
      return route.fulfill({ status: failRead ? 500 : 200, body: "Weather: 1 pending" });
    }
    if (url.pathname.startsWith("/settings/weather")) {
      assert.equal(route.request().method(), "POST");
      assert.equal(route.request().headers()["hx-request"], "true");
      const form = new URLSearchParams(route.request().postData());
      assert.equal(form.get("csrf_token"), "test-token");
      posts.push([url.pathname, Object.fromEntries(form)]);
      return route.fulfill({ status: failWrite ? 422 : url.pathname.endsWith("/refresh") ? 200 : 204,
        body: failWrite ? "API key missing" : url.pathname.endsWith("/refresh") ? "Queued: 1" : "" });
    }
    const settings = url.pathname === "/settings";
    return route.fulfill({ contentType: "text/html", body: `<!doctype html><html><head>
      <meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/app.css">
      </head><body>${settings ? `
      <section data-weather-settings data-error="Status unavailable" data-saved="Saved">
      <form action="/settings/weather" method="post" data-weather-form>
        <input type="hidden" name="csrf_token" value="test-token">
        <select name="interval"><option value="off">Off</option><option value="3">3 hours</option>
        <option value="6">6 hours</option><option value="12">12 hours</option></select>
        <button>Save</button><p data-weather-form-status role="status"></p>
      </form>
      <form action="/settings/weather/refresh" method="post" data-weather-form>
        <input type="hidden" name="csrf_token" value="test-token">
        <select name="vacation_id"><option value="trip">Trip</option></select>
        <button>Refresh</button><p data-weather-form-status role="status"></p>
      </form>
      <p data-weather-settings-status></p></section>` : `
      <section class="panel" data-weather-panel data-weather-url="/forecast" data-error="Status unavailable">
        <div class="weather-heading"><h2>Weather</h2><span class="muted small" data-weather-updated></span></div>
        <p data-weather-error hidden></p><div class="weather-days" data-weather-days></div></section>
      <div class="calendar-weather" data-weather-day="2026-10-02"></div>
      <div class="calendar-weather-week"><span></span><div class="calendar-weather" data-weather-day="2026-10-02"></div></div>
      `}<script src="/weather.js"></script></body></html>` });
  });
  const refresh = () => page.evaluate(() => document.body.dispatchEvent(new CustomEvent("geographyChanged")));
  try {
    await page.goto("http://127.0.0.1/");
    await page.locator(".weather-summary").waitFor();
    assert.equal(await page.locator(".weather-place h4").textContent(), entry.Place);
    assert.equal(await page.locator(".weather-place script").count(), 0, "Place names must be escaped");
    assert.equal(await page.locator("[data-weather-updated]").innerText(), updated);
    assert.deepEqual(await page.locator(".weather-metrics dd").allTextContents(), ["30%", "0.2 mm", "0.0 mm", "11 km/h"]);
    assert.equal(await page.locator(".weather-coverage").innerText(), entry.Coverage);
    assert.ok(!(await page.locator(".weather-place").innerText()).includes("Data as of:"));
    assert.equal(await page.locator("[data-weather-updated]").evaluate(el => getComputedStyle(el).textAlign), "right");
    assert.equal(await page.locator(".calendar-weather__entry").count(), 2, "Both calendars show the same saved forecast");
    assert.ok(await page.locator(".calendar-weather__entry").evaluateAll(nodes =>
      nodes.every(node => node.title.includes("forecast times"))), "Compact summaries expose full details");
    entries = [entry, { ...entry, Place: "Destination fallback: Rome", Notice: "Saved forecast is old" }];
    await refresh();
    await page.waitForFunction(() => document.querySelectorAll(".weather-place").length === 2);
    assert.equal(await page.locator(".calendar-weather__entry").count(), 4, "Transfer days show both places");
    await page.setViewportSize({ width: 390, height: 800 });
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
      "Weather cards must fit mobile width");
    delayed = new Promise(resolve => { release = resolve; });
    const oldReads = reads;
    await refresh();
    while (reads === oldReads) await new Promise(resolve => setTimeout(resolve, 10));
    entries = [{ ...entry, Summary: "Rain · 10–15 °C" }];
    updated = "Data as of: 02.10.2026 11:00 UTC";
    delayed = undefined;
    await refresh();
    await page.getByText("☀ Rain · 10–15 °C", { exact: true }).waitFor();
    release();
    await page.waitForTimeout(100);
    assert.equal(await page.locator(".weather-place").count(), 1, "Stale responses must not overwrite new data");
    assert.equal(await page.locator("[data-weather-updated]").innerText(), updated, "Stale responses must not revert the timestamp");
    failRead = true;
    await refresh();
    await page.locator("[data-weather-error]").getByText("Status unavailable").waitFor();
    assert.equal(await page.locator(".calendar-weather.is-unavailable").count(), 2);
    assert.equal(await page.locator(".weather-place").count(), 1, "Read failures retain prior tab contents with an explicit error");
    failRead = false;
    await refresh();
    await page.waitForFunction(() => !document.querySelector(".calendar-weather.is-unavailable"));
    await page.goto("http://127.0.0.1/settings");
    const interval = page.locator('[name="interval"]');
    for (const value of ["3", "6", "12", "off"]) {
      await interval.selectOption(value);
      await page.getByRole("button", { name: "Save", exact: true }).click();
      await page.locator("[data-weather-form-status]").first().getByText("Saved", { exact: true }).waitFor();
      assert.equal(posts.at(-1)[1].interval, value);
    }
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await page.getByText("Queued: 1", { exact: true }).waitFor();
    assert.equal(posts.at(-1)[1].vacation_id, "trip");
    failWrite = true;
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await page.getByRole("alert").getByText("API key missing", { exact: true }).waitFor();
    assert.equal(await page.locator('[name="vacation_id"]').inputValue(), "trip");
    failRead = true;
    await refresh();
    await page.locator("[data-weather-settings-status]").getByText("Status unavailable").waitFor();
    assert.deepEqual(errors, []);
    console.log("Validated weather display, both calendars, transfer days, stale/error handling and Settings-only refresh.");
  } finally {
    if (release) release();
    await context.close();
  }
}

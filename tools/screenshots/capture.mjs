import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { mkdir, readFile, realpath, stat, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { extname, join, relative, resolve, sep } from "node:path";
import { once } from "node:events";
import { chromium } from "playwright";
import { verifyCalendarRegionUpdates } from "./calendar-regions.mjs";
import { verifyAISearchCenters } from "./ai-centers.mjs";
import { verifyIdeasMap } from "./ideas-map.mjs";

const options = {};
for (const argument of process.argv.slice(2)) {
  const match = /^--(site|revision|channel)=(.+)$/.exec(argument);
  if (!match) throw new Error(`Unknown argument: ${argument}`);
  options[match[1]] = match[2];
}
const root = await realpath(resolve(options.site ?? "dist"));
const metadata = JSON.parse(await readFile(join(root, "manifest.json"), "utf8"));
const output = join(root, "screenshots");
const prefix = "/VacationPlanner/";
const failures = [];
const mimeTypes = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css",
  ".js": "text/javascript",
  ".json": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".woff2": "font/woff2",
};

// Serve under a project subpath, not "/", to catch broken GitHub Pages links.
const server = createServer((request, response) => {
  serve(request, response).catch(error => {
    failures.push(`Static server: ${error.message}`);
    response.writeHead(500).end();
  });
});
async function serve(request, response) {
  if (!["GET", "HEAD"].includes(request.method)) {
    response.writeHead(405).end();
    return;
  }
  const pathname = decodeURIComponent(new URL(request.url, "http://localhost").pathname);
  if (!pathname.startsWith(prefix)) {
    response.writeHead(404).end();
    return;
  }
  const name = pathname.slice(prefix.length);
  const target = resolve(root, name.endsWith("/") || name === "" ? `${name}index.html` : name);
  if (target !== root && !target.startsWith(root + sep)) {
    response.writeHead(403).end();
    return;
  }
  let actual;
  try {
    actual = await realpath(target);
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    response.writeHead(404).end();
    return;
  }
  if (!actual.startsWith(root + sep) || !(await stat(actual)).isFile()) {
    response.writeHead(403).end();
    return;
  }
  response.writeHead(200, { "Content-Type": mimeTypes[extname(actual)] ?? "application/octet-stream" });
  if (request.method === "HEAD") response.end();
  else createReadStream(actual).on("error", error => response.destroy(error)).pipe(response);
}

server.listen(0, "127.0.0.1");
await once(server, "listening");
const origin = `http://127.0.0.1:${server.address().port}`;
const base = origin + prefix;
let browser;
const screenshots = [];
const desktop = { width: 1440, height: 1000 };
const shots = [
  { name: "dashboard", page: "index.html", title: "Planned and archived vacations" },
  { name: "overview", tab: "overview", title: "Accommodation-only overview map" },
  { name: "travel", tab: "travel", title: "Arrival and departure bookings" },
  { name: "accommodation", tab: "lodging", title: "All accommodation records" },
  { name: "day-planner", tab: "tagesplan", view: "day", title: "Day planner and route summary" },
  { name: "week-planner", tab: "tagesplan", view: "week", title: "Week planner and regional ideas" },
  { name: "ideas", tab: "ideen", title: "Ideas and saved reference links" },
  { name: "budget", tab: "budget", title: "Budget derived from original bookings" },
  { name: "cheatsheet", tab: "cheatsheet", title: "Standard and custom travel vocabulary" },
  { name: "settings", page: "settings.html", title: "Settings and Foundry deployment selection" },
  { name: "about", page: "about.html", title: "Application information" },
  { name: "mobile", tab: "overview", mobile: true, title: "Responsive trip overview" },
];

try {
  browser = await chromium.launch(
    process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH }
      : { channel: options.channel ?? process.env.PLAYWRIGHT_CHANNEL ?? undefined },
  );
  const context = await browser.newContext({
    viewport: desktop,
    deviceScaleFactor: 1,
    timezoneId: "Europe/Rome",
    colorScheme: "light",
    reducedMotion: "reduce",
    serviceWorkers: "block",
  });
  await context.route("**/*", async route => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== origin || !["GET", "HEAD"].includes(request.method())) {
      failures.push(`Unexpected network request: ${request.method()} ${request.url()}`);
      await route.abort("blockedbyclient");
    } else {
      await route.continue();
    }
  });
  const page = await context.newPage();
  page.on("pageerror", error => failures.push(error.message));
  page.on("response", response => {
    if (response.status() >= 400) failures.push(`HTTP ${response.status()}: ${response.url()}`);
  });
  page.on("requestfailed", request => failures.push(`Failed request: ${request.url()}`));

  for (const language of ["en", "de"]) {
    await mkdir(join(output, language), { recursive: true });
    for (const shot of shots) {
      await page.setViewportSize(shot.mobile ? { width: 390, height: 844 } : desktop);
      const path = `demo/${language}/${shot.page ?? "vacation.html"}`;
      const response = await page.goto(base + path, { waitUntil: "networkidle" });
      assert.equal(response.status(), 200, path);
      assert.equal(await page.locator("html").getAttribute("lang"), language);
      if (shot.tab) {
        await page.locator(`.tabs__row--main [data-tab="${shot.tab}"]`).click();
        await page.locator(`[data-tab-panel="${shot.tab}"].is-active`).waitFor();
      }
      if (shot.view) {
        await page.locator(`[data-view="${shot.view}"]`).click();
        await page.locator(shot.view === "day" ? "[data-day-view]" : "[data-weekview]").waitFor();
      }
      await verifyView(page, shot);
      await page.evaluate(() => document.fonts.ready);
      const file = join(output, language, `${shot.name}.png`);
      await page.screenshot({ path: file, fullPage: !shot.mobile, animations: "disabled" });
      const data = await readFile(file);
      assert.equal(data.subarray(1, 4).toString(), "PNG", file);
      screenshots.push({
        language, name: shot.name, title: shot.title,
        file: relative(root, file).split(sep).join("/"),
        page: path, tab: shot.tab, view: shot.view,
        sha256: createHash("sha256").update(data).digest("hex"),
        bytes: data.length,
      });
      console.log(`Captured ${language}/${shot.name}`);
    }
    await verifyLinks(page, `demo/${language}/index.html`);
    await verifyLinks(page, `demo/${language}/vacation.html`);
    await verifyLinks(page, `demo/${language}/settings.html`);
    await verifyLinks(page, `demo/${language}/about.html`);
    await verifyLinks(page, `demo/${language}/export.html`);
  }
  // Gallery images are generated above, before checking documentation/site links.
  await verifyLinks(page, "index.html");
  await verifyLinks(page, "docs.html");
  await verifyCalendarRegionUpdates(browser);
  await verifyAISearchCenters(browser);
  await verifyIdeasMap(browser);
  assert.deepEqual(failures, [], "The demo must work without failed requests or external services");
  await writeFile(join(output, "manifest.json"), JSON.stringify({
    version: metadata.version,
    revision: options.revision ?? "local",
    generatedAt: new Date().toISOString(),
    browser: browser.version(),
    screenshots,
  }, null, 2) + "\n");
  console.log(`Validated both locales and wrote ${screenshots.length} screenshots with SHA-256 manifest.`);
} finally {
  if (browser) await browser.close();
  await new Promise((done, reject) => server.close(error => error ? reject(error) : done()));
}

async function verifyView(page, shot) {
  await verifyHeader(page);
  assert.equal(await page.locator("script[src*='htmx'], script[src*='/js/app.js']").count(), 0);
  assert.equal(await page.locator("form").count(), 0, "The demo must not submit forms");
  const unsafe = await page.locator(
    "input:not([type=hidden]):not([data-ai-radius]), textarea, select:not([data-ideas-region-filter]):not([data-ideas-map-origin]):not([data-ai-center]), " +
    "button:not([data-tab]):not([data-view]):not([data-goto-day]):not([data-payer-filter]):not([data-print]):not([data-ideas-sort]):not(.ideas-map-link):not(.idea-route-label)",
  ).evaluateAll(
    elements => elements.filter(element => !element.disabled && !element.readOnly).map(element => element.outerHTML),
  );
  assert.deepEqual(unsafe, [], "Mutating controls must be read-only or disabled");
  if (["overview", "mobile"].includes(shot.name)) {
    await page.locator("#map.leaflet-container").waitFor();
    assert.equal(await page.locator("#map .leaflet-marker-icon").count(), 3, "All three lodgings, no ideas");
    const marker = page.locator("#map .leaflet-marker-icon").first();
    const title = await marker.getAttribute("title");
    const dates = title.match(/\d{2}\.\d{2}\.\d{4} – \d{2}\.\d{2}\.\d{4}/);
    assert.ok(dates, "Accommodation hover title contains the booked date range");
    await marker.click();
    assert.ok((await page.locator(".leaflet-popup-content").innerText()).includes(dates[0]),
      "The accommodation popup contains the same date range");
    await page.locator(".leaflet-popup-close-button").click();
  }

  if (shot.name === "cheatsheet") {
    assert.equal(await page.locator(".cheatsheet-table").count(), 1);
    assert.equal(await page.locator("#cheatsheet-rows tr").count(), 26, "22 standard, two participant and two custom phrases");
    assert.equal(await page.locator("#cheatsheet-rows [data-introduction]").count(), 2);
    assert.ok((await page.locator("#cheatsheet-rows [data-introduction]").allTextContents()).every(
      text => text.includes("Mi chiamo") && !text.includes("{name}"),
    ), "Participants receive complete self-introductions rather than placeholders");
  }
  if (shot.name === "day-planner") {
    assert.ok(await page.locator("[data-day-view] .day-journey__stop:visible").count() > 0,
      "The route must contain actual planned stops, not a loading placeholder");
    assert.equal((await page.locator("[data-region-day]:visible").innerText()).trim(), "Toscana",
      "Day view shows the booked accommodation region");
    const transfer = await page.locator("[data-region-day]").allTextContents();
    assert.ok(transfer.some(text => text.includes("Toscana · Lazio")),
      "Transfer day contains both accommodation regions");
  }
  if (["day-planner", "week-planner"].includes(shot.name)) {
    const scope = page.locator(shot.view === "day" ? "[data-day-view]" : "[data-weekview]");
    if (shot.view === "week") {
      const bands = await scope.locator("[data-region-week] .calendar-region-band").allTextContents();
      assert.ok(bands.some(text => text.includes("Toscana · Lazio")), "Week band includes the transfer");
      assert.ok(await scope.locator("[data-region-week] .calendar-region-band").evaluateAll(
        elements => elements.some(element => /span [2-7]/.test(element.style.gridColumn)),
      ), "Consecutive days in the same region share one horizontal band");
    }
    const counts = await scope.locator("[data-day-count]").allTextContents();
    assert.ok(counts.some(count => /\([1-9]\d*\)/.test(count)), "Planner shows activity counts");
    const filter = scope.locator("[data-ideas-region-filter]");
    assert.ok(await filter.locator("option").count() >= 3, "Ideas cover at least two regions");
    const region = await filter.locator("option").nth(1).getAttribute("value");
    await filter.selectOption(region);
    assert.deepEqual(await page.locator("[data-ideas-region-filter]").evaluateAll(
      elements => elements.map(element => element.value),
    ), [region, region], "Day and week regional filters stay synchronized");
    const visible = await scope.locator("[data-ideas-region-group]:visible").evaluateAll(
      elements => elements.map(element => element.dataset.ideasRegionGroup),
    );
    assert.ok(visible.includes(region), "Selected regional ideas remain visible");
    assert.ok(visible.every(value => value === region || value === "region:"), "Other regions are hidden");
    await filter.selectOption("*");
  }
  if (shot.name === "budget") {
    assert.ok(await page.locator(".budget__summary .budget__stat").count() >= 3);
    assert.ok(await page.locator(".budget-notice__bookings a").count() > 0,
      "Unassigned bookings retain their original-entry links");
    await page.locator(".budget-notice__bookings a").first().click();
    await page.locator('[data-tab-panel="lodging"].is-active').waitFor();
    await page.locator('.tabs__row--main [data-tab="budget"]').click();
  }
  if (shot.name === "settings") {
    assert.ok(await page.locator("#ai-deployment option").count() >= 2,
      "The current Foundry deployment chooser is rendered");
    assert.equal(await page.locator("#foundry-settings-panel input[type=checkbox]").count(), 0);
    assert.ok(await page.locator("#route-retry-vacation option").count() > 1);
    assert.equal(await page.locator("#route-retry-vacation").isDisabled(), true, "Static demo must not retry routes");
  }
  if (shot.name === "ideas") {
    assert.ok(await page.locator("#ideen-list .item-row__thumb").evaluateAll(images => images.every(image => {
      const bounds = image.getBoundingClientRect(), row = image.closest(".item-row").getBoundingClientRect();
      return Math.abs(row.right - bounds.right - 1) < 1 &&
        Math.abs((row.top + row.bottom) / 2 - (bounds.top + bounds.bottom) / 2) < 1 &&
        bounds.width > 120 && Math.abs(bounds.width / bounds.height - 4 / 3) < 0.02;
    })), "Images stay at the right edge, vertically centered and larger where space allows");
    const thumbnailWidth = (await page.locator("#ideen-list .item-row__thumb").first().boundingBox()).width;
    await page.setViewportSize({ width: 1920, height: desktop.height });
    const wideThumbnail = (await page.locator("#ideen-list .item-row__thumb").first().boundingBox()).width;
    assert.ok(wideThumbnail > thumbnailWidth && wideThumbnail <= 220, "Thumbnails grow with available space up to their cap");
    await page.setViewportSize(desktop);
    assert.ok(await page.locator("#ideen-list").evaluate(list => {
      const rows = [...list.children].map(row => row.getBoundingClientRect());
      return rows.length >= 3 && rows[0].top === rows[1].top &&
        rows[0].right < rows[1].left && rows[2].top >= rows[0].bottom;
    }), "Exactly two saved idea cards share each desktop row");
    await page.locator("#ideen-list > li").first().evaluate(row => row.classList.add("is-editing"));
    assert.ok(await page.locator("#ideen-list").evaluate(list =>
      Math.abs(list.firstElementChild.getBoundingClientRect().width - list.getBoundingClientRect().width) < 1),
    "Inline editors span both desktop columns");
    await page.locator("#ideen-list > li").first().evaluate(row => row.classList.remove("is-editing"));
    assert.ok(await page.locator(".idea-location-warning").count() > 0, "Unlocated ideas show a location warning");
    await page.locator("#ideas-map.leaflet-container").waitFor();
    assert.equal(await page.locator("#ideas-map .lodging-marker").count(), 3);
    assert.equal(await page.locator("#ideas-map .idea-map-marker").count(), 14);
    assert.equal(await page.locator("[data-ideas-map-rows] tr").count(), 15);
    assert.equal(await page.locator("[data-ideas-map-origin] option").count(), 4);
    assert.equal(await page.locator("[data-ideas-map-origin]").inputValue(), "", "Overview is the default");
    assert.equal(await page.locator(".ideas-map-start").count(), 14, "Overview includes a saved origin per located idea");
    assert.ok(await page.locator("[data-ideas-map-rows] tr").first().locator("td").nth(1).textContent() !== "—");
    assert.ok(await page.locator("[data-ideas-map-rows] tr").first().evaluate(row => {
      const cells = [...row.cells].slice(1);
      const picker = row.querySelector("select").getBoundingClientRect(), box = row.getBoundingClientRect();
      return cells.every(cell => getComputedStyle(cell).textAlign === "center" &&
        getComputedStyle(cell).verticalAlign === "middle") &&
        Math.abs((picker.top + picker.bottom) / 2 - (box.top + box.bottom) / 2) < 1;
    }), "Metrics and day selectors are centered horizontally and vertically");
    await page.locator('[data-ideas-sort="distanceM"]').click();
    await page.locator('[data-ideas-sort="distanceM"]').click();
    assert.equal(await page.locator('[data-ideas-sort="distanceM"]').locator("..").getAttribute("aria-sort"), "descending");
    await page.locator('[data-ideas-sort="distanceM"]').click();
    assert.equal(await page.locator("[data-ideas-map-refresh], [data-ideas-map-retry]").count(), 0, "Map controls must not contain refresh or retry buttons");
    assert.equal(await page.locator("[data-ideas-map-status]").isVisible(), false, "No redundant map success paragraph");
    assert.ok(await page.locator(".ideas-map-heading").evaluate(el => {
      const heading = el.querySelector("h2");
      const totals = el.querySelector("[data-ideas-map-cache-status]");
      const a = heading.getBoundingClientRect(), b = totals.getBoundingClientRect();
      return !!totals.textContent && a.right < b.left && b.top < a.bottom &&
        getComputedStyle(totals).textAlign === "right" &&
        parseFloat(getComputedStyle(totals).fontSize) < parseFloat(getComputedStyle(heading).fontSize);
    }), "Small right-aligned route totals share the heading row");
    assert.equal(await page.locator("[data-ideas-map-rows] tr").first().locator("td").count(), 4);
    assert.ok(await page.locator(".ideas-map-description").count() > 0);
    assert.equal(await page.locator("[data-idea-schedule]").count(), 15);
    const dayChoices = await page.locator("[data-idea-schedule]").first().locator("option").evaluateAll(options =>
      options.filter(option => option.value).map(option => ({ value: option.value, label: option.textContent })));
    assert.equal(dayChoices.length, 7);
    for (let i = 0; i < dayChoices.length; i++) {
      const region = i < 5 ? "Toscana" : i === 5 ? "Toscana · Lazio" : "Lazio";
      assert.equal(dayChoices[i].label, `${region} · ${dayChoices[i].value.split("-").reverse().join(".")}`,
        "Day options prefix dates with the same inclusive accommodation regions as the calendar");
    }
    assert.ok(await page.locator("[data-idea-schedule]").evaluateAll(elements => elements.every(el => el.disabled)),
      "The static demo must never schedule activities");
    assert.ok(await page.locator(".ideas-map-controls").evaluate(el => {
      const label = el.querySelector("label").getBoundingClientRect();
      const select = el.querySelector("select").getBoundingClientRect();
      return label.right <= select.left && label.top < select.bottom && label.bottom > select.top;
    }), "Starting-accommodation label and dropdown share a line");
    const origin = await page.locator("[data-ideas-map-origin] option").nth(1).getAttribute("value");
    await page.locator("[data-ideas-map-origin]").selectOption(origin);
    assert.equal(await page.locator("#ideas-map .idea-driving-route").count(), 14,
      "Selected accommodation displays every illustrative route in the offline demo");
    assert.equal(await page.locator("#ideas-map .idea-route-distance").count(), 14, "Every saved road has a distance label");
    await page.waitForFunction(() => [...document.querySelectorAll("#ideas-map .idea-route-distance")]
      .filter(el => getComputedStyle(el).visibility === "visible").length === 14);
    assert.ok(await page.locator("#ideas-map .idea-route-distance").evaluateAll(elements => {
      const bounds = elements.map(el => el.getBoundingClientRect());
      return bounds.every((a, i) => bounds.slice(i + 1).every(b =>
        a.right <= b.left || a.left >= b.right || a.bottom <= b.top || a.top >= b.bottom));
    }), "The actual demo must show all 14 distance labels without overlap");
    await page.setViewportSize({ width: 390, height: 844 });
    assert.ok(await page.locator("#ideen-list").evaluate(list => {
      const rows = [...list.children].map(row => row.getBoundingClientRect());
      return rows.every(row => Math.abs(row.width - list.getBoundingClientRect().width) < 1) &&
        rows[1].top >= rows[0].bottom;
    }), "Mobile ideas use one full-width card per row");
    const title = page.locator("#ideen-list .item-row__title strong").first();
    const originalTitle = await title.textContent();
    await title.evaluate(el => { el.textContent = "VeryLongLandmarkName".repeat(10); });
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
      "Ideas map and route table must fit the mobile viewport");
    await title.evaluate((el, text) => { el.textContent = text; }, originalTitle);
    await page.setViewportSize(desktop);
    assert.equal(await page.locator("#ai-center").inputValue(), "destination");
    assert.equal(await page.locator("#ai-center option").count(), 7, "Destination, two regions, three accommodations and custom point");
    const labels = await page.locator("#ai-center option").allTextContents();
    assert.ok(labels.includes("Toscana") && labels.includes("Lazio"), "Each accommodation region appears once");
    assert.equal(await page.locator("[data-ai-search-map] .ai-search-lodging").count(), 3);
    const accommodation = await page.locator('[data-ai-center] option[value^="lodging:"]').first().getAttribute("value");
    await page.locator(`[data-ai-lodging="${accommodation}"]`).click();
    assert.equal(await page.locator("[data-ai-center]").inputValue(), accommodation,
      "The offline AI map selects accommodations without network calls");
    await page.locator("[data-ai-center]").selectOption("destination");
  }
  if (shot.name === "day-planner" || shot.name === "week-planner") {
    assert.equal(await page.locator('[data-tab-panel="tagesplan"] [data-geography-refresh], [data-planner-geography-status], .planner-region-tools').count(), 0,
      "The planner must not retain the moved refresh control or old hint block");
  }
  if (shot.mobile) {
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    if (overflow > 1) {
      console.error("Mobile layout overflow:", await page.evaluate(() =>
        [...document.querySelectorAll("body *")].filter(element => {
          const bounds = element.getBoundingClientRect();
          return bounds.width > 0 && bounds.right > innerWidth + 1 && !element.closest(".tabs__row");
        }).slice(0,20).map(element => ({
          tag: element.tagName, class: element.className,
          right: element.getBoundingClientRect().right, text: element.textContent.trim().slice(0,100),
        })),
      ));
    }
    assert.ok(overflow <= 1, `Mobile page overflows by ${overflow}px`);
  }
}

async function verifyHeader(page) {
  assert.match(await page.title(), /^Vacationplanner · /);
  assert.equal(await page.locator(".brand span").last().textContent(), "Vacation Planner");
  const layout = await page.evaluate(() => {
    const rect = selector => document.querySelector(selector).getBoundingClientRect();
    const inner = rect(".topbar__inner"), main = rect("main.container");
    const brand = rect(".brand"), status = rect("#background-status"), nav = rect(".topbar__nav");
    const header = document.querySelector(".topbar");
    return {
      width: innerWidth, inner: { x: inner.x, width: inner.width },
      main: { x: main.x, width: main.width },
      left: brand.left - inner.left, right: inner.right - status.right,
      gap: status.left - nav.right,
      overflow: header.scrollWidth - header.clientWidth,
      overlap: brand.right > status.left && brand.top < status.bottom && brand.bottom > status.top,
    };
  });
  assert.deepEqual(layout.inner, layout.main, "Every page and its header share the same centered width");
  assert.ok(Math.abs(layout.inner.width - layout.width * (layout.width >= 1000 ? 0.75 : 1)) < 1);
  assert.ok(Math.abs(layout.left - 20) < 1 && Math.abs(layout.right - 20) < 1,
    "Brand and background status align with the left and right content edges");
  assert.ok(layout.overflow <= 1 && !layout.overlap,
    `Header must neither overflow nor overlap at ${layout.width}px on ${page.url()}`);
  if (layout.width >= 1440) {
    assert.ok(layout.gap >= 37.8 && layout.gap <= 75.6, "Menu/status gap stays within roughly 1–2 CSS centimeters");
  }
  const spinner = page.locator(".background-status__spinner");
  if (await spinner.count()) {
    assert.equal(await spinner.getAttribute("aria-hidden"), "true");
    assert.equal(await spinner.evaluate(el => getComputedStyle(el).animationName), "none");
    await page.emulateMedia({ reducedMotion: "no-preference" });
    assert.ok(await spinner.evaluate(el => {
      const style = getComputedStyle(el);
      return style.width === style.height && style.borderRadius === "50%" &&
        style.animationName === "background-status-spin" &&
        el.getAnimations().some(animation => animation.playState === "running");
    }), "Active jobs have a running circular animation");
    await page.emulateMedia({ reducedMotion: "reduce" });
    assert.equal(await spinner.evaluate(el => getComputedStyle(el).animationName), "none",
      "Reduced-motion preferences disable spinning without hiding progress");
  }
}

async function verifyLinks(page, path) {
  const response = await page.goto(base + path, { waitUntil: "networkidle" });
  assert.equal(response.status(), 200, path);
  if (path.startsWith("demo/")) {
    for (const width of [390, 768, 1000, 1101, 1280, 1440, 1920, 2560]) {
      await page.setViewportSize({ width, height: 900 });
      await verifyHeader(page);
    }
    await page.setViewportSize(desktop);
  }
  const links = await page.locator("a[href], img[src], script[src], link[href]").evaluateAll(
    elements => elements.map(element => element.href || element.src),
  );
  for (const href of new Set(links)) {
    const url = new URL(href);
    if (url.origin !== origin) continue;
    assert.ok(url.pathname.startsWith(prefix), `Link escapes Pages subpath: ${href}`);
    const response = await page.request.head(href);
    assert.equal(response.status(), 200, `Broken internal link from ${path}: ${href}`);
  }
}

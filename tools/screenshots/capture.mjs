import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { mkdir, readFile, realpath, stat, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { extname, join, relative, resolve, sep } from "node:path";
import { once } from "node:events";
import { chromium } from "playwright";

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
  assert.equal(await page.locator("script[src*='htmx'], script[src*='/js/app.js']").count(), 0);
  assert.equal(await page.locator("form").count(), 0, "The demo must not submit forms");
  const unsafe = await page.locator(
    "input:not([type=hidden]), textarea, select:not([data-ideas-region-filter]), " +
    "button:not([data-tab]):not([data-view]):not([data-goto-day]):not([data-payer-filter]):not([data-print])",
  ).evaluateAll(
    elements => elements.filter(element => !element.disabled && !element.readOnly).map(element => element.outerHTML),
  );
  assert.deepEqual(unsafe, [], "Mutating controls must be read-only or disabled");
  if (["overview", "mobile"].includes(shot.name)) {
    await page.locator("#map.leaflet-container").waitFor();
    assert.equal(await page.locator("#map .leaflet-marker-icon").count(), 3, "All three lodgings, no ideas");
  }
  if (shot.name === "cheatsheet") {
    assert.equal(await page.locator(".cheatsheet-table").count(), 1);
    assert.ok(await page.locator("#cheatsheet-rows tr").count() >= 24, "22 standard and two custom phrases");
  }
  if (shot.name === "day-planner") {
    assert.ok(await page.locator("[data-day-view] .day-journey__stop:visible").count() > 0,
      "The route must contain actual planned stops, not a loading placeholder");
  }
  if (["day-planner", "week-planner"].includes(shot.name)) {
    const scope = page.locator(shot.view === "day" ? "[data-day-view]" : "[data-weekview]");
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

async function verifyLinks(page, path) {
  const response = await page.goto(base + path, { waitUntil: "networkidle" });
  assert.equal(response.status(), 200, path);
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

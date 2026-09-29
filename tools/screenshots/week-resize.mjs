import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

export async function verifyWeekResize(browser) {
  const context = await browser.newContext({ viewport: { width: 1000, height: 1100 } });
  const page = await context.newPage();
  const [script, css] = await Promise.all([
    readFile(new URL("../../web/static/js/app.js", import.meta.url), "utf8"),
    readFile(new URL("../../web/static/css/app.css", import.meta.url), "utf8"),
  ]);
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  const id = "11111111-1111-4111-8111-111111111111";
  const days = ["2026-10-09", "2026-10-10"];
  let state = { day: days[0], start: 540, end: 600 };
  let calls = 0, failure = "", release;
  let gate;
  const px = min => min <= 360 ? Math.round(min * 16 / 60) : 96 + Math.round((min - 360) * 40 / 60);
  const minute = label => { const [h, m] = label.split(":").map(Number); return h * 60 + m; };
  const daily = () => `<div class="planner-block" data-id="${id}" data-start="${state.start}" data-end="${state.end}">Museum</div>`;
  const handles = `<button type="button" class="weekcal-block__resize weekcal-block__resize--start" data-week-resize="start" aria-label="Change start"></button>
    <button type="button" class="weekcal-block__resize weekcal-block__resize--end" data-week-resize="end" aria-label="Change end"></button>`;
  const blockHTML = () => `<div class="weekcal-block weekcal-block--move" data-id="${id}" data-start="${state.start}" data-end="${state.end}"
    style="top:${px(state.start)}px;height:${px(state.end) - px(state.start)}px">
    <span class="weekcal-block__time"></span><span class="weekcal-block__title">Museum</span>${handles}</div>`;
  await context.addCookies([{ name: "csrf_token", value: "weekly-token", url: "http://weekly.test" }]);
  await page.route("**/*", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/app.js") return route.fulfill({ contentType: "text/javascript", body: script });
    if (path === "/app.css") return route.fulfill({ contentType: "text/css", body: css });
    if (path === `/items/${id}/schedule`) {
      calls++;
      assert.equal(route.request().headers()["x-csrf-token"], "weekly-token");
      const form = new URLSearchParams(route.request().postData());
      if (gate) await gate;
      if (failure === "network") return route.abort("failed");
      if (failure === "http") return route.fulfill({ status: 500, body: "Unable to save" });
      state = { day: form.get("day"), start: minute(form.get("start")), end: minute(form.get("end")) };
      return route.fulfill({ contentType: "text/html", body: daily() });
    }
    assert.equal(path, "/");
    return route.fulfill({ contentType: "text/html", body: `<!doctype html><html><head><link rel="stylesheet" href="/app.css"></head><body>
      <div class="weekcal" data-resize-start="Change start" data-resize-end="Change end" data-saving="Saving" data-save-error="Save failed; restored">
      <p data-week-save-status role="status" hidden></p>
      <div style="display:flex;margin:30px;width:600px">
      ${days.map(day => `<div class="weekcal__col" data-weekcol data-day="${day}" style="width:300px;height:816px;flex:none">
        ${day === state.day ? blockHTML() : ""}
        <div class="weekcal-block weekcal-block--travel" style="top:700px;height:40px">Train</div></div>`).join("")}
      </div></div>
      ${days.map(day => `<div hidden data-planner-grid data-day="${day}">${day === state.day ? daily() : ""}</div>`).join("")}
      <script src="/app.js"></script></body></html>` });
  });
  const block = page.locator(`.weekcal-block[data-id="${id}"]`);
  async function load(start, end, day = days[0]) {
    state = { day, start, end };
    await page.goto("http://weekly.test");
    await page.waitForFunction(() => document.readyState === "complete");
  }
  async function begin(edge) {
    const box = await block.locator(`[data-week-resize="${edge}"]`).boundingBox();
    const point = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
    await page.mouse.move(point.x, point.y);
    await page.mouse.down();
    return point;
  }
  async function drag(edge, target, { cancel = false, x = undefined, wait = true } = {}) {
    const before = { ...state };
    const point = await begin(edge);
    await page.mouse.move(x ?? point.x, point.y + px(target) - px(before[edge]), { steps: 4 });
    if (cancel) await page.keyboard.press("Escape");
    await page.mouse.up();
    if (wait) await page.waitForFunction(() => !document.querySelector(".weekcal-block.is-saving"));
  }
  async function check(start, end, day = days[0]) {
    assert.deepEqual(state, { start, end, day });
    assert.equal(await block.getAttribute("data-start"), String(start));
    assert.equal(await block.getAttribute("data-end"), String(end));
    assert.equal(await block.locator("..").getAttribute("data-day"), day);
    assert.equal(await page.locator(`[data-planner-grid][data-day="${day}"] .planner-block`).getAttribute("data-start"), String(start));
    assert.equal(await page.locator(`[data-planner-grid][data-day="${day}"] .planner-block`).getAttribute("data-end"), String(end));
  }
  try {
    await load(540, 600);
    await drag("start", 480);
    await check(480, 600);
    await drag("end", 720, { x: 520 });
    await check(480, 720); // Resizing never moves into another day.
    await drag("start", 570);
    await check(570, 720);
    await drag("end", 630);
    await check(570, 630);
    await page.reload();
    await check(570, 630);
    await drag("end", 580);
    await check(570, 600); // Same minimum duration as the activity editor.
    await load(330, 420);
    await drag("start", 390);
    await check(390, 420);
    await drag("start", 300);
    await check(300, 420); // Both directions through the compressed 06:00 boundary.
    await load(60, 120);
    await drag("start", 0);
    await check(0, 120);
    await load(1380, 1410);
    await drag("end", 1440);
    await check(1380, 1440);
    await load(540, 600);
    let beforeCalls = calls;
    await begin("start");
    await page.mouse.up();
    assert.equal(calls, beforeCalls);
    await drag("end", 660, { cancel: true });
    await check(540, 600);
    assert.equal(calls, beforeCalls);
    const cancelPoint = await begin("end");
    await page.mouse.move(cancelPoint.x, cancelPoint.y + 40, { steps: 4 });
    assert.equal(await block.getAttribute("data-end"), "660");
    await block.dispatchEvent("pointermove", { pointerId: 99, isPrimary: false, clientX: 500, clientY: 400 });
    assert.equal(await block.getAttribute("data-end"), "660");
    await block.dispatchEvent("pointercancel", { pointerId: 1, isPrimary: true });
    await page.mouse.up();
    await check(540, 600);
    assert.equal(calls, beforeCalls);
    const returnPoint = await begin("end");
    await page.mouse.move(returnPoint.x, returnPoint.y + 40, { steps: 4 });
    await page.mouse.move(returnPoint.x, returnPoint.y, { steps: 4 });
    await page.mouse.up();
    await check(540, 600);
    assert.equal(calls, beforeCalls);
    for (const reason of ["http", "network"]) {
      failure = reason;
      await drag("end", 660);
      await check(540, 600);
      assert.equal(await page.locator("[data-week-save-status]").textContent(), "Save failed; restored");
      assert.equal(await page.locator("[data-week-save-status]").getAttribute("role"), "alert");
    }
    failure = "";
    gate = new Promise(resolve => { release = resolve; });
    beforeCalls = calls;
    await drag("end", 660, { wait: false });
    await page.waitForFunction(() => !!document.querySelector(".weekcal-block.is-saving"));
    await block.locator('[data-week-resize="start"]').press("ArrowUp");
    assert.equal(calls, beforeCalls + 1);
    release();
    gate = undefined;
    await page.waitForFunction(() => !document.querySelector(".weekcal-block.is-saving"));
    await check(540, 660);
    await block.locator('[data-week-resize="start"]').press("ArrowUp");
    await page.waitForFunction(() => !document.querySelector(".weekcal-block.is-saving"));
    await check(535, 660);
    await load(540, 600);
    const box = await block.boundingBox();
    await page.mouse.move(box.x + 20, box.y + 15);
    await page.mouse.down();
    await page.mouse.move(box.x + 320, box.y + 55, { steps: 4 });
    await page.mouse.up();
    await page.waitForFunction(() => !document.querySelector(".weekcal-block.is-saving"));
    await check(600, 660, days[1]);
    beforeCalls = calls;
    await page.locator(".weekcal-block--travel").first().click();
    assert.equal(calls, beforeCalls);
    await block.evaluate(el => el.remove());
    await page.locator(`[data-weekcol][data-day="${days[0]}"]`).evaluate((col, itemID) => {
      const transfer = new DataTransfer();
      transfer.setData("text/plain", itemID);
      const rect = col.getBoundingClientRect();
      col.dispatchEvent(new DragEvent("drop", { bubbles: true, dataTransfer: transfer, clientX: rect.left + 50, clientY: rect.top + 216 }));
    }, id);
    await block.waitFor();
    assert.equal(await block.locator("[data-week-resize]").count(), 2);
    await drag("end", 660);
    await check(540, 660);
    const touchBox = await block.locator('[data-week-resize="end"]').boundingBox();
    const cdp = await context.newCDPSession(page);
    const touch = { x: touchBox.x + touchBox.width / 2, y: touchBox.y + touchBox.height / 2 };
    await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [touch] });
    await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ ...touch, y: touch.y + 40 }] });
    await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    await page.waitForFunction(() => !document.querySelector(".weekcal-block.is-saving"));
    await check(540, 720);
    await cdp.detach();
    assert.deepEqual(errors, []);
  } finally {
    await context.close();
  }
}

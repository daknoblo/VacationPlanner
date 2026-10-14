import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

export async function verifyCurrency(browser) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const script = await readFile(new URL("../../web/static/js/currency.js", import.meta.url), "utf8");
  let code = "EUR";
  let rate = 1;
  let calls = 0;
  let failSave = false;
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await context.addCookies([{ name: "csrf_token", value: "test-token", url: "http://127.0.0.1" }]);
  await page.route("**/*", async route => {
    const url = new URL(route.request().url());
    if (url.origin !== "http://127.0.0.1") throw new Error("Unexpected currency provider request in browser");
    if (url.pathname === "/currency.js") return route.fulfill({ contentType: "text/javascript", body: script });
    if (url.pathname === "/currency") {
      calls++;
      if (route.request().method() === "POST") {
        assert.equal(route.request().headers()["x-csrf-token"], "test-token");
        if (failSave) return route.fulfill({ status: 500, body: "Save failed" });
        code = new URLSearchParams(route.request().postData()).get("currency");
        rate = code === "DKK" ? 7.46 : 1;
      }
      return route.fulfill({ contentType: "text/html", body: `<div data-currency-rate="${rate}" data-currency-code="${code}">1 EUR = ${rate} ${code}</div>` });
    }
    if (url.pathname === "/") return route.fulfill({ contentType: "text/html", body: `<!doctype html><html><body>
      <div hidden data-currency-panel data-currency-url="/currency" data-error="Currency unavailable">
        <select data-currency-select><option>EUR</option><option>DKK</option></select>
        <p data-currency-error hidden></p>
        <div data-currency-values><div data-currency-rate="1" data-currency-code="EUR">1 EUR = 1 EUR</div></div>
        <label data-currency-local-label>Local currency</label>
        <input type="number" min="0" max="1000000000" step="any" value="100" data-currency-local>
        <input type="number" min="0" max="1000000000" step="any" data-currency-euro>
      </div><script src="/currency.js"></script></body></html>` });
    throw new Error(`Unexpected request: ${url}`);
  });
  try {
    await page.goto("http://127.0.0.1");
    assert.equal(calls, 0, "Hidden tabs never poll currency data");
    await page.locator("[data-currency-panel]").evaluate(panel => { panel.hidden = false; });
    const select = page.locator("[data-currency-select]");
    const local = page.locator("[data-currency-local]");
    const euro = page.locator("[data-currency-euro]");
    await select.selectOption("DKK");
    await page.waitForFunction(() => document.querySelector("[data-currency-rate]").dataset.currencyCode === "DKK");
    await local.fill("746");
    assert.equal(await euro.inputValue(), "100.00");
    await euro.fill("25");
    assert.equal(await local.inputValue(), "186.50");
    const inputNode = await euro.elementHandle();
    await euro.focus();
    await page.locator("[data-currency-panel]").evaluate(panel => { panel.hidden = true; });
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    rate = 8;
    await page.locator("[data-currency-panel]").evaluate(panel => { panel.hidden = false; });
    await page.waitForFunction(() => document.querySelector("[data-currency-rate]").dataset.currencyRate === "8");
    assert.equal(await euro.inputValue(), "25");
    assert.equal(await local.inputValue(), "200.00");
    assert.ok(await inputNode.evaluate(node => node === document.querySelector("[data-currency-euro]")), "Polling must retain the converter inputs");
    failSave = true;
    await select.selectOption("EUR");
    await page.locator("[data-currency-error]").waitFor();
    assert.equal(await select.inputValue(), "DKK", "Failed saves restore the persisted currency");
    assert.equal(await euro.inputValue(), "25");
    await local.fill("");
    assert.equal(await euro.inputValue(), "");
    await local.fill("-10");
    assert.equal(await euro.inputValue(), "", "Invalid amounts must not produce a misleading result");
    assert.deepEqual(errors, []);
    console.log("Validated currency selection, bidirectional conversion, cached refreshes, CSRF and failure rollback.");
  } finally {
    await context.close();
  }
}
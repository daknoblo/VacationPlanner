(function () {
  "use strict";
  var root = document.querySelector("[data-currency-panel]");
  if (!root) return;
  var select = root.querySelector("[data-currency-select]");
  var values = root.querySelector("[data-currency-values]");
  var error = root.querySelector("[data-currency-error]");
  var local = root.querySelector("[data-currency-local]");
  var euro = root.querySelector("[data-currency-euro]");
  var timer, request;
  var saving = false;
  var saved = select.value;
  var source = local;

  function convert() {
    var data = values.querySelector("[data-currency-rate]");
    var rate = data && Number(data.dataset.currencyRate);
    var available = Number.isFinite(rate) && rate > 0;
    local.disabled = euro.disabled = !available;
    if (data && data.dataset.currencyCode) root.querySelector("[data-currency-local-label]").textContent = data.dataset.currencyCode;
    var target = source === local ? euro : local;
    if (!available || source.value === "" || !source.validity.valid) {
      target.value = "";
      return;
    }
    var result = source === local ? Number(source.value) / rate : Number(source.value) * rate;
    target.value = Number.isFinite(result) ? result.toFixed(2) : "";
  }
  [local, euro].forEach(function (input) {
    input.addEventListener("input", function () { source = input; convert(); });
  });
  function visible() { return root.getClientRects().length > 0; }
  async function refresh(save) {
    if (!root.dataset.currencyUrl) return;
    if (!visible() || saving) return;
    clearTimeout(timer);
    if (request) request.abort();
    request = new AbortController();
    var current = request;
    if (save) {
      saving = true;
      select.disabled = true;
    }
    try {
      var options = { signal: current.signal, headers: { Accept: "text/html" } };
      if (save) {
        var cookie = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]*)/);
        options.method = "POST";
        options.headers["Content-Type"] = "application/x-www-form-urlencoded";
        options.headers["X-CSRF-Token"] = cookie ? decodeURIComponent(cookie[1]) : "";
        options.body = new URLSearchParams({ currency: select.value }).toString();
      }
      var response = await fetch(root.dataset.currencyUrl, options);
      if (!response.ok) throw new Error("Currency request failed");
      var html = await response.text();
      if (current.signal.aborted) return;
      if (values.innerHTML !== html) values.innerHTML = html;
      var selectedCode = values.querySelector("[data-currency-code]");
      if (!save && selectedCode) select.value = selectedCode.dataset.currencyCode;
      saved = select.value;
      error.hidden = true;
      convert();
    } catch (failure) {
      if (current.signal.aborted) return;
      if (save) select.value = saved;
      error.textContent = root.dataset.error;
      error.hidden = false;
    } finally {
      if (save) { saving = false; select.disabled = false; }
      if (visible() && !current.signal.aborted) timer = setTimeout(function () { refresh(false); }, 30000);
    }
  }
  select.addEventListener("change", function () { refresh(true); });
  var wasVisible = false;
  new ResizeObserver(function () {
    var now = visible();
    if (now && !wasVisible) refresh(false);
    if (!now) {
      clearTimeout(timer);
      if (request && !saving) request.abort();
    }
    wasVisible = now;
  }).observe(root);
  convert();
}());
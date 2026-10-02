(function () {
  "use strict";
  var panel = document.querySelector("[data-weather-panel]");
  var settings = document.querySelector("[data-weather-settings]");
  if (!panel && !settings) return;
  var request, version = 0, timer;
  function text(tag, value, className) {
    var el = document.createElement(tag);
    el.textContent = value;
    if (className) el.className = className;
    return el;
  }
  function paint(view) {
    var days = document.createDocumentFragment();
    view.Days.forEach(function (day) {
      var section = text("section", "", "weather-day");
      section.appendChild(text("h3", day.Label));
      day.Entries.forEach(function (entry) {
        var place = text("article", "", "weather-place");
        place.appendChild(text("h4", entry.Place));
        if (entry.Summary) place.appendChild(text("p", entry.Icon + " " + entry.Summary, "weather-summary"));
        if (entry.Detail) place.appendChild(text("p", entry.Detail, "muted small"));
        if (entry.Notice) place.appendChild(text("p", entry.Notice, "weather-notice small"));
        section.appendChild(place);
      });
      days.appendChild(section);
    });
    panel.querySelector("[data-weather-days]").replaceChildren(days);
    document.querySelectorAll("[data-weather-day]").forEach(function (cell) {
      var fragment = document.createDocumentFragment();
      (view.ByDate[cell.dataset.weatherDay] || []).forEach(function (entry) {
        var node = text("span", entry.Icon + " " + entry.Compact, "calendar-weather__entry");
        node.title = [entry.Place, entry.Summary, entry.Detail, entry.Notice].filter(Boolean).join(" · ");
        node.appendChild(text("span", entry.Place, "calendar-weather__place"));
        if (entry.Notice) node.appendChild(text("span", entry.Notice, "calendar-weather__notice"));
        fragment.appendChild(node);
      });
      cell.replaceChildren(fragment);
    });
  }
  async function load() {
    clearTimeout(timer);
    if (request) request.abort();
    var current = ++version;
    if (document.hidden) return;
    request = new AbortController();
    try {
      var response = await fetch(panel ? panel.dataset.weatherUrl : "/settings/weather/status", {
        signal: request.signal, headers: { "Accept": panel ? "application/json" : "text/plain" }
      });
      if (!response.ok) throw new Error("Weather status unavailable");
      if (panel) {
        var view = await response.json();
        if (!Array.isArray(view.Days) || !view.ByDate) throw new Error("Invalid weather status");
        if (current !== version) return;
        paint(view);
        panel.querySelector("[data-weather-error]").hidden = true;
      } else {
        var status = await response.text();
        if (current !== version) return;
        settings.querySelector("[data-weather-settings-status]").textContent = status;
      }
      document.querySelectorAll("[data-weather-day]").forEach(function (cell) { cell.classList.remove("is-unavailable"); });
    } catch (error) {
      if (current !== version || error.name === "AbortError") return;
      var output = panel ? panel.querySelector("[data-weather-error]") : settings.querySelector("[data-weather-settings-status]");
      output.textContent = (panel || settings).dataset.error;
      output.hidden = false;
      document.querySelectorAll("[data-weather-day]").forEach(function (cell) {
        cell.classList.add("is-unavailable");
        cell.replaceChildren(text("span", panel.dataset.error, "calendar-weather__notice"));
      });
    } finally {
      if (current === version && !document.hidden) timer = setTimeout(load, 60000);
    }
  }
  if (settings) settings.querySelectorAll("[data-weather-form]").forEach(function (form) {
    form.addEventListener("submit", async function (event) {
      event.preventDefault();
      var button = form.querySelector("button");
      if (button.disabled) return;
      button.disabled = true;
      var output = form.querySelector("[data-weather-form-status]");
      output.textContent = "";
      try {
        var response = await fetch(form.action, { method: "POST", headers: { "HX-Request": "true" },
          body: new URLSearchParams(new FormData(form)) });
        var message = await response.text();
        if (!response.ok) throw new Error(response.status === 422 ? message : settings.dataset.error);
        output.textContent = message || settings.dataset.saved;
        output.setAttribute("role", "status");
        load();
      } catch (error) {
        output.textContent = error instanceof TypeError ? settings.dataset.error : error.message;
        output.setAttribute("role", "alert");
      } finally {
        button.disabled = false;
      }
    });
  });
  ["infoChanged", "geographyChanged"].forEach(function (name) { document.body.addEventListener(name, load); });
  document.addEventListener("visibilitychange", load);
  load();
}());

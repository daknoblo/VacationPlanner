(function () {
  "use strict";

  function getCookie(name) {
    var parts = document.cookie ? document.cookie.split("; ") : [];
    for (var i = 0; i < parts.length; i++) {
      var idx = parts[i].indexOf("=");
      if (idx > -1 && parts[i].slice(0, idx) === name) {
        return decodeURIComponent(parts[i].slice(idx + 1));
      }
    }
    return "";
  }

  // Attach the CSRF token to every htmx request (header-based double submit).
  document.body.addEventListener("htmx:configRequest", function (evt) {
    var token = getCookie("csrf_token");
    if (token) {
      evt.detail.headers["X-CSRF-Token"] = token;
    }
  });

  function toast(msg) {
    var el = document.getElementById("toast");
    if (!el) return;
    el.textContent = msg;
    el.classList.add("is-visible");
    window.setTimeout(function () { el.classList.remove("is-visible"); }, 2500);
  }

  document.body.addEventListener("saved", function () {
    var t = document.getElementById("toast");
    toast(t && t.dataset.saved ? t.dataset.saved : "Saved \u2713");
  });

  // Reset forms flagged with data-reset after a successful submit.
  document.body.addEventListener("htmx:afterRequest", function (evt) {
    var form = evt.target;
    if (form && form.matches && form.matches("form[data-reset]") && evt.detail.successful) {
      form.reset();
      clearTempMarker();
    }
  });

  // ---- Leaflet map ----
  var map = null;
  var markerLayer = null;
  var tempMarker = null;

  function clearTempMarker() {
    if (tempMarker && map) {
      map.removeLayer(tempMarker);
      tempMarker = null;
    }
  }

  function escapeHtml(s) {
    var div = document.createElement("div");
    div.textContent = s == null ? "" : String(s);
    return div.innerHTML;
  }

  function initMap() {
    var el = document.getElementById("map");
    if (!el || typeof L === "undefined") return;

    L.Icon.Default.mergeOptions({
      iconRetinaUrl: "/static/vendor/leaflet/images/marker-icon-2x.png",
      iconUrl: "/static/vendor/leaflet/images/marker-icon.png",
      shadowUrl: "/static/vendor/leaflet/images/marker-shadow.png"
    });

    var lat = parseFloat(el.dataset.lat);
    var lng = parseFloat(el.dataset.lng);
    var hasCenter = !isNaN(lat) && !isNaN(lng);

    map = L.map(el).setView(hasCenter ? [lat, lng] : [48.2082, 16.3738], hasCenter ? 12 : 4);

    var attribution = el.dataset.attribution || "\u00A9 OpenStreetMap contributors";
    L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
      referrerPolicy: "strict-origin-when-cross-origin",
      attribution: attribution
    }).addTo(map);

    markerLayer = L.layerGroup().addTo(map);

    map.on("click", function (e) {
      var latInput = document.getElementById("sight-latitude");
      var lngInput = document.getElementById("sight-longitude");
      if (!latInput || !lngInput) return;
      latInput.value = e.latlng.lat.toFixed(6);
      lngInput.value = e.latlng.lng.toFixed(6);
      clearTempMarker();
      tempMarker = L.marker(e.latlng, { opacity: 0.6 }).addTo(map);
    });

    refreshMarkers();
    document.body.addEventListener("itemsChanged", refreshMarkers);
  }

  function refreshMarkers() {
    var el = document.getElementById("map");
    if (!el || !map || !markerLayer) return;
    var id = el.dataset.vacationId;
    if (!id) return;

    fetch("/vacations/" + encodeURIComponent(id) + "/api/items", {
      headers: { "Accept": "application/json" }
    })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (data) {
        markerLayer.clearLayers();
        var bounds = [];
        (data.items || []).forEach(function (s) {
          var marker = L.marker([s.lat, s.lng], { opacity: s.visited ? 0.5 : 1 });
          var title = s.title + (s.category ? " (" + s.category + ")" : "");
          marker.bindPopup(escapeHtml(title));
          marker.addTo(markerLayer);
          bounds.push([s.lat, s.lng]);
        });
        if (data.center) {
          map.setView([data.center.lat, data.center.lng], map.getZoom());
        }
        if (bounds.length > 1) {
          map.fitBounds(bounds, { padding: [30, 30], maxZoom: 14 });
        }
      })
      .catch(function () { /* transient errors are non-fatal */ });
  }

  // ---- Location picker (destination autocomplete + map) ----
  var locationMaps = [];

  function initLocationPickers() {
    if (typeof L === "undefined") return;
    var pickers = document.querySelectorAll(".location-picker");
    for (var i = 0; i < pickers.length; i++) {
      initLocationPicker(pickers[i]);
    }
  }

  function zoomForResult(it) {
    var type = (it.type || "").toLowerCase();
    var cls = (it.class || "").toLowerCase();
    if (type === "country" || cls === "boundary") return 5;
    if (type === "state" || type === "region" || type === "province") return 7;
    if (type === "city" || type === "town" || type === "village") return 11;
    return 9;
  }

  function initLocationPicker(pk) {
    var input = pk.querySelector("[data-geocode-input]");
    var list = pk.querySelector("[data-geocode-list]");
    var latIn = pk.querySelector("[data-geocode-lat]");
    var lngIn = pk.querySelector("[data-geocode-lng]");
    var mapEl = pk.querySelector("[data-geocode-map]");
    if (!mapEl) return;

    var lat = parseFloat(latIn && latIn.value);
    var lng = parseFloat(lngIn && lngIn.value);
    var hasPoint = !isNaN(lat) && !isNaN(lng);

    var lmap = L.map(mapEl).setView(hasPoint ? [lat, lng] : [25, 5], hasPoint ? 6 : 2);
    L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
      referrerPolicy: "strict-origin-when-cross-origin",
      attribution: "\u00A9 OpenStreetMap contributors"
    }).addTo(lmap);
    locationMaps.push(lmap);

    var marker = null;
    function setPoint(la, ln, zoom) {
      if (latIn) latIn.value = la.toFixed(6);
      if (lngIn) lngIn.value = ln.toFixed(6);
      if (marker) { marker.setLatLng([la, ln]); }
      else { marker = L.marker([la, ln]).addTo(lmap); }
      lmap.setView([la, ln], zoom || lmap.getZoom());
    }
    if (hasPoint) setPoint(lat, lng, 6);

    lmap.on("click", function (e) { setPoint(e.latlng.lat, e.latlng.lng); });
    window.setTimeout(function () { lmap.invalidateSize(); }, 200);

    function hideList() { if (list) { list.hidden = true; list.innerHTML = ""; } }

    function renderSuggestions(items) {
      if (!list) return;
      list.innerHTML = "";
      if (!items.length) { hideList(); return; }
      items.forEach(function (it) {
        var opt = document.createElement("button");
        opt.type = "button";
        opt.className = "suggest__item";
        opt.textContent = it.display_name;
        opt.addEventListener("click", function () {
          if (input) input.value = it.display_name;
          setPoint(it.lat, it.lng, zoomForResult(it));
          hideList();
        });
        list.appendChild(opt);
      });
      list.hidden = false;
    }

    function search(q) {
      fetch("/api/geocode?q=" + encodeURIComponent(q), { headers: { "Accept": "application/json" } })
        .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
        .then(function (data) { renderSuggestions((data && data.results) || []); })
        .catch(function () { hideList(); });
    }

    if (input) {
      var timer = null;
      input.addEventListener("input", function () {
        var q = input.value.trim();
        if (timer) window.clearTimeout(timer);
        if (q.length < 2) { hideList(); return; }
        timer = window.setTimeout(function () { search(q); }, 350);
      });
      input.addEventListener("blur", function () { window.setTimeout(hideList, 200); });
    }
  }

  // Resize picker maps when a <details> that contains one is opened.
  document.addEventListener("toggle", function (e) {
    if (e.target && e.target.tagName === "DETAILS") {
      window.setTimeout(function () {
        for (var i = 0; i < locationMaps.length; i++) { locationMaps[i].invalidateSize(); }
      }, 60);
    }
  }, true);

  // Open the native date/time picker as soon as the field is clicked.
  document.addEventListener("click", function (e) {
    var el = e.target;
    if (el && el.matches && el.matches('input[type="date"], input[type="datetime-local"]') &&
        typeof el.showPicker === "function") {
      try { el.showPicker(); } catch (err) { /* already open or not user-activated */ }
    }
  });

  // Print button (export view).
  document.addEventListener("click", function (e) {
    var el = e.target;
    if (el && el.closest && el.closest("[data-print]")) {
      window.print();
    }
  });

  // ---- Tabs ----
  function resizeMaps() {
    window.setTimeout(function () {
      for (var i = 0; i < locationMaps.length; i++) { locationMaps[i].invalidateSize(); }
      if (map) { map.invalidateSize(); }
    }, 30);
  }

  function activateTab(tabsEl, name) {
    var tabs = tabsEl.querySelectorAll(".tabs__tab");
    var panels = tabsEl.querySelectorAll(".tab-panel");
    for (var i = 0; i < tabs.length; i++) {
      tabs[i].classList.toggle("is-active", tabs[i].getAttribute("data-tab") === name);
    }
    for (var j = 0; j < panels.length; j++) {
      panels[j].classList.toggle("is-active", panels[j].getAttribute("data-tab-panel") === name);
    }
    resizeMaps();
    nudgeDaySummary();
  }

  document.addEventListener("click", function (e) {
    // Expander toggle for the collapsible day row (long trips).
    var toggle = e.target && e.target.closest ? e.target.closest("[data-days-toggle]") : null;
    if (toggle) {
      var wrap = toggle.closest("[data-day-tabs]");
      var panel = wrap ? wrap.querySelector("[data-days-panel]") : null;
      if (panel) {
        var willOpen = panel.hasAttribute("hidden");
        if (willOpen) { panel.removeAttribute("hidden"); }
        else { panel.setAttribute("hidden", ""); }
        toggle.setAttribute("aria-expanded", willOpen ? "true" : "false");
      }
      return;
    }
    var tab = e.target && e.target.closest ? e.target.closest(".tabs__tab") : null;
    if (!tab) return;
    var tabsEl = tab.closest("[data-tabs]");
    if (!tabsEl) return;
    var name = tab.getAttribute("data-tab");
    activateTab(tabsEl, name);
    if (window.history && window.history.replaceState) {
      window.history.replaceState(null, "", "#" + name);
    }
    reflectCollapsedDay(tab);
  });

  // reflectCollapsedDay updates the day expander's label and closes its panel
  // once a day inside it has been chosen.
  function reflectCollapsedDay(tab) {
    var coll = tab.closest ? tab.closest("[data-days-collapsible]") : null;
    if (!coll) return;
    var current = coll.querySelector("[data-days-current]");
    if (current) { current.textContent = (tab.textContent || "").replace(/\s+/g, " ").trim(); }
    var panel = coll.querySelector("[data-days-panel]");
    if (panel) { panel.setAttribute("hidden", ""); }
    var toggle = coll.querySelector("[data-days-toggle]");
    if (toggle) { toggle.setAttribute("aria-expanded", "false"); }
  }

  function initTabs() {
    var tabsEl = document.querySelector("[data-tabs]");
    if (!tabsEl) return;
    var hash = (window.location.hash || "").replace(/^#/, "");
    if (/^[a-z0-9-]+$/.test(hash)) {
      var target = tabsEl.querySelector('.tabs__tab[data-tab="' + hash + '"]');
      if (target) { activateTab(tabsEl, hash); reflectCollapsedDay(target); }
    }
  }

  // ---- Activity name autocomplete (AI, destination-aware) ----
  function initActivityInputs() {
    var inputs = document.querySelectorAll("[data-activity-input]");
    for (var i = 0; i < inputs.length; i++) { initActivityInput(inputs[i]); }
  }

  function initActivityInput(input) {
    var form = input.closest("form");
    var list = form ? form.querySelector("[data-activity-list]") : null;
    if (!list) return;
    var dest = input.getAttribute("data-activity-dest") || "";
    var timer = null;
    function hide() { list.hidden = true; list.innerHTML = ""; }
    function fill(it) {
      input.value = it.name || "";
      var cat = form.querySelector("[data-activity-category]");
      var desc = form.querySelector("[data-activity-desc]");
      if (cat && it.category) cat.value = it.category;
      if (desc && it.description) desc.value = it.description;
      hide();
    }
    function search(q) {
      fetch("/api/activities/suggest?q=" + encodeURIComponent(q) + "&dest=" + encodeURIComponent(dest),
        { headers: { "Accept": "application/json" } })
        .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
        .then(function (data) {
          var items = (data && data.results) || [];
          list.innerHTML = "";
          if (!items.length) { hide(); return; }
          items.forEach(function (it) {
            var opt = document.createElement("button");
            opt.type = "button";
            opt.className = "suggest__item";
            opt.textContent = it.name + (it.category ? " \u00b7 " + it.category : "");
            opt.addEventListener("click", function () { fill(it); });
            list.appendChild(opt);
          });
          list.hidden = false;
        })
        .catch(function () { hide(); });
    }
    input.addEventListener("input", function () {
      var q = input.value.trim();
      if (timer) window.clearTimeout(timer);
      if (q.length < 2) { hide(); return; }
      timer = window.setTimeout(function () { search(q); }, 400);
    });
    input.addEventListener("blur", function () { window.setTimeout(hide, 200); });
  }

  // ---- Item location autocomplete (geocode -> fills hidden lat/lng) ----
  function initGeoLiteInputs() {
    var inputs = document.querySelectorAll("[data-geo-lite]");
    for (var i = 0; i < inputs.length; i++) { initGeoLite(inputs[i]); }
  }

  function initGeoLite(input) {
    var wrap = input.closest(".location-picker__field") || input.parentNode;
    var list = wrap.querySelector("[data-geo-lite-list]");
    var form = input.closest("form");
    var latIn = form ? form.querySelector("[data-geo-lite-lat]") : null;
    var lngIn = form ? form.querySelector("[data-geo-lite-lng]") : null;
    var timer = null;

    function clearCoords() { if (latIn) latIn.value = ""; if (lngIn) lngIn.value = ""; }
    function hide() { if (list) { list.hidden = true; list.innerHTML = ""; } }

    function choose(it) {
      input.value = it.display_name || input.value;
      if (latIn) latIn.value = it.lat;
      if (lngIn) lngIn.value = it.lng;
      hide();
    }

    input.addEventListener("input", function () {
      clearCoords();
      var q = input.value.trim();
      if (timer) window.clearTimeout(timer);
      if (q.length < 3) { hide(); return; }
      timer = window.setTimeout(function () {
        fetch("/api/geocode?q=" + encodeURIComponent(q), { headers: { "Accept": "application/json" } })
          .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
          .then(function (data) {
            if (!list) return;
            var results = (data && data.results) || [];
            if (!results.length) { hide(); return; }
            list.innerHTML = "";
            results.slice(0, 6).forEach(function (it) {
              var b = document.createElement("button");
              b.type = "button";
              b.className = "suggest__item";
              b.textContent = it.display_name;
              b.addEventListener("click", function () { choose(it); });
              list.appendChild(b);
            });
            list.hidden = false;
          })
          .catch(function () { hide(); });
      }, 300);
    });

    input.addEventListener("blur", function () { window.setTimeout(hide, 200); });
  }

  // ---- Day planner (drag + resize activity blocks) ----
  var PLANNER_PPM = 0.8;   // px per minute (must match CSS --ppm)
  var PLANNER_SNAP = 5;    // snap to 5-minute steps

  function plLabel(m) {
    m = m < 0 ? 0 : (m > 1440 ? 1440 : m | 0);
    var h = Math.floor(m / 60), mm = m % 60;
    return (h < 10 ? "0" : "") + h + ":" + (mm < 10 ? "0" : "") + mm;
  }
  function plClamp(v, lo, hi) { return v < lo ? lo : (v > hi ? hi : v); }

  function initPlanners() {
    var grids = document.querySelectorAll("[data-planner-grid]");
    for (var i = 0; i < grids.length; i++) { initPlanner(grids[i]); }
  }

  function initPlanner(grid) {
    var drag = null;

    function applyBlock(block, start, end) {
      block.setAttribute("data-start", start);
      block.setAttribute("data-end", end);
      block.style.top = (start * PLANNER_PPM) + "px";
      block.style.height = ((end - start) * PLANNER_PPM) + "px";
      var t = block.querySelector(".planner-block__time");
      if (t) t.textContent = plLabel(start) + "\u2013" + plLabel(end);
    }

    function persist(block) {
      var id = block.getAttribute("data-id");
      if (!id) return;
      var body = new URLSearchParams();
      body.set("start", plLabel(parseInt(block.getAttribute("data-start"), 10)));
      body.set("end", plLabel(parseInt(block.getAttribute("data-end"), 10)));
      fetch("/items/" + encodeURIComponent(id), {
        method: "POST",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded",
          "X-CSRF-Token": getCookie("csrf_token")
        },
        body: body.toString()
      }).catch(function () { /* non-fatal */ });
    }

    grid.addEventListener("pointerdown", function (e) {
      var block = e.target.closest ? e.target.closest(".planner-block") : null;
      if (!block || !grid.contains(block)) return;
      if (e.target.closest(".planner-block__del")) return;
      e.preventDefault();
      var resize = !!e.target.closest("[data-planner-resize]");
      var start = parseInt(block.getAttribute("data-start"), 10) || 0;
      var end = parseInt(block.getAttribute("data-end"), 10) || (start + 60);
      drag = { block: block, mode: resize ? "resize" : "move", y: e.clientY, s: start, e: end };
      block.classList.add("is-dragging");
      if (block.setPointerCapture) { try { block.setPointerCapture(e.pointerId); } catch (err) { /* ignore */ } }
    });

    grid.addEventListener("pointermove", function (e) {
      if (!drag) return;
      var delta = Math.round(((e.clientY - drag.y) / PLANNER_PPM) / PLANNER_SNAP) * PLANNER_SNAP;
      var start = drag.s, end = drag.e;
      if (drag.mode === "move") {
        var dur = end - start;
        start = plClamp(drag.s + delta, 0, 1440 - dur);
        end = start + dur;
      } else {
        end = plClamp(drag.e + delta, start + 15, 1440);
      }
      applyBlock(drag.block, start, end);
    });

    function finish() {
      if (!drag) return;
      var block = drag.block;
      block.classList.remove("is-dragging");
      drag = null;
      persist(block);
    }
    grid.addEventListener("pointerup", finish);
    grid.addEventListener("pointercancel", finish);
  }

  // ---- Mermaid day-summary rendering ----
  function renderMermaid(root) {
    if (typeof window.mermaid === "undefined") return;
    var scope = (root && root.querySelectorAll) ? root : document;
    var nodes = scope.querySelectorAll(".mermaid:not([data-processed])");
    if (!nodes.length) return;
    try { window.mermaid.run({ nodes: nodes }); } catch (e) { /* ignore render errors */ }
  }

  function nudgeDaySummary() {
    var el = document.querySelector(".tab-panel.is-active [data-day-summary]");
    if (el) el.dispatchEvent(new CustomEvent("loadsummary"));
  }

  document.body.addEventListener("htmx:afterSwap", function (e) { renderMermaid(e.target); });
  document.body.addEventListener("itemsChanged", nudgeDaySummary);

  // ---- Date range: choosing "From" constrains and opens "To" ----
  function pairedDateTo(fromEl) {
    var form = fromEl.closest("form");
    return form ? form.querySelector("[data-date-to]") : null;
  }

  function initDateRanges() {
    var froms = document.querySelectorAll("[data-date-from]");
    for (var i = 0; i < froms.length; i++) {
      var to = pairedDateTo(froms[i]);
      if (to && froms[i].value) { to.min = froms[i].value; }
    }
  }

  document.addEventListener("change", function (e) {
    var from = e.target && e.target.closest ? e.target.closest("[data-date-from]") : null;
    if (!from || !from.value) return;
    var to = pairedDateTo(from);
    if (!to) return;
    to.min = from.value;
    if (!to.value || to.value < from.value) { to.value = from.value; }
    try { to.focus({ preventScroll: true }); } catch (err) { /* ignore */ }
    if (typeof to.showPicker === "function") {
      try { to.showPicker(); } catch (err) { /* requires user activation */ }
    }
  });

  // ---- Quick-fill "Home" for the travel from/to fields ----
  document.addEventListener("click", function (e) {
    var btn = e.target && e.target.closest ? e.target.closest("[data-fill-home]") : null;
    if (!btn) return;
    var form = btn.closest("form");
    if (!form) return;
    var input = form.querySelector('[name="' + btn.getAttribute("data-fill-target") + '"]');
    if (input) {
      input.value = btn.getAttribute("data-fill-value") || "";
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.focus();
    }
  });

  function init() {
    if (typeof window.mermaid !== "undefined") {
      window.mermaid.initialize({
        startOnLoad: false,
        securityLevel: "strict",
        theme: "default",
        flowchart: { htmlLabels: false, curve: "basis" }
      });
    }
    initMap();
    initLocationPickers();
    initActivityInputs();
    initGeoLiteInputs();
    initPlanners();
    initTabs();
    initDateRanges();
  }

  if (document.readyState !== "loading") {
    init();
  } else {
    document.addEventListener("DOMContentLoaded", init);
  }
})();

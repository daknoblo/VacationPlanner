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
      resyncIconPicker(form);
    }
  });

  // Re-bind geo autocomplete on content swapped in by htmx (e.g. the travel editor).
  document.body.addEventListener("htmx:afterSwap", function () {
    initGeoLiteInputs();
  });

  // Once an AI suggestion has been added to the trip, remove its tile so it is
  // not offered again in the current list.
  document.body.addEventListener("htmx:afterRequest", function (e) {
    var form = e.target && e.target.closest ? e.target.closest("[data-suggestion-add]") : null;
    if (!form) return;
    if (e.detail && e.detail.successful) {
      var card = form.closest(".suggestion");
      if (card) { card.remove(); }
    }
  });

  // The travel/lodging editors auto-save on change; suppress native submission.
  document.addEventListener("submit", function (e) {
    if (e.target && e.target.matches && e.target.matches("[data-travel-editor], [data-lodging-editor]")) {
      e.preventDefault();
    }
  });

  // Live value display for range sliders (e.g. the AI search radius).
  document.addEventListener("input", function (e) {
    var input = e.target;
    if (!input || !input.matches || !input.matches("[data-range]")) return;
    var field = input.closest(".field") || input.parentNode;
    var out = field ? field.querySelector("[data-range-value]") : null;
    if (out) { out.textContent = input.value; }
  });

  // Chain travel legs: when a leg's destination changes, default the next leg's
  // start to that destination (location + coordinates) and re-save it.
  document.addEventListener("change", function (e) {
    var input = e.target;
    if (!input || !input.matches) return;
    if (!input.matches('.travel-step [name="to_location"], .travel-step [name="to_lat"], .travel-step [name="to_lng"]')) return;
    var form = input.closest("form.travel-step");
    var wrap = form ? form.closest(".travel-step-wrap") : null;
    if (!wrap) return;
    var next = wrap.nextElementSibling;
    while (next && !(next.classList && next.classList.contains("travel-step-wrap"))) { next = next.nextElementSibling; }
    var nextForm = next ? next.querySelector("form.travel-step") : null;
    if (!nextForm) return;
    var to = form.querySelector('[name="to_location"]');
    var nFrom = nextForm.querySelector('[name="from_location"]');
    if (!to || !nFrom || nFrom.value === to.value) return;
    nFrom.value = to.value;
    var toLat = form.querySelector('[name="to_lat"]');
    var toLng = form.querySelector('[name="to_lng"]');
    var nLat = nextForm.querySelector('[name="from_lat"]');
    var nLng = nextForm.querySelector('[name="from_lng"]');
    if (nLat && toLat) { nLat.value = toLat.value; }
    if (nLng && toLng) { nLng.value = toLng.value; }
    nextForm.dispatchEvent(new Event("change", { bubbles: true }));
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

    var lat = parseFloat(el.dataset.lat);
    var lng = parseFloat(el.dataset.lng);
    var hasCenter = !isNaN(lat) && !isNaN(lng);
    var zoom = parseInt(el.dataset.zoom, 10);
    var hasZoom = !isNaN(zoom) && zoom > 0;

    map = L.map(el).setView(hasCenter ? [lat, lng] : [48.2082, 16.3738], hasCenter ? (hasZoom ? zoom : 12) : 4);

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

  function lodgingMarkerIcon() {
    return L.divIcon({ className: "lodging-marker", html: "🛏", iconSize: [28, 28], iconAnchor: [14, 14] });
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
        (data.lodgings || []).forEach(function (s) {
          var marker = L.marker([s.lat, s.lng], { icon: lodgingMarkerIcon(), title: s.title });
          marker.bindPopup(escapeHtml("🛏 " + s.title));
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
    var zoomIn = pk.querySelector("[data-geocode-zoom]");
    var mapEl = pk.querySelector("[data-geocode-map]");
    if (!mapEl) return;

    var lat = parseFloat(latIn && latIn.value);
    var lng = parseFloat(lngIn && lngIn.value);
    var hasPoint = !isNaN(lat) && !isNaN(lng);
    var storedZoom = parseInt(zoomIn && zoomIn.value, 10);
    var hasStoredZoom = !isNaN(storedZoom) && storedZoom > 0;

    var lmap = L.map(mapEl).setView(hasPoint ? [lat, lng] : [25, 5], hasPoint ? (hasStoredZoom ? storedZoom : 6) : 2);
    L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
      referrerPolicy: "strict-origin-when-cross-origin",
      attribution: "\u00A9 OpenStreetMap contributors"
    }).addTo(lmap);
    locationMaps.push(lmap);

    var marker = null;
    function setPoint(la, ln, zoom, persist) {
      var z = zoom || lmap.getZoom();
      if (latIn) latIn.value = la.toFixed(6);
      if (lngIn) lngIn.value = ln.toFixed(6);
      if (persist && zoomIn) { zoomIn.value = z; }
      if (marker) { marker.setLatLng([la, ln]); }
      else { marker = L.marker([la, ln]).addTo(lmap); }
      lmap.setView([la, ln], z);
    }
    if (hasPoint) setPoint(lat, lng, hasStoredZoom ? storedZoom : 6, false);

    // When the picker clears its label on manual clicks (e.g. the AI search
    // center), a raw map click sets a new point without a matching name, so drop
    // the stale label — the coordinates alone then define the location.
    var clearLabelOnClick = pk.hasAttribute("data-geocode-clear-on-click");
    lmap.on("click", function (e) {
      setPoint(e.latlng.lat, e.latlng.lng, null, true);
      if (clearLabelOnClick && input) { input.value = ""; }
    });
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
          setPoint(it.lat, it.lng, zoomForResult(it), true);
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

  // Generic show/hide toggle for a panel referenced by [data-toggle="panel-id"]
  // (e.g. the dashboard "new vacation" form).
  document.addEventListener("click", function (e) {
    var btn = e.target && e.target.closest ? e.target.closest("[data-toggle]") : null;
    if (!btn) return;
    var panel = document.getElementById(btn.getAttribute("data-toggle"));
    if (!panel) return;
    var show = panel.hasAttribute("hidden");
    if (show) { panel.removeAttribute("hidden"); } else { panel.setAttribute("hidden", ""); }
    btn.setAttribute("aria-expanded", show ? "true" : "false");
    if (show) {
      window.setTimeout(function () {
        for (var i = 0; i < locationMaps.length; i++) { locationMaps[i].invalidateSize(); }
      }, 60);
    }
  });

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
    for (var i = 0; i < tabs.length; i++) {
      if (tabs[i].closest("[data-tabs]") !== tabsEl) continue;
      tabs[i].classList.toggle("is-active", tabs[i].getAttribute("data-tab") === name);
    }
    var panels = tabsEl.querySelectorAll(".tab-panel");
    for (var j = 0; j < panels.length; j++) {
      if (panels[j].closest("[data-tabs]") !== tabsEl) continue;
      panels[j].classList.toggle("is-active", panels[j].getAttribute("data-tab-panel") === name);
    }
    resizeMaps();
    refreshDayCards();
  }

  // ---- Overview: click an activity to center the map on it ----
  document.addEventListener("click", function (e) {
    var li = e.target && e.target.closest ? e.target.closest("[data-focus-map]") : null;
    if (!li || !map) return;
    // Don't hijack clicks on the origin picker or other controls inside a card.
    if (e.target.closest && e.target.closest("select,option,button,input,a,label")) return;
    var la = parseFloat(li.getAttribute("data-lat"));
    var ln = parseFloat(li.getAttribute("data-lng"));
    if (isNaN(la) || isNaN(ln)) return;
    map.setView([la, ln], Math.max(map.getZoom(), 13));
    var actives = document.querySelectorAll(".activity-item.is-active");
    for (var i = 0; i < actives.length; i++) { actives[i].classList.remove("is-active"); }
    li.classList.add("is-active");
  });

  document.addEventListener("click", function (e) {
    var tab = e.target && e.target.closest ? e.target.closest(".tabs__tab") : null;
    if (!tab) return;
    var tabsEl = tab.closest("[data-tabs]");
    if (!tabsEl) return;
    var name = tab.getAttribute("data-tab");
    activateTab(tabsEl, name);
    if (window.history && window.history.replaceState) {
      window.history.replaceState(null, "", "#" + name);
    }
  });

  // ---- Tagesplan day/week view toggle ----
  function initViewToggle() {
    var toggles = document.querySelectorAll("[data-viewtoggle]");
    for (var i = 0; i < toggles.length; i++) { bindViewToggle(toggles[i]); }
  }

  function bindViewToggle(toggle) {
    var container = toggle.closest("[data-tagesplan]");
    if (!container) return;
    var dayView = container.querySelector("[data-day-view]");
    var weekView = container.querySelector("[data-weekview]");
    var btns = toggle.querySelectorAll("[data-view]");
    function setView(view) {
      for (var i = 0; i < btns.length; i++) {
        btns[i].classList.toggle("is-active", btns[i].getAttribute("data-view") === view);
      }
      if (dayView) dayView.hidden = (view === "week");
      if (weekView) weekView.hidden = (view !== "week");
    }
    for (var j = 0; j < btns.length; j++) {
      btns[j].addEventListener("click", function () { setView(this.getAttribute("data-view")); });
    }
  }

  // Clicking a day column header in week view jumps to that day in day view.
  document.addEventListener("click", function (e) {
    var head = e.target && e.target.closest ? e.target.closest("[data-goto-day]") : null;
    if (!head) return;
    var container = head.closest("[data-tagesplan]");
    if (!container) return;
    var idx = head.getAttribute("data-goto-day");
    var dayBtn = container.querySelector('[data-viewtoggle] [data-view="day"]');
    if (dayBtn) dayBtn.click();
    var dayTab = container.querySelector('.tabs__tab[data-tab="day-' + idx + '"]');
    if (dayTab) dayTab.click();
  });

  // ---- Icon picker (category symbols) ----
  document.addEventListener("click", function (e) {
    var btn = e.target && e.target.closest ? e.target.closest("[data-icon-picker] .icon-picker__btn") : null;
    if (!btn) return;
    var form = btn.closest("form");
    applyIcon(form, btn.closest("[data-icon-picker]"), btn.getAttribute("data-emoji"));
    if (form) { form.dataset.iconManual = "1"; }
    var drop = btn.closest("[data-icon-drop]");
    if (drop) { drop.open = false; }
  });

  // Suggest a fitting symbol from the typed category name, unless the user has
  // already picked one manually.
  document.addEventListener("input", function (e) {
    var input = e.target;
    if (!input || !input.matches || !input.matches("[data-cat-name]")) return;
    var form = input.closest("form");
    if (!form || form.dataset.iconManual === "1") return;
    var emoji = suggestCategoryIcon(input.value);
    if (emoji) { applyIcon(form, form.querySelector("[data-icon-picker]"), emoji); }
  });

  function applyIcon(form, picker, emoji) {
    if (!emoji) return;
    var hidden = form ? form.querySelector("[data-icon-value]") : null;
    if (hidden) { hidden.value = emoji; }
    var current = form ? form.querySelector("[data-icon-current]") : null;
    if (current) { current.textContent = emoji; }
    if (picker) {
      var btns = picker.querySelectorAll(".icon-picker__btn");
      var match = null;
      for (var i = 0; i < btns.length; i++) {
        if (btns[i].getAttribute("data-emoji") === emoji) { match = btns[i]; break; }
      }
      selectIcon(picker, match);
    }
  }

  function selectIcon(picker, active) {
    if (!picker) return;
    var selected = picker.querySelectorAll(".is-selected");
    for (var i = 0; i < selected.length; i++) { selected[i].classList.remove("is-selected"); }
    if (active) { active.classList.add("is-selected"); }
  }

  // resyncIconPicker resets the picker to its first (default) icon after the
  // form is reset following a successful submit.
  function resyncIconPicker(form) {
    if (!form) return;
    form.dataset.iconManual = "";
    var picker = form.querySelector("[data-icon-picker]");
    var first = picker ? picker.querySelector(".icon-picker__btn") : null;
    applyIcon(form, picker, first ? first.getAttribute("data-emoji") : "");
  }

  var ICON_SUGGESTIONS = [
    [/museum/i, "🏛️"],
    [/gallery|kunst|\bart\b/i, "🖼️"],
    [/restaurant|essen|dinner|lunch|\bfood\b|mittag|abendessen/i, "🍽️"],
    [/caf[eé]|coffee|kaffee|frühst|breakfast/i, "☕"],
    [/wine|wein|vineyard/i, "🍷"],
    [/beer|bier|brewery|\bpub\b/i, "🍺"],
    [/\bbar\b|cocktail|drink/i, "🍸"],
    [/pizza/i, "🍕"],
    [/ice ?cream|\beis\b|gelato/i, "🍦"],
    [/shop|shopping|einkauf|store|\bmall\b/i, "🛍️"],
    [/market|markt/i, "🛒"],
    [/church|kirche|cathedral|\bdom\b/i, "⛪"],
    [/mosque|moschee/i, "🕌"],
    [/castle|schloss|\bburg\b|palace|palast/i, "🏰"],
    [/beach|strand/i, "🏖️"],
    [/island|insel/i, "🏝️"],
    [/mountain|\bberg|gipfel|peak|alpen/i, "⛰️"],
    [/\bpark\b|garden|garten|natur/i, "🏞️"],
    [/hike|wander|trail|trekking/i, "🥾"],
    [/bike|\brad\b|cycl|fahrrad/i, "🚴"],
    [/swim|schwimm|\bpool\b/i, "🏊"],
    [/dive|tauch|snorkel|schnorch/i, "🤿"],
    [/surf/i, "🏄"],
    [/boat|ferry|fähre|schiff|cruise|kreuzfahrt/i, "⛴️"],
    [/sail|segel/i, "⛵"],
    [/\bski\b|snowboard/i, "⛷️"],
    [/golf/i, "⛳"],
    [/tennis/i, "🎾"],
    [/photo|foto|\bview\b|aussicht|viewpoint|panorama/i, "📷"],
    [/theat|\bshow\b|bühne/i, "🎭"],
    [/concert|konzert|music|musik|festival/i, "🎤"],
    [/spa|wellness|sauna|therme/i, "🧖"],
    [/\bzoo\b|tierpark/i, "🦁"],
    [/aquar/i, "🐠"],
    [/amusement|freizeitpark|theme ?park|kirmes/i, "🎡"],
    [/hotel|unterkunft|accommod|apartment|hostel/i, "🏨"],
    [/camp|\bzelt/i, "🏕️"],
    [/train|\bbahn|\bzug\b|railway/i, "🚆"],
    [/metro|subway|u-bahn|tram/i, "🚇"],
    [/\bbus\b/i, "🚌"],
    [/flight|\bflug|airport|flughafen|plane/i, "✈️"],
    [/\bcar\b|\bauto\b|drive|rental|mietwagen|parkplatz|parking/i, "🚗"],
    [/pharmacy|apotheke/i, "💊"],
    [/hospital|krankenhaus|klinik|arzt|doctor/i, "🏥"],
    [/\bbank\b|\bgeld\b|money|\batm\b/i, "🏦"],
    [/\binfo\b|tourist/i, "ℹ️"],
    [/landmark|wahrzeichen|sight|sehensw|monument/i, "🗼"]
  ];

  function suggestCategoryIcon(name) {
    var q = (name || "").trim();
    if (q.length < 3) return "";
    for (var i = 0; i < ICON_SUGGESTIONS.length; i++) {
      if (ICON_SUGGESTIONS[i][0].test(q)) { return ICON_SUGGESTIONS[i][1]; }
    }
    return "";
  }

  function initTabs() {
    var tabsEl = document.querySelector("[data-tabs]");
    if (!tabsEl) return;
    var hash = (window.location.hash || "").replace(/^#/, "");
    if (/^[a-z0-9-]+$/.test(hash)) {
      var candidates = tabsEl.querySelectorAll('.tabs__tab[data-tab="' + hash + '"]');
      for (var i = 0; i < candidates.length; i++) {
        if (candidates[i].closest("[data-tabs]") === tabsEl) { activateTab(tabsEl, hash); break; }
      }
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
    if (input.dataset.geoBound) return;
    input.dataset.geoBound = "1";
    var wrap = input.closest(".location-picker__field") || input.parentNode;
    var list = wrap.querySelector("[data-geo-lite-list]");
    var form = input.closest("form");
    var latName = input.getAttribute("data-geo-lat");
    var lngName = input.getAttribute("data-geo-lng");
    var latIn = form ? (latName ? form.querySelector('[name="' + latName + '"]') : form.querySelector("[data-geo-lite-lat]")) : null;
    var lngIn = form ? (lngName ? form.querySelector('[name="' + lngName + '"]') : form.querySelector("[data-geo-lite-lng]")) : null;
    var timer = null;

    function clearCoords() { if (latIn) latIn.value = ""; if (lngIn) lngIn.value = ""; }
    function hide() { if (list) { list.hidden = true; list.innerHTML = ""; } }

    function choose(it) {
      input.value = it.display_name || input.value;
      if (latIn) latIn.value = it.lat;
      if (lngIn) lngIn.value = it.lng;
      hide();
      input.dispatchEvent(new Event("change", { bubbles: true }));
    }

    input.addEventListener("input", function () {
      clearCoords();
      var q = input.value.trim();
      if (timer) window.clearTimeout(timer);
      if (q.length < 3) { hide(); return; }
      timer = window.setTimeout(function () {
        var url = "/api/geocode?q=" + encodeURIComponent(q);
        var nlat = input.getAttribute("data-geo-near-lat");
        var nlng = input.getAttribute("data-geo-near-lng");
        if (nlat && nlng) { url += "&lat=" + encodeURIComponent(nlat) + "&lon=" + encodeURIComponent(nlng); }
        fetch(url, { headers: { "Accept": "application/json" } })
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
  var WEEK_SNAP = 30;      // week view snaps to 30-minute steps (coarser, easier)

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

  // ---- Drag ideas from the backlog onto a day's grid to schedule them ----
  document.addEventListener("dragstart", function (e) {
    var chip = e.target && e.target.closest ? e.target.closest(".idea-chip") : null;
    if (!chip || !e.dataTransfer) return;
    e.dataTransfer.setData("text/plain", chip.getAttribute("data-id"));
    e.dataTransfer.effectAllowed = "move";
    chip.classList.add("is-dragging");
  });
  document.addEventListener("dragend", function (e) {
    var chip = e.target && e.target.closest ? e.target.closest(".idea-chip") : null;
    if (chip) chip.classList.remove("is-dragging");
  });

  function initGridDrops() {
    var grids = document.querySelectorAll("[data-planner-grid]");
    for (var i = 0; i < grids.length; i++) { initGridDrop(grids[i]); }
  }

  function initGridDrop(grid) {
    grid.addEventListener("dragover", function (e) {
      if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
      e.preventDefault();
      grid.classList.add("is-droptarget");
    });
    grid.addEventListener("dragleave", function () { grid.classList.remove("is-droptarget"); });
    grid.addEventListener("drop", function (e) {
      e.preventDefault();
      grid.classList.remove("is-droptarget");
      var id = e.dataTransfer ? e.dataTransfer.getData("text/plain") : "";
      if (!id) return;
      var day = grid.getAttribute("data-day");
      if (!day) return;
      var rect = grid.getBoundingClientRect();
      var minutes = Math.round(((e.clientY - rect.top) / PLANNER_PPM) / PLANNER_SNAP) * PLANNER_SNAP;
      minutes = plClamp(minutes, 0, 1440 - 60);
      var body = new URLSearchParams();
      body.set("day", day);
      body.set("start", plLabel(minutes));
      body.set("end", plLabel(minutes + 60));
      fetch("/items/" + encodeURIComponent(id) + "/schedule", {
        method: "POST",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded",
          "X-CSRF-Token": getCookie("csrf_token")
        },
        body: body.toString()
      }).then(function (r) { return r.ok ? r.text() : Promise.reject(r.status); })
        .then(function (html) {
          grid.insertAdjacentHTML("beforeend", html);
          var chip = document.querySelector('.idea-chip[data-id="' + id + '"]');
          if (chip && chip.parentNode) chip.parentNode.removeChild(chip);
          document.body.dispatchEvent(new CustomEvent("itemsChanged", { bubbles: true }));
        })
        .catch(function () { /* ignore */ });
    });
  }

  // ---- Drag ideas onto week-view day columns to schedule them ----
  // Mirrors the server's calMinPx piecewise mapping (0–6h compressed).
  function calMinToPx(min) {
    if (min < 0) min = 0; if (min > 1440) min = 1440;
    if (min <= 360) return Math.round(min * 16 / 60);
    return 96 + Math.round((min - 360) * 40 / 60);
  }
  function calPxToMin(px) {
    if (px <= 0) return 0;
    if (px <= 96) return px * 60 / 16;
    return 360 + (px - 96) * 60 / 40;
  }

  function initWeekDrops() {
    var cols = document.querySelectorAll("[data-weekcol]");
    for (var i = 0; i < cols.length; i++) { initWeekDrop(cols[i]); }
  }

  function initWeekDrop(col) {
    if (col.dataset.weekDropBound) return;
    col.dataset.weekDropBound = "1";
    col.addEventListener("dragover", function (e) {
      if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
      e.preventDefault();
      col.classList.add("is-droptarget");
    });
    col.addEventListener("dragleave", function () { col.classList.remove("is-droptarget"); });
    col.addEventListener("drop", function (e) {
      e.preventDefault();
      col.classList.remove("is-droptarget");
      var id = e.dataTransfer ? e.dataTransfer.getData("text/plain") : "";
      if (!id) return;
      var day = col.getAttribute("data-day");
      if (!day) return;
      var rect = col.getBoundingClientRect();
      var minutes = Math.round(calPxToMin(e.clientY - rect.top) / WEEK_SNAP) * WEEK_SNAP;
      minutes = plClamp(minutes, 0, 1440 - 60);
      var chip = document.querySelector('.idea-chip[data-id="' + id + '"]');
      var title = chip ? (chip.getAttribute("data-title") || "") : "";
      var body = new URLSearchParams();
      body.set("day", day);
      body.set("start", plLabel(minutes));
      body.set("end", plLabel(minutes + 60));
      fetch("/items/" + encodeURIComponent(id) + "/schedule", {
        method: "POST",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded",
          "X-CSRF-Token": getCookie("csrf_token")
        },
        body: body.toString()
      }).then(function (r) { return r.ok ? r.text() : Promise.reject(r.status); })
        .then(function () {
          appendWeekBlock(col, id, minutes, minutes + 60, title);
          if (chip && chip.parentNode) chip.parentNode.removeChild(chip);
          document.body.dispatchEvent(new CustomEvent("itemsChanged", { bubbles: true }));
        })
        .catch(function () { /* ignore */ });
    });
  }

  function appendWeekBlock(col, id, start, end, title) {
    var top = calMinToPx(start), bottom = calMinToPx(end);
    var block = document.createElement("div");
    block.className = "weekcal-block weekcal-block--move";
    block.setAttribute("data-id", id);
    block.setAttribute("data-start", start);
    block.setAttribute("data-end", end);
    block.style.top = top + "px";
    block.style.height = (bottom - top) + "px";
    block.setAttribute("title", title);
    var timeEl = document.createElement("span");
    timeEl.className = "weekcal-block__time";
    timeEl.textContent = plLabel(start) + "\u2013" + plLabel(end);
    var titleEl = document.createElement("span");
    titleEl.className = "weekcal-block__title";
    titleEl.textContent = title;
    block.appendChild(timeEl);
    block.appendChild(titleEl);
    col.appendChild(block);
  }

  // ---- Move a scheduled block within the week calendar (drag to reschedule) ----
  // Document-level pointer handling (no capture) so the block can be reparented
  // into another day's column mid-drag without losing events.
  var weekDrag = null;

  function weekColUnder(x, y, block) {
    var prev = block.style.pointerEvents;
    block.style.pointerEvents = "none";
    var el = document.elementFromPoint(x, y);
    block.style.pointerEvents = prev;
    return el && el.closest ? el.closest(".weekcal__col[data-weekcol]") : null;
  }

  document.addEventListener("pointerdown", function (e) {
    var block = e.target && e.target.closest ? e.target.closest(".weekcal-block[data-id]") : null;
    if (!block) return;
    e.preventDefault();
    var start = parseInt(block.getAttribute("data-start"), 10) || 0;
    var end = parseInt(block.getAttribute("data-end"), 10) || (start + 60);
    weekDrag = { block: block, dur: Math.max(15, end - start), col: block.closest(".weekcal__col"), start: start, moved: false };
    block.classList.add("is-dragging");
  });

  document.addEventListener("pointermove", function (e) {
    if (!weekDrag) return;
    weekDrag.moved = true;
    var target = weekColUnder(e.clientX, e.clientY, weekDrag.block) || weekDrag.col;
    if (!target) return;
    if (target !== weekDrag.block.parentNode) { target.appendChild(weekDrag.block); weekDrag.col = target; }
    var rect = target.getBoundingClientRect();
    var minutes = Math.round(calPxToMin(e.clientY - rect.top) / WEEK_SNAP) * WEEK_SNAP;
    minutes = plClamp(minutes, 0, 1440 - weekDrag.dur);
    weekDrag.start = minutes;
    var top = calMinToPx(minutes), bottom = calMinToPx(minutes + weekDrag.dur);
    weekDrag.block.style.top = top + "px";
    weekDrag.block.style.height = Math.max(4, bottom - top) + "px";
    var t = weekDrag.block.querySelector(".weekcal-block__time");
    if (t) t.textContent = plLabel(minutes) + "\u2013" + plLabel(minutes + weekDrag.dur);
  });

  function weekMoveFinish() {
    if (!weekDrag) return;
    var d = weekDrag;
    weekDrag = null;
    d.block.classList.remove("is-dragging");
    if (!d.moved || !d.col) return;
    var day = d.col.getAttribute("data-day");
    var id = d.block.getAttribute("data-id");
    if (!day || !id) return;
    var start = d.start, end = d.start + d.dur;
    d.block.setAttribute("data-start", start);
    d.block.setAttribute("data-end", end);
    var body = new URLSearchParams();
    body.set("day", day);
    body.set("start", plLabel(start));
    body.set("end", plLabel(end));
    fetch("/items/" + encodeURIComponent(id) + "/schedule", {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        "X-CSRF-Token": getCookie("csrf_token")
      },
      body: body.toString()
    }).then(function () {
      document.body.dispatchEvent(new CustomEvent("itemsChanged", { bubbles: true }));
    }).catch(function () { /* ignore */ });
  }
  document.addEventListener("pointerup", weekMoveFinish);
  document.addEventListener("pointercancel", weekMoveFinish);

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
      if (block.hasAttribute("data-static")) return;
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

  // ---- Day route cards refresh ----
  function refreshDayCards() {
    var cards = document.querySelector(".tab-panel.is-active [data-day-cards]");
    if (cards) cards.dispatchEvent(new CustomEvent("loadcards"));
  }

  document.body.addEventListener("itemsChanged", refreshDayCards);

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
      input.dispatchEvent(new Event("change", { bubbles: true }));
      input.focus();
    }
  });

  // Mirror typing between the notes editors (Overview + General) so both always
  // show the same content. Setting .value does not fire input, so this neither
  // loops nor triggers the other editor's auto-save.
  document.addEventListener("input", function (e) {
    var el = e.target;
    if (!el || !el.matches || !el.matches("[data-notes-sync]")) return;
    var all = document.querySelectorAll("[data-notes-sync]");
    for (var i = 0; i < all.length; i++) {
      if (all[i] !== el && all[i].value !== el.value) { all[i].value = el.value; }
    }
  });

  // ---- Document preview (in-page modal; never opens a new tab) ----
  (function () {
    var dlg = document.getElementById("doc-preview");
    if (!dlg) return;
    var bodyEl = dlg.querySelector("[data-doc-preview-body]");
    var nameEl = dlg.querySelector("[data-doc-preview-name]");
    var dlEl = dlg.querySelector("[data-doc-preview-download]");
    var closeBtn = dlg.querySelector("[data-doc-preview-close]");

    // Emptying the body removes the iframe/img so a closed preview stops loading.
    function clearBody() { if (bodyEl) bodyEl.textContent = ""; }
    function closeDlg() { clearBody(); if (dlg.open) { dlg.close(); } }

    // Also clear on Escape / any other close path.
    dlg.addEventListener("close", clearBody);
    dlg.addEventListener("cancel", clearBody);

    // Escape closes the modal explicitly. Focus is kept on the close button (see
    // below) so this keydown reaches the dialog even when a native dialog
    // "cancel" isn't dispatched reliably.
    dlg.addEventListener("keydown", function (e) {
      if (e.key === "Escape" || e.key === "Esc") { e.preventDefault(); closeDlg(); }
    });

    // Close on the close button or a click on the backdrop (the dialog itself).
    dlg.addEventListener("click", function (e) {
      if (e.target === dlg || (e.target.closest && e.target.closest("[data-doc-preview-close]"))) {
        closeDlg();
      }
    });

    document.addEventListener("click", function (e) {
      var a = e.target && e.target.closest ? e.target.closest("[data-doc-preview]") : null;
      if (!a) return;
      var kind = a.getAttribute("data-doc-preview");
      // Only PDFs and images preview inline; other types keep their normal
      // (download) behaviour and never open a new tab.
      if (kind !== "pdf" && kind !== "image") return;
      e.preventDefault();

      var href = a.getAttribute("href");
      var name = a.getAttribute("data-doc-name") || "";
      clearBody();
      if (nameEl) nameEl.textContent = name;
      if (dlEl) dlEl.setAttribute("href", href);

      var node;
      if (kind === "image") {
        node = document.createElement("img");
        node.className = "doc-preview__img";
        node.alt = name;
      } else {
        node = document.createElement("iframe");
        node.className = "doc-preview__frame";
        node.setAttribute("title", name);
      }
      node.src = href;
      if (bodyEl) bodyEl.appendChild(node);

      if (typeof dlg.showModal === "function") { dlg.showModal(); }
      else { dlg.setAttribute("open", ""); }
      // Keep focus on a dialog control (not inside the PDF iframe) so Escape and
      // the close button work immediately.
      if (closeBtn) { try { closeBtn.focus(); } catch (err) { /* ignore */ } }
    });
  })();

  function init() {
    if (typeof L !== "undefined" && L.Icon && L.Icon.Default) {
      // Use the vendored marker images directly. Deleting the Default
      // _getIconUrl override stops Leaflet from prepending its auto-detected
      // imagePath (which would double the URL and 404).
      delete L.Icon.Default.prototype._getIconUrl;
      L.Icon.Default.mergeOptions({
        iconRetinaUrl: "/static/vendor/leaflet/images/marker-icon-2x.png",
        iconUrl: "/static/vendor/leaflet/images/marker-icon.png",
        shadowUrl: "/static/vendor/leaflet/images/marker-shadow.png"
      });
    }
    initMap();
    initLocationPickers();
    initActivityInputs();
    initGeoLiteInputs();
    initPlanners();
    initGridDrops();
    initWeekDrops();
    initTabs();
    initViewToggle();
    initDateRanges();
  }

  if (document.readyState !== "loading") {
    init();
  } else {
    document.addEventListener("DOMContentLoaded", init);
  }
})();

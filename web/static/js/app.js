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
    L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
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
    document.body.addEventListener("sightsChanged", refreshMarkers);
  }

  function refreshMarkers() {
    var el = document.getElementById("map");
    if (!el || !map || !markerLayer) return;
    var id = el.dataset.vacationId;
    if (!id) return;

    fetch("/vacations/" + encodeURIComponent(id) + "/api/sights", {
      headers: { "Accept": "application/json" }
    })
      .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
      .then(function (data) {
        markerLayer.clearLayers();
        var bounds = [];
        (data.sights || []).forEach(function (s) {
          var marker = L.marker([s.lat, s.lng], { opacity: s.visited ? 0.5 : 1 });
          var title = s.name + (s.category ? " (" + s.category + ")" : "");
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
    L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
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

  // Header switcher: navigate to the chosen vacation.
  document.addEventListener("change", function (e) {
    var el = e.target;
    if (el && el.matches && el.matches("select[data-nav-switch]") && el.value) {
      window.location.href = el.value;
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
  }

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

  function initTabs() {
    var tabsEl = document.querySelector("[data-tabs]");
    if (!tabsEl) return;
    var hash = (window.location.hash || "").replace(/^#/, "");
    if (/^[a-z0-9-]+$/.test(hash)) {
      var target = tabsEl.querySelector('.tabs__tab[data-tab="' + hash + '"]');
      if (target) activateTab(tabsEl, hash);
    }
  }

  function init() {
    initMap();
    initLocationPickers();
    initTabs();
  }

  if (document.readyState !== "loading") {
    init();
  } else {
    document.addEventListener("DOMContentLoaded", init);
  }
})();

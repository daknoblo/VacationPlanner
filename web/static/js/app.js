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

  if (document.readyState !== "loading") {
    initMap();
  } else {
    document.addEventListener("DOMContentLoaded", initMap);
  }
})();

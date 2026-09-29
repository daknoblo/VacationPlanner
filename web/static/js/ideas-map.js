(function () {
  "use strict";
  var root = document.querySelector("[data-ideas-map-panel]");
  if (!root || typeof L === "undefined") return;
  var el = root.querySelector("#ideas-map");
  var select = root.querySelector("[data-ideas-map-origin]");
  var status = root.querySelector("[data-ideas-map-status]");
  var cacheStatus = root.querySelector("[data-ideas-map-cache-status]");
  var rows = root.querySelector("[data-ideas-map-rows]");
  var table = root.querySelector(".ideas-map-table");
  var demo = window.VP_DEMO_IDEAS;
  var map, layer, roads, data, request, timer, overlay;
  var version = 0;
  var selected = "";
  var viewKey = null;
  var positions = null;
  var viewPoints = null;
  var rendered = null;
  var pending = false;
  var failed = false;
  var popupIdea = null;
  var userAdjusted = false;
  var fitting = false;
  var activeRow = null;
  var activeRoad = null;

  function located(point) {
    return Number.isFinite(point.lat) && Number.isFinite(point.lng) &&
      Math.abs(point.lat) <= 90 && Math.abs(point.lng) <= 180;
  }
  function visible() { return el.getBoundingClientRect().width > 0; }
  function stop() {
    version++;
    if (request) request.abort();
    clearTimeout(timer);
  }
  function text(tag, value) {
    var node = document.createElement(tag);
    node.textContent = value;
    return node;
  }
  function fit(points) {
    fitting = true;
    if (!points.length) { map.setView([48, 10], 4, { animate: false }); fitting = false; return; }
    var size = map.getSize();
    map.fitBounds(points, {
      padding: [Math.max(30, Math.ceil(size.x * 0.15)), Math.max(30, Math.ceil(size.y * 0.15))],
      maxZoom: 13, animate: false
    });
    fitting = false;
  }
  function clearHighlight(road) {
    if (road && road !== activeRoad) return;
    if (activeRow) activeRow.classList.remove("is-route-active");
    if (activeRoad) activeRoad.setStyle({ color: "#2563eb", weight: 3, opacity: 0.65 });
    activeRow = null;
    activeRoad = null;
  }
  function highlight(row, road, reveal) {
    clearHighlight();
    activeRow = row;
    activeRoad = road;
    row.classList.add("is-route-active");
    road.setStyle({ color: "#1d4ed8", weight: 5, opacity: 1 });
    road.bringToFront();
    if (road.getTooltip()) road.getTooltip().bringToFront();
    if (reveal) {
      // Scroll only the comparison table; keep the hovered map under the pointer.
      var bounds = table.getBoundingClientRect();
      var target = row.getBoundingClientRect();
      if (target.top < bounds.top) table.scrollTop += target.top - bounds.top;
      else if (target.bottom > bounds.bottom) table.scrollTop += target.bottom - bounds.bottom;
    }
  }
  function initialize() {
    if (map) return;
    map = L.map(el, { scrollWheelZoom: false }).setView([48, 10], 4);
    if (!demo) {
      L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
        maxZoom: 19, referrerPolicy: "strict-origin-when-cross-origin",
        attribution: "\u00a9 OpenStreetMap contributors"
      }).addTo(map);
    }
    roads = L.layerGroup().addTo(map);
    layer = L.layerGroup().addTo(map);
    map.on("movestart", function () { if (!fitting) userAdjusted = true; });
  }
  function choose(value) {
    selected = value;
    userAdjusted = false;
    viewKey = null;
    rendered = null;
    if (data) { data.routes = {}; render(); }
    load();
  }
  function summary() {
    var progress = data.progress || {};
    var remaining = progress.total > progress.completed;
    var value = !data.ideas.length ? root.dataset.noIdeas :
      !selected ? root.dataset.overview :
      !data.lodgings.some(function (l) { return l.id === selected && located(l); }) ? root.dataset.noOrigin :
      demo ? root.dataset.demo :
      !data.routing ? root.dataset.disabled :
      pending ? root.dataset.pending :
      failed ? root.dataset.unavailable : root.dataset.ready;
    if (remaining) value += " · " + root.dataset.pending + " (" + progress.completed + "/" + progress.total + ")";
    status.textContent = value;
    cacheStatus.textContent = data.progress_label || "";
  }
  function render() {
    if (!data || !visible()) return;
    initialize();
    var signature = JSON.stringify([selected, data.lodgings, data.ideas, data.routes, data.routing]);
    if (signature === rendered) { summary(); return; }
    rendered = signature;
    select.replaceChildren(new Option(root.dataset.choose, ""));
    data.lodgings.forEach(function (lodging) {
      var option = new Option(lodging.title + " · " + lodging.date_range +
        (located(lodging) ? "" : " · " + root.dataset.missing), lodging.id);
      option.disabled = !located(lodging);
      select.add(option);
    });
    var origin = data.lodgings.find(function (lodging) { return lodging.id === selected && located(lodging); });
    if (selected && !origin && !data.lodgings.some(function (lodging) { return lodging.id === selected; })) {
      select.add(new Option(root.dataset.removed, selected));
    }
    select.value = selected;
    select.disabled = false;
    rows.replaceChildren();
    clearHighlight();
    var reopen = popupIdea;
    layer.clearLayers();
    roads.clearLayers();
    pending = false;
    failed = false;
    var points = [];
    var routeBounds = origin ? L.latLngBounds([[origin.lat, origin.lng]]) : null;
    data.lodgings.forEach(function (lodging) {
      if (!located(lodging)) return;
      var label = text("div", lodging.title + " · " + lodging.date_range);
      var marker = L.marker([lodging.lat, lodging.lng], {
        icon: L.divIcon({ className: "lodging-marker" + (lodging.id === selected ? " ideas-map-origin" : ""),
          html: "\u{1f6cf}", iconSize: [28, 28], iconAnchor: [14, 14] }),
        title: label.textContent, zIndexOffset: lodging.id === selected ? 2000 : 1000
      }).bindPopup(label).addTo(layer);
      marker.on("click", function () { if (selected !== lodging.id) choose(lodging.id); });
      points.push([lodging.lat, lodging.lng]);
    });
    data.ideas.forEach(function (idea, index) {
      var row = document.createElement("tr");
      row.dataset.ideaId = idea.id;
      var name = document.createElement("td");
      var title = text("button", (index + 1) + ". " + idea.title + (idea.day ? " · " + idea.day : ""));
      title.type = "button";
      title.className = "ideas-map-link";
      name.appendChild(title);
      row.appendChild(name);
      var popup = text("div", idea.title + (idea.day ? " · " + idea.day : ""));
      var result = { status: "pending" };
      if (!located(idea)) result = { status: "missing" };
      else if (!origin) result = { status: "noOrigin" };
      else if (demo) {
        var sample = (index + 1) * (data.lodgings.indexOf(origin) + 1);
        result = { status: "demo", distance: (sample * 4.2).toFixed(1) + " km", duration: (sample * 6) + " min",
          geometry: [[origin.lat, origin.lng], [(origin.lat + idea.lat) / 2 + 0.015, (origin.lng + idea.lng) / 2], [idea.lat, idea.lng]] };
      } else if (!data.routing) result = { status: "disabled" };
      else if (data.routes && data.routes[idea.id]) result = data.routes[idea.id];
      if (result.status === "pending") pending = true;
      if (result.status === "unavailable") failed = true;
      var road = Array.isArray(result.geometry) && result.geometry.length >= 2;
      var label = result.status === "ready" && !road ? root.dataset.noGeometry :
        root.dataset[result.status] || root.dataset.unavailable;
      row.append(text("td", result.distance || "\u2014"), text("td", result.duration || "\u2014"), text("td", label));
      rows.appendChild(row);
      popup.appendChild(text("p", (origin ? origin.title + ": " : "") +
        [result.distance, result.duration, label].filter(Boolean).join(" · ")));
      if (origin && road) {
        var line = L.polyline(result.geometry, { color: "#2563eb", weight: 3, opacity: 0.65, className: "idea-driving-route" })
          .bindPopup(popup.cloneNode(true)).addTo(roads);
        routeBounds.extend(line.getBounds());
        if (result.distance) {
          line.bindTooltip(text("span", (index + 1) + " · " + result.distance), {
            permanent: true, direction: "center", className: "idea-route-distance", opacity: 0.95
          });
        }
        line.on("mouseover", function () { highlight(row, line, true); });
        line.on("mouseout", function () { clearHighlight(line); });
        line.on("click", function () { highlight(row, line, true); });
        row.addEventListener("mouseenter", function () { highlight(row, line, false); });
        row.addEventListener("mouseleave", function () { clearHighlight(line); });
        title.addEventListener("focus", function () { highlight(row, line, false); });
        title.addEventListener("blur", function () { clearHighlight(line); });
        var path = line.getElement();
        if (path) {
          path.dataset.ideaId = idea.id;
          path.setAttribute("tabindex", "0");
          path.setAttribute("role", "button");
          path.setAttribute("aria-label", idea.title + " · " + (result.distance || ""));
          path.addEventListener("focus", function () { highlight(row, line, true); });
          path.addEventListener("blur", function () { clearHighlight(line); });
          path.addEventListener("keydown", function (event) {
            if (event.key === "Enter" || event.key === " ") {
              event.preventDefault();
              line.openPopup();
            }
          });
        }
      }
      if (located(idea)) {
        var marker = L.marker([idea.lat, idea.lng], {
          icon: L.divIcon({ className: "idea-map-marker", html: String(index + 1), iconSize: [26, 26], iconAnchor: [13, 13] }),
          title: idea.title
        }).bindPopup(popup, { autoPan: false }).addTo(layer);
        marker.on("popupopen", function () { popupIdea = idea.id; });
        marker.on("popupclose", function () { popupIdea = null; });
        if (reopen === idea.id) marker.openPopup();
        points.push([idea.lat, idea.lng]);
        if (routeBounds) routeBounds.extend([idea.lat, idea.lng]);
        title.addEventListener("click", function () {
          map.setView([idea.lat, idea.lng], Math.max(map.getZoom(), 12), { animate: false });
          marker.openPopup();
          el.scrollIntoView({ block: "nearest" });
        });
      } else {
        title.disabled = true;
        {
          var edit = document.createElement(demo ? "span" : "a");
          edit.className = "idea-location-warning";
          edit.title = root.dataset.locationWarning;
          edit.setAttribute("aria-label", root.dataset.locationWarning);
          var warning = text("span", "!");
          warning.setAttribute("aria-hidden", "true");
          edit.appendChild(warning);
          if (demo) edit.setAttribute("role", "img");
          else {
            edit.href = "#idea-location-" + idea.id;
            edit.setAttribute("data-idea-location-edit", idea.id);
          }
          name.appendChild(edit);
        }
      }
    });
    var fitBounds = routeBounds || (points.length ? L.latLngBounds(points) : null);
    var nextPositions = fitBounds ? fitBounds.toBBoxString() : "";
    var nextView = JSON.stringify([selected, origin ? [origin.lat, origin.lng] : null]);
    viewPoints = (origin || !selected) ? (fitBounds ? [fitBounds.getSouthWest(), fitBounds.getNorthEast()] : []) : null;
    if (viewPoints && (nextView !== viewKey || (!userAdjusted && positions !== nextPositions))) {
      fit(viewPoints);
    }
    if (demo && (nextView !== viewKey || positions !== nextPositions)) {
      if (overlay) overlay.setBounds(map.getBounds());
      else overlay = L.imageOverlay("../../static/demo/map.svg", map.getBounds()).addTo(map);
    }
    viewKey = nextView;
    positions = nextPositions;
    summary();
  }
  async function load() {
    if (!visible()) return;
    stop();
    var run = version;
    if (!data) status.textContent = root.dataset.loading;
    try {
      if (demo) data = demo;
      else {
        request = new AbortController();
        var url = root.dataset.mapUrl + (selected ? "?lodging=" + encodeURIComponent(selected) : "");
        var response = await fetch(url, { signal: request.signal, headers: { Accept: "application/json" } });
        if (!response.ok) throw new Error("Map unavailable");
        var nextData = await response.json();
        if (run !== version) return;
        if (!Array.isArray(nextData.lodgings) || !Array.isArray(nextData.ideas)) throw new Error("Invalid map");
        data = nextData;
      }
      render();
    } catch (error) {
      if (run !== version) return;
      data = null;
      rows.replaceChildren();
      clearHighlight();
      if (layer) layer.clearLayers();
      if (roads) roads.clearLayers();
      rendered = null;
      status.textContent = root.dataset.error;
      cacheStatus.textContent = "";
    }
    if (!demo && run === version && visible()) timer = setTimeout(load, 3000);
  }
  select.addEventListener("change", function () { choose(select.value); });
  ["itemsChanged", "infoChanged", "geographyChanged"].forEach(function (event) {
    document.body.addEventListener(event, load);
  });
  var wasVisible = false;
  function resizeMap() {
    fitting = true;
    map.invalidateSize();
    fitting = false;
    if (!userAdjusted && viewPoints) fit(viewPoints);
    if (demo && overlay) overlay.setBounds(map.getBounds());
  }
  new ResizeObserver(function () {
    var now = visible();
    if (now && !wasVisible) {
      if (map) resizeMap();
      load();
    } else if (now && map) resizeMap();
    else if (!now) stop();
    wasVisible = now;
  }).observe(el);
}());

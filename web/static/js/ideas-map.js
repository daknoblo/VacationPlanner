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
  var failed = false;
  var popupIdea = null;
  var userAdjusted = false;
  var fitting = false;
  var activeRow = null;
  var activeRoad = null;
  var routeLabels = [];
  var labelFrame = null;
  var labelGuides = null;
  var moving = false;
  var scheduling = false;
  var scheduleErrors = new Map();

  function choosingDay() {
    return scheduling || document.activeElement && document.activeElement.matches("[data-idea-schedule]");
  }

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
  function updateLabelHighlights() {
    routeLabels.forEach(function (entry) {
      entry.tooltip.getElement().classList.toggle("is-route-active", entry.road === activeRoad);
    });
  }
  function clearHighlight(road, keepLabels) {
    if (road && road !== activeRoad) return;
    if (road && road.getTooltip() && road.getTooltip().getElement().matches(":hover, :focus-within")) return;
    if (activeRow) activeRow.classList.remove("is-route-active");
    if (activeRoad) activeRoad.setStyle({ color: "#2563eb", weight: 3, opacity: 0.65 });
    activeRow = null;
    activeRoad = null;
    updateLabelHighlights();
    if (!keepLabels) scheduleLabels();
  }
  function highlight(row, road, reveal, keepLabels) {
    clearHighlight(null, true);
    activeRow = row;
    activeRoad = road;
    row.classList.add("is-route-active");
    road.setStyle({ color: "#c026d3", weight: 5, opacity: 1 });
    road.bringToFront();
    if (road.getTooltip()) road.getTooltip().bringToFront();
    updateLabelHighlights();
    if (!keepLabels) scheduleLabels();
    if (reveal) {
      // Scroll only the comparison table; keep the hovered map under the pointer.
      var bounds = table.getBoundingClientRect();
      var target = row.getBoundingClientRect();
      if (target.top < bounds.top) table.scrollTop += target.top - bounds.top;
      else if (target.bottom > bounds.bottom) table.scrollTop += target.bottom - bounds.bottom;
    }
  }
  function focusRoad(row, road) {
    popupIdea = null;
    map.closePopup();
    userAdjusted = true;
    var bounds = road.getBounds();
    fit([bounds.getSouthWest(), bounds.getNorthEast()]);
    highlight(row, road, true);
    road.openPopup(road.getCenter());
    if (demo && overlay) overlay.setBounds(map.getBounds());
  }
  function hideLabels() {
    if (labelGuides) labelGuides.replaceChildren();
    routeLabels.forEach(function (entry) { entry.tooltip.getElement().style.visibility = "hidden"; });
  }
  function scheduleLabels() {
    if (labelFrame !== null) cancelAnimationFrame(labelFrame);
    labelFrame = requestAnimationFrame(layoutLabels);
  }
  function overlaps(a, b) {
    return a.left < b.right + 5 && a.right + 5 > b.left && a.top < b.bottom + 5 && a.bottom + 5 > b.top;
  }
  function layoutLabels() {
    labelFrame = null;
    if (!map || moving || !visible()) return;
    labelGuides.replaceChildren();
    if (!routeLabels.length) return;
    var size = map.getSize();
    var viewport = el.getBoundingClientRect();
    var occupied = [];
    var obstacles = Array.from(el.querySelectorAll(".leaflet-control, .leaflet-marker-icon"));
    map.eachLayer(function (item) {
      if (item instanceof L.Popup && item.isOpen()) obstacles.push(item.getElement());
    });
    obstacles.forEach(function (node) {
      var rect = node.getBoundingClientRect();
      occupied.push({ left: rect.left - viewport.left, right: rect.right - viewport.left,
        top: rect.top - viewport.top, bottom: rect.bottom - viewport.top });
    });
    var widest = 0, tallest = 0;
    routeLabels.forEach(function (entry) {
      widest = Math.max(widest, entry.tooltip.getElement().offsetWidth);
      tallest = Math.max(tallest, entry.tooltip.getElement().offsetHeight);
    });
    var columns = Math.floor((size.x - 8) / (widest + 8));
    var labelRows = Math.floor((size.y - 8) / (tallest + 8));
    var slots = [];
    for (var row = 0; row < labelRows; row++) {
      for (var col = 0; col < columns; col++) {
        slots.push(L.point((size.x - (columns - 1) * (widest + 8)) / 2 + col * (widest + 8),
          (size.y - (labelRows - 1) * (tallest + 8)) / 2 + row * (tallest + 8)));
      }
    }
    // Prioritize the hovered/focused route when a very small viewport cannot fit every label.
    var ordered = routeLabels.filter(function (entry) { return entry.road === activeRoad; })
      .concat(routeLabels.filter(function (entry) { return entry.road !== activeRoad; }));
    ordered.forEach(function (entry) {
      var node = entry.tooltip.getElement();
      var width = node.offsetWidth, height = node.offsetHeight;
      if (width > size.x - 16 || height > size.y - 16) { node.style.visibility = "hidden"; return; }
      var midpoint = map.latLngToContainerPoint(entry.road.getCenter());
      var target = midpoint.x >= 0 && midpoint.x <= size.x && midpoint.y >= 0 && midpoint.y <= size.y ?
        midpoint : size.divideBy(2);
      var nearest = entry.road.closestLayerPoint(map.containerPointToLayerPoint(target));
      if (!nearest) { node.style.visibility = "hidden"; return; }
      var anchor = map.layerPointToContainerPoint(nearest);
      var placed = null;
      if (node.matches(":hover, :focus-within")) {
        var current = node.getBoundingClientRect();
        var pinned = { left: current.left - viewport.left, right: current.right - viewport.left,
          top: current.top - viewport.top, bottom: current.bottom - viewport.top };
        if (pinned.left >= 8 && pinned.top >= 8 && pinned.right <= size.x - 8 && pinned.bottom <= size.y - 8 &&
            !occupied.some(function (rect) { return overlaps(pinned, rect); })) placed = pinned;
      }
      var candidates = slots.slice().sort(function (a, b) { return a.distanceTo(anchor) - b.distanceTo(anchor); });
      for (var index = 0; index < candidates.length && !placed; index++) {
        var point = candidates[index];
        var candidate = { left: point.x - width / 2, top: point.y - height / 2,
          right: point.x + width / 2, bottom: point.y + height / 2 };
        if (occupied.some(function (rect) { return overlaps(candidate, rect); })) continue;
        placed = candidate;
        break;
      }
      if (!placed) {
        var closest = Infinity;
        for (var y = 8; y + height <= size.y - 8; y += 8) {
          for (var x = 8; x + width <= size.x - 8; x += 8) {
            var distance = L.point(x + width / 2, y + height / 2).distanceTo(anchor);
            if (distance >= closest) continue;
            var gap = { left: x, top: y, right: x + width, bottom: y + height };
            if (occupied.some(function (rect) { return overlaps(gap, rect); })) continue;
            placed = gap;
            closest = distance;
          }
        }
      }
      if (!placed) { node.style.visibility = "hidden"; return; }
      occupied.push(placed);
      var center = L.point(placed.left + width / 2, placed.top + height / 2);
      entry.tooltip.setLatLng(map.containerPointToLatLng(center));
      node.style.visibility = "visible";
      node.classList.toggle("is-route-active", entry.road === activeRoad);
      if (center.distanceTo(anchor) > 8) {
        var guide = document.createElementNS("http://www.w3.org/2000/svg", "line");
        guide.setAttribute("x1", anchor.x);
        guide.setAttribute("y1", anchor.y);
        guide.setAttribute("x2", center.x);
        guide.setAttribute("y2", center.y);
        labelGuides.appendChild(guide);
      }
    });
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
    labelGuides = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    labelGuides.classList.add("idea-route-guides");
    labelGuides.setAttribute("aria-hidden", "true");
    el.appendChild(labelGuides);
    map.on("movestart", function () { if (!fitting) userAdjusted = true; });
    map.on("movestart zoomstart", function () { moving = true; hideLabels(); });
    map.on("moveend", function () { moving = false; scheduleLabels(); });
    map.on("popupopen popupclose", scheduleLabels);
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
    var value = !data.ideas.length ? root.dataset.noIdeas :
      !selected ? "" :
      !data.lodgings.some(function (l) { return l.id === selected && located(l); }) ? root.dataset.noOrigin :
      demo ? "" :
      !data.routing ? root.dataset.disabled :
      failed ? root.dataset.unavailable : "";
    status.textContent = value;
    status.hidden = !value;
    cacheStatus.textContent = data.progress_label || "";
  }
  async function scheduleIdea(idea, picker, feedback) {
    var previous = idea.scheduled_day || "";
    var day = picker.value;
    if (!day || day === previous) return;
    stop();
    scheduling = true;
    scheduleErrors.delete(idea.id);
    picker.disabled = true;
    select.disabled = true;
    feedback.setAttribute("role", "status");
    feedback.textContent = root.dataset.saving;
    try {
      var cookie = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]*)/);
      var response = await fetch("/items/" + encodeURIComponent(idea.id) + "/schedule", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded",
          "X-CSRF-Token": cookie ? decodeURIComponent(cookie[1]) : "" },
        body: new URLSearchParams({ day: day, day_only: "1" }).toString()
      });
      if (!response.ok) throw new Error("Scheduling failed");
      var html = await response.text();
      idea.scheduled_day = day;
      feedback.textContent = "";
      picker.blur();
      scheduling = false;
      document.body.dispatchEvent(new CustomEvent("itemsChanged", {
        bubbles: true, detail: { scheduledItem: { id: idea.id, day: day, html: html } }
      }));
    } catch (error) {
      scheduleErrors.set(idea.id, true);
      picker.value = previous;
      feedback.setAttribute("role", "alert");
      feedback.textContent = root.dataset.scheduleError;
    } finally {
      scheduling = false;
      picker.disabled = false;
      select.disabled = false;
      clearTimeout(timer);
      timer = setTimeout(load, 3000);
    }
  }
  function render() {
    if (!data || !visible()) return;
    initialize();
    var signature = JSON.stringify([selected, data.lodgings, data.ideas, data.routes, data.routing, data.days]);
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
    hideLabels();
    routeLabels = [];
    layer.clearLayers();
    roads.clearLayers();
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
      var title = text("button", (index + 1) + ". " + idea.title);
      title.type = "button";
      title.className = "ideas-map-link";
      name.appendChild(title);
      var description = idea.description || (
        idea.description_status === "unavailable" ? root.dataset.descriptionUnavailable :
        idea.description_status === "pending" || idea.description_status === "running" ? root.dataset.descriptionPending : "");
      if (description) {
        var excerpt = text("span", description);
        excerpt.className = "ideas-map-description muted small";
        if (idea.description_status === "ready") excerpt.title = root.dataset.descriptionAi;
        name.appendChild(excerpt);
      }
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
      if (result.status === "unavailable") failed = true;
      var road = Array.isArray(result.geometry) && result.geometry.length >= 2;
      var label = result.status === "ready" && !road ? root.dataset.noGeometry :
        root.dataset[result.status] || root.dataset.unavailable;
      var distance = text("td", result.distance || "\u2014");
      var duration = text("td", result.duration || "\u2014");
      distance.title = duration.title = label;
      row.append(distance, duration);
      var schedule = document.createElement("td");
      var picker = document.createElement("select");
      picker.setAttribute("data-idea-schedule", idea.id);
      picker.setAttribute("aria-label", root.dataset.schedule + " · " + idea.title);
      var placeholder = new Option(root.dataset.unscheduled, "");
      placeholder.disabled = true;
      picker.add(placeholder);
      (data.days || []).forEach(function (day) { picker.add(new Option(day.label, day.value)); });
      if (idea.scheduled_day && !(data.days || []).some(function (day) { return day.value === idea.scheduled_day; })) {
        var oldDay = new Option(idea.day || idea.scheduled_day, idea.scheduled_day);
        oldDay.disabled = true;
        picker.add(oldDay);
      }
      picker.value = idea.scheduled_day || "";
      picker.disabled = !!demo || !(data.days || []).length;
      var feedback = text("span", "");
      feedback.className = "ideas-map-schedule-status small";
      if (scheduleErrors.has(idea.id)) {
        feedback.setAttribute("role", "alert");
        feedback.textContent = root.dataset.scheduleError;
      }
      picker.addEventListener("change", function () { scheduleIdea(idea, picker, feedback); });
      schedule.append(picker, feedback);
      row.appendChild(schedule);
      rows.appendChild(row);
      popup.appendChild(text("p", (origin ? origin.title + ": " : "") +
        [result.distance, result.duration, label].filter(Boolean).join(" · ")));
      if (origin && road) {
        var routeName = [root.dataset.focusRoute, (index + 1) + ". " + idea.title, result.distance, result.duration].filter(Boolean).join(" · ");
        var line = L.polyline(result.geometry, { color: "#2563eb", weight: 3, opacity: 0.65, className: "idea-driving-route" })
          .bindPopup(popup.cloneNode(true), { autoPan: false }).addTo(roads);
        routeBounds.extend(line.getBounds());
        if (result.distance) {
          var routeButton = document.createElement("button");
          routeButton.type = "button";
          routeButton.className = "idea-route-label";
          routeButton.dataset.ideaId = idea.id;
          routeButton.setAttribute("aria-label", routeName);
          var number = text("span", String(index + 1));
          number.className = "idea-route-number";
          var metrics = document.createElement("span");
          metrics.className = "idea-route-metrics";
          metrics.append(text("span", result.distance), text("span", result.duration || "\u2014"));
          routeButton.append(number, metrics);
          L.DomEvent.disableClickPropagation(routeButton);
          routeButton.addEventListener("click", function (event) {
            event.stopPropagation();
            focusRoad(row, line);
          });
          // Do not move a label out from under the pointer or keyboard focus.
          routeButton.addEventListener("mouseenter", function () { highlight(row, line, true, true); });
          routeButton.addEventListener("mouseleave", function () { clearHighlight(line, true); });
          routeButton.addEventListener("focus", function () { highlight(row, line, true, true); });
          routeButton.addEventListener("blur", function () { clearHighlight(line, true); });
          line.bindTooltip(routeButton, {
            permanent: true, interactive: true, direction: "center", className: "idea-route-distance", opacity: 0.95
          });
          line.getTooltip().getElement().setAttribute("role", "presentation");
          routeLabels.push({ road: line, tooltip: line.getTooltip() });
        }
        line.on("mouseover", function () { highlight(row, line, true); });
        line.on("mouseout", function () { clearHighlight(line); });
        line.on("click", function () { focusRoad(row, line); });
        row.addEventListener("mouseenter", function () { highlight(row, line, false); });
        row.addEventListener("mouseleave", function () { clearHighlight(line); });
        title.addEventListener("focus", function () { highlight(row, line, false); });
        title.addEventListener("blur", function () { clearHighlight(line); });
        var path = line.getElement();
        if (path) {
          path.dataset.ideaId = idea.id;
          path.setAttribute("tabindex", "0");
          path.setAttribute("role", "button");
          path.setAttribute("aria-label", routeName);
          path.addEventListener("focus", function () { highlight(row, line, true); });
          path.addEventListener("blur", function () { clearHighlight(line); });
          path.addEventListener("keydown", function (event) {
            if (event.key === "Enter" || event.key === " ") {
              event.preventDefault();
              focusRoad(row, line);
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
        if (line) {
          marker.on("click", function () { focusRoad(row, line); });
          marker.on("mouseover", function () { highlight(row, line, true); });
          marker.on("mouseout", function () { clearHighlight(line); });
          marker.getElement().setAttribute("aria-label", routeName);
          marker.getElement().addEventListener("keydown", function (event) {
            if (event.key === " ") {
              event.preventDefault();
              focusRoad(row, line);
            }
          });
        }
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
          title.after(edit);
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
    scheduleLabels();
    summary();
  }
  async function load() {
    if (!visible()) return;
    if (choosingDay()) {
      clearTimeout(timer);
      timer = setTimeout(load, 3000);
      return;
    }
    stop();
    var run = version;
    if (!data) { status.textContent = root.dataset.loading; status.hidden = false; }
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
        if (choosingDay()) {
          timer = setTimeout(load, 3000);
          return;
        }
        data = nextData;
      }
      render();
    } catch (error) {
      if (run !== version) return;
      data = null;
      rows.replaceChildren();
      clearHighlight();
      hideLabels();
      routeLabels = [];
      if (layer) layer.clearLayers();
      if (roads) roads.clearLayers();
      rendered = null;
      status.textContent = root.dataset.error;
      status.hidden = false;
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
    scheduleLabels();
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

(function () {
  "use strict";
  var root = document.querySelector("[data-ideas-map-panel]");
  if (!root || typeof L === "undefined") return;
  var el = root.querySelector("#ideas-map");
  var select = root.querySelector("[data-ideas-map-origin]");
  var status = root.querySelector("[data-ideas-map-status]");
  var rows = root.querySelector("[data-ideas-map-rows]");
  var demo = window.VP_DEMO_IDEAS;
  var map, layer, data, signature, request, timer;
  var dirty = true;
  var version = 0;
  var selected = "";
  var cache = new Map();

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
  function message(result) {
    return root.dataset[result.status] || root.dataset.unavailable;
  }
  function fit(points) {
    if (!points.length) return;
    var size = map.getSize();
    map.fitBounds(points, {
      padding: [Math.max(30, Math.ceil(size.x * 0.15)), Math.max(30, Math.ceil(size.y * 0.15))],
      maxZoom: 13
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
    layer = L.layerGroup().addTo(map);
  }
  function render() {
    if (!data || !visible()) return;
    initialize();
    stop();
    var run = version;
    select.replaceChildren(new Option(root.dataset.choose, ""));
    data.lodgings.forEach(function (lodging) {
      var option = new Option(lodging.title + " · " + lodging.date_range +
        (located(lodging) ? "" : " · " + root.dataset.missing), lodging.id);
      option.disabled = !located(lodging);
      select.add(option);
    });
    var origin = data.lodgings.find(function (lodging) { return lodging.id === selected && located(lodging); });
    if (!selected) {
      origin = data.lodgings.find(located);
      selected = origin ? origin.id : "";
    }
    if (selected && !origin && !data.lodgings.some(function (lodging) { return lodging.id === selected; })) {
      select.add(new Option(root.dataset.removed, selected));
    }
    select.value = selected;
    select.disabled = !data.lodgings.some(located);
    rows.replaceChildren();
    layer.clearLayers();
    var points = [];
    data.lodgings.forEach(function (lodging) {
      if (!located(lodging)) return;
      var label = text("div", lodging.title + " · " + lodging.date_range);
      var marker = L.marker([lodging.lat, lodging.lng], {
        icon: L.divIcon({ className: "lodging-marker" + (lodging.id === selected ? " ideas-map-origin" : ""),
          html: "\u{1f6cf}", iconSize: [28, 28], iconAnchor: [14, 14] }),
        title: label.textContent,
        zIndexOffset: lodging.id === selected ? 2000 : 1000
      }).bindPopup(label).addTo(layer);
      marker.on("click", function () {
        if (selected !== lodging.id) { selected = lodging.id; render(); }
      });
      points.push([lodging.lat, lodging.lng]);
    });
    var queue = [];
    data.ideas.forEach(function (idea, index) {
      var row = document.createElement("tr");
      row.dataset.ideaId = idea.id;
      var name = document.createElement("td");
      var title = text("button", (index + 1) + ". " + idea.title + (idea.day ? " · " + idea.day : ""));
      title.type = "button";
      title.className = "ideas-map-link";
      name.appendChild(title);
      row.appendChild(name);
      var distance = text("td", "\u2014");
      var duration = text("td", "\u2014");
      var state = text("td", "");
      row.append(distance, duration, state);
      rows.appendChild(row);
      var popup = text("div", idea.title + (idea.day ? " · " + idea.day : ""));
      var detail = text("p", "");
      popup.appendChild(detail);
      var marker;
      if (located(idea)) {
        marker = L.marker([idea.lat, idea.lng], {
          icon: L.divIcon({ className: "idea-map-marker", html: String(index + 1), iconSize: [26, 26], iconAnchor: [13, 13] }),
          title: idea.title
        }).bindPopup(popup).addTo(layer);
        points.push([idea.lat, idea.lng]);
        title.addEventListener("click", function () {
          map.setView([idea.lat, idea.lng], Math.max(map.getZoom(), 12));
          marker.openPopup();
          el.scrollIntoView({ block: "nearest" });
        });
      } else {
        title.disabled = true;
        if (!demo) {
          var edit = text("a", root.dataset.missing);
          edit.href = "#idea-location-" + idea.id;
          edit.setAttribute("data-idea-location-edit", "");
          name.appendChild(edit);
        }
      }
      function update(result) {
        distance.textContent = result.distance || "\u2014";
        duration.textContent = result.duration || "\u2014";
        state.textContent = message(result);
        detail.textContent = (origin ? origin.title + ": " : "") +
          [result.distance, result.duration, message(result)].filter(Boolean).join(" · ");
      }
      if (!located(idea)) { update({ status: "missing" }); return; }
      if (!origin) { update({ status: "noOrigin" }); return; }
      if (demo) {
        var sample = (index + 1) * (data.lodgings.indexOf(origin) + 1);
        update({ status: "demo", distance: (sample * 4.2).toFixed(1) + " km", duration: (sample * 6) + " min" });
        return;
      }
      if (!data.routing) { update({ status: "disabled" }); return; }
      var key = JSON.stringify([origin.id, origin.lat, origin.lng, idea.id, idea.lat, idea.lng]);
      var saved = cache.get(key);
      if (saved && Date.now() - saved.time < 30 * 60 * 1000) { update(saved.result); return; }
      update({ status: "pending" });
      queue.push({ idea: idea, key: key, update: update });
    });
    var nextSignature = JSON.stringify(points);
    if (signature !== nextSignature) {
      if (demo && points.length) {
        L.imageOverlay("../../static/demo/map.svg", L.latLngBounds(points).pad(2)).addTo(map);
      }
      fit(points);
      signature = nextSignature;
    }
    var completed = 0;
    var total = queue.length;
    var failures = false;
    function summary(failed) {
      status.textContent = !data.ideas.length ? root.dataset.noIdeas : !origin ? root.dataset.noOrigin :
        demo ? root.dataset.demo : !data.routing ? root.dataset.disabled :
        failed ? root.dataset.unavailable :
        queue.length ? root.dataset.loading + " (" + completed + "/" + total + ")" :
        failures ? root.dataset.unavailable : root.dataset.ready;
    }
    summary(false);
    async function next() {
      if (run !== version || !visible() || !queue.length) return;
      var entry = queue.shift();
      request = new AbortController();
      try {
        var url = root.dataset.routeUrl + "?lodging=" + encodeURIComponent(origin.id) + "&item=" + encodeURIComponent(entry.idea.id);
        var response = await fetch(url, { signal: request.signal, headers: { Accept: "application/json" } });
        if (!response.ok) throw new Error("Route unavailable");
        var result = await response.json();
        if (run !== version) return;
        if (!["ready", "unavailable", "missing", "disabled"].includes(result.status)) throw new Error("Invalid route response");
        if (result.status === "ready" && (!result.distance || !result.duration)) throw new Error("Invalid route metrics");
        entry.update(result);
        if (result.status === "ready") {
          if (cache.size >= 256) cache.delete(cache.keys().next().value);
          cache.set(entry.key, { result: result, time: Date.now() });
        } else {
          failures = true;
        }
        completed++;
        summary(false);
        timer = setTimeout(next, 1600);
      } catch (error) {
        if (run !== version) return;
        entry.update({ status: "unavailable" });
        queue.forEach(function (pending) { pending.update({ status: "unavailable" }); });
        queue = [];
        summary(true);
      }
    }
    next();
  }
  async function load() {
    if (!visible()) return;
    stop();
    var run = version;
    status.textContent = root.dataset.loading;
    select.disabled = true;
    try {
      if (demo) { data = demo; }
      else {
        request = new AbortController();
        var response = await fetch(root.dataset.mapUrl, { signal: request.signal, headers: { Accept: "application/json" } });
        if (!response.ok) throw new Error("Map unavailable");
        var nextData = await response.json();
        if (run !== version) return;
        if (!Array.isArray(nextData.lodgings) || !Array.isArray(nextData.ideas)) throw new Error("Invalid map");
        data = nextData;
      }
      dirty = false;
      render();
    } catch (error) {
      if (run !== version) return;
      dirty = true;
      data = null;
      rows.replaceChildren();
      if (layer) layer.clearLayers();
      signature = null;
      status.textContent = root.dataset.error;
    }
  }
  select.addEventListener("change", function () { selected = select.value; render(); });
  root.querySelector("[data-ideas-map-retry]").addEventListener("click", load);
  ["itemsChanged", "infoChanged", "geographyChanged"].forEach(function (event) {
    document.body.addEventListener(event, function () { dirty = true; load(); });
  });
  var wasVisible = false;
  new ResizeObserver(function () {
    var now = visible();
    if (now && !wasVisible) {
      if (dirty || !data) load();
      else { map.invalidateSize(); render(); }
    } else if (now && map) { map.invalidateSize(); }
    else if (!now) { stop(); }
    wasVisible = now;
  }).observe(el);
}());

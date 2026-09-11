/* Static-only navigation. No requests, storage, mutations, or live service clients. */
(function () {
  "use strict";
  var mainTabs = document.querySelector(".page-vacation main > [data-tabs]");
  var map;
  var german = document.documentElement.lang === "de";

  function selectTab(root, name) {
    if (!root) return;
    root.querySelectorAll("[data-tab]").forEach(function (button) {
      if (button.closest("[data-tabs]") !== root) return;
      var active = button.dataset.tab === name;
      button.classList.toggle("is-active", active);
      button.setAttribute("aria-selected", String(active));
    });
    root.querySelectorAll("[data-tab-panel]").forEach(function (panel) {
      if (panel.parentElement.closest("[data-tabs]") !== root) return;
      panel.classList.toggle("is-active", panel.dataset.tabPanel === name);
    });
    if (map) setTimeout(function () { map.invalidateSize(); }, 0);
  }

  function plannerView(view) {
    var root = document.querySelector("[data-tagesplan]");
    if (!root) return;
    root.querySelector("[data-day-view]").hidden = view !== "day";
    root.querySelector("[data-weekview]").hidden = view !== "week";
    root.querySelectorAll("[data-view]").forEach(function (button) {
      button.classList.toggle("is-active", button.dataset.view === view);
      button.setAttribute("aria-pressed", String(button.dataset.view === view));
    });
  }

  function activateHash() {
    var hash = location.hash.slice(1);
    if (!hash || !mainTabs) return;
    var booking = /^budget-source-(item|lodging|travel)-([0-9a-f-]+)$/i.exec(hash);
    if (booking) {
      var kind = booking[1];
      var target = document.getElementById(kind + "-" + booking[2]);
      if (!target && kind === "travel") {
        target = document.querySelector('[data-travel-source-id="' + booking[2] + '"]');
      }
      if (!target && kind === "item") {
        target = document.querySelector('[data-day-view] [data-id="' + booking[2] + '"]');
      }
      if (target) {
        var dayPanel = target.closest("[data-day-view] [data-tab-panel]");
        selectTab(mainTabs, dayPanel ? "tagesplan" : kind === "item" ? "ideen" : kind);
        if (dayPanel) {
          plannerView("day");
          selectTab(document.querySelector("[data-day-view]"), dayPanel.dataset.tabPanel);
        }
        var details = target.closest("details");
        if (details) details.open = true;
        target.setAttribute("tabindex", "-1");
        target.focus({preventScroll: true});
        target.scrollIntoView({block: "center"});
      }
      return;
    }
    if (/^day-\d+$/.test(hash) || hash === "day" || hash === "day-planner") {
      selectTab(mainTabs, "tagesplan");
      plannerView("day");
      selectTab(document.querySelector("[data-day-view]"), /^day-\d+$/.test(hash) ? hash : "day-0");
      return;
    }
    if (hash === "week" || hash === "week-planner") {
      selectTab(mainTabs, "tagesplan");
      plannerView("week");
      return;
    }
    if (hash === "ideas") hash = "ideen";
    if (Array.from(mainTabs.querySelectorAll("[data-tab]")).some(function (tab) {
      return tab.closest("[data-tabs]") === mainTabs && tab.dataset.tab === hash;
    })) selectTab(mainTabs, hash);
  }

  document.addEventListener("click", function (event) {
    var control = event.target.closest("button, a");
    if (!control || control.disabled || control.getAttribute("aria-disabled") === "true") return;
    if (control.hasAttribute("data-tab")) {
      selectTab(control.closest("[data-tabs]"), control.dataset.tab);
      location.hash = control.dataset.tab;
    } else if (control.hasAttribute("data-view")) {
      plannerView(control.dataset.view);
      location.hash = control.dataset.view === "day" ? "day-0" : "week";
    } else if (control.hasAttribute("data-goto-day")) {
      location.hash = "day-" + control.dataset.gotoDay;
      activateHash();
    } else if (control.hasAttribute("data-payer-filter")) {
      var root = control.closest("[data-tab-panel]") || document;
      var payer = control.dataset.payerFilter;
      root.querySelectorAll("[data-payer]").forEach(function (row) {
        row.hidden = payer !== "" && row.dataset.payer !== payer;
      });
      root.querySelectorAll("[data-payer-filter]").forEach(function (chip) {
        chip.classList.toggle("is-active", chip === control);
        chip.setAttribute("aria-pressed", String(chip === control));
      });
    } else if (control.hasAttribute("data-print")) {
      window.print();
    }
  });
  document.addEventListener("change", function (event) {
    if (!event.target.matches("[data-ideas-region-filter]")) return;
    var region = event.target.value;
    document.querySelectorAll("[data-ideas-region-filter]").forEach(function (select) {
      select.value = region;
      var root = select.closest("[data-ideas-regions]");
      var visible = 0;
      root.querySelectorAll("[data-ideas-region-group]").forEach(function (group) {
        group.hidden = region !== "*" && group.dataset.ideasRegionGroup !== "region:" &&
          group.dataset.ideasRegionGroup !== region;
        if (!group.hidden) visible++;
      });
      var empty = root.querySelector("[data-ideas-region-empty]");
      if (empty) empty.hidden = visible !== 0;
    });
  });
  window.addEventListener("hashchange", activateHash);

  var mapElement = document.getElementById("map");
  if (mapElement && window.L) {
    var data = window.VP_DEMO_MAP || {};
    var center = data.center || {lat: 46, lng: 11};
    var lodgings = (data.lodgings || []).filter(function (lodging) {
      return Number.isFinite(lodging.lat) && Number.isFinite(lodging.lng);
    });
    var points = lodgings.map(function (lodging) { return [lodging.lat, lodging.lng]; });
    points.push([center.lat, center.lng]);
    var bounds = L.latLngBounds(points).pad(0.7);
    if (bounds.getNorth() - bounds.getSouth() < 0.04 || bounds.getEast() - bounds.getWest() < 0.04) {
      bounds = L.latLngBounds([[center.lat - 0.08, center.lng - 0.12], [center.lat + 0.08, center.lng + 0.12]]);
    }
    map = L.map(mapElement, {scrollWheelZoom: false, attributionControl: false, minZoom: 7, maxZoom: 16});
    L.imageOverlay("../../static/demo/map.svg", bounds).addTo(map);
    var size = map.getSize();
    map.fitBounds(L.latLngBounds(points), {
      padding: [Math.max(30, Math.ceil(size.x * 0.15)), Math.max(30, Math.ceil(size.y * 0.15))],
      maxZoom: 13
    });
    lodgings.forEach(function (lodging) {
      var label = document.createElement("span");
      label.textContent = lodging.title + (german ? " · Beispielunterkunft" : " · Sample accommodation");
      L.marker([lodging.lat, lodging.lng], {
        icon: L.divIcon({className: "lodging-marker", html: "\u{1f6cf}", iconSize: [28, 28], iconAnchor: [14, 14]}),
        title: lodging.title
      })
        .bindPopup(label).addTo(map);
    });
    document.querySelectorAll("[data-focus-map]").forEach(function (row) {
      row.style.cursor = "pointer";
      row.setAttribute("tabindex", "0");
      row.setAttribute("role", "button");
      function focusMap() {
        if (!Number.isFinite(Number(row.dataset.lat)) || !Number.isFinite(Number(row.dataset.lng))) return;
        selectTab(mainTabs, "overview");
        map.setView([Number(row.dataset.lat), Number(row.dataset.lng)], 12);
        mapElement.scrollIntoView({block: "nearest"});
      }
      row.addEventListener("click", focusMap);
      row.addEventListener("keydown", function (event) {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          focusMap();
        }
      });
    });
  }
  activateHash();
}());

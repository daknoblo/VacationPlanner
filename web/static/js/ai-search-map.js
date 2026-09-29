/* Shared read-only accommodation selection for the live and offline AI maps. */
(function () {
  "use strict";
  window.VPAISearchMap = function (map, select, radius) {
    var lodgings = L.layerGroup().addTo(map);
    var circle = null;

    function point(option) {
      if (!option || option.disabled || option.dataset.unavailable === "true" ||
          !option.value.startsWith("lodging:") || !option.dataset.lat || !option.dataset.lng) return null;
      var lat = Number(option.dataset.lat), lng = Number(option.dataset.lng);
      return Number.isFinite(lat) && Number.isFinite(lng) && Math.abs(lat) <= 90 && Math.abs(lng) <= 180 ?
        [lat, lng] : null;
    }

    function fitRadius() {
      var center = point(select.selectedOptions[0]);
      if (!center || !radius) {
        if (circle) { circle.remove(); circle = null; }
        return;
      }
      var meters = Number(radius.value) * 1000;
      if (circle) circle.setLatLng(center).setRadius(meters);
      else circle = L.circle(center, {
        radius: meters, color: "#2563eb", weight: 2, dashArray: "4 4", fillOpacity: 0.05, interactive: false
      }).addTo(map);
      var size = map.getContainer().getBoundingClientRect();
      if (size.width && size.height) {
        map.fitBounds(circle.getBounds(), { padding: [24, 24], maxZoom: 15, animate: false });
      }
    }

    function refresh() {
      lodgings.clearLayers();
      Array.from(select.options).forEach(function (option) {
        var coordinates = point(option);
        if (!coordinates) return;
        var label = document.createElement("span");
        label.textContent = option.textContent;
        var marker = L.marker(coordinates, {
          icon: L.divIcon({
            className: "lodging-marker ai-search-lodging",
            html: "\u{1f6cf}", iconSize: [28, 28], iconAnchor: [14, 14]
          }),
          title: option.textContent, zIndexOffset: 1000, bubblingMouseEvents: false
        }).bindTooltip(label).addTo(lodgings);
        marker.getElement().setAttribute("aria-label", option.textContent);
        marker.getElement().dataset.aiLodging = option.value;
        function choose() {
          select.value = option.value;
          select.dispatchEvent(new Event("change", { bubbles: true }));
        }
        marker.on("click", choose);
        marker.getElement().addEventListener("keydown", function (event) {
          if (event.key !== "Enter" && event.key !== " ") return;
          event.preventDefault();
          event.stopPropagation();
          choose();
        });
      });
      fitRadius();
    }

    select.addEventListener("change", fitRadius);
    select.addEventListener("ai:centers-refreshed", refresh);
    if (radius) radius.addEventListener("input", fitRadius);
    new ResizeObserver(function () {
      map.invalidateSize({ animate: false });
      fitRadius();
    }).observe(map.getContainer());
    refresh();
    return { update: fitRadius };
  };
}());

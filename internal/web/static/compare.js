// Overlay the gravity curves of several fermentations by days since start.
// Ticking a box redraws immediately and keeps ?ids= in the address bar.
(function () {
  "use strict";
  var el = document.getElementById("compare-chart");
  var form = document.getElementById("compare-form");
  if (!el || !window.uPlot) return;
  var note = document.getElementById("compare-note");
  var norm = document.getElementById("normalise");
  var MAX = 8;

  var css = getComputedStyle(document.documentElement);
  var fg = css.getPropertyValue("--fg").trim(), line = css.getPropertyValue("--line").trim();
  // Okabe-Ito palette: distinguishable for colour-blind viewers, fine on light and dark.
  var palette = ["#E69F00", "#56B4E9", "#009E73", "#F0E442", "#0072B2", "#D55E00", "#CC79A7", "#999999"];

  var cache = {}, names = {}, plot = null, unit = "SG";

  function json(url) {
    return fetch(url, { headers: { Accept: "application/json" } }).then(function (r) {
      if (!r.ok) throw new Error(url + ": " + r.status);
      return r.json();
    });
  }
  function height() {
    if (window.innerWidth < 768) return 280;
    return Math.round(Math.max(320, Math.min(640, window.innerHeight * 0.55)));
  }
  function boxes() { return form ? Array.prototype.slice.call(form.querySelectorAll("input[type=checkbox][value]")) : []; }
  function selected() { return boxes().filter(function (c) { return c.checked; }).map(function (c) { return c.value; }); }

  // Break lines across reporting gaps instead of drawing a ramp.
  function breakGaps(t, g) {
    if (t.length < 3) return [t, g];
    var d = [];
    for (var i = 1; i < t.length; i++) d.push(t[i] - t[i - 1]);
    d.sort(function (a, b) { return a - b; });
    var thr = Math.max(3 * d[d.length >> 1], 3600 / 86400);
    var x = [t[0]], y = [g[0]];
    for (var j = 1; j < t.length; j++) {
      if (t[j] - t[j - 1] > thr) { x.push(t[j - 1] + 1 / 86400); y.push(null); }
      x.push(t[j]); y.push(g[j]);
    }
    return [x, y];
  }
  function attenuation(g) {
    var base = null;
    for (var i = 0; i < g.length && base == null; i++) if (g[i] != null) base = g[i];
    if (base == null) return g;
    return g.map(function (v) {
      if (v == null) return null;
      return unit === "SG" ? (base - v) / (base - 1) * 100 : (base - v) / base * 100;
    });
  }

  function draw(ids) {
    var tables = [], series = [{}], pct = norm && norm.checked;
    ids.forEach(function (id, i) {
      var s = cache[id];
      if (!s || !s.t.length) return;
      unit = s.gravityUnit;
      var t0 = s.t[0];
      var t = s.t.map(function (x) { return (x - t0) / 86400; });
      var g = pct ? attenuation(s.gravity) : s.gravity;
      tables.push(breakGaps(t, g));
      series.push({
        label: names[id] || ("#" + id), stroke: palette[i % palette.length], width: 2,
        value: function (u, v) { return v == null ? "–" : (pct ? v.toFixed(0) + "%" : v.toFixed(unit === "SG" ? 4 : 2)); }
      });
    });
    if (plot) { plot.destroy(); plot = null; }
    if (!tables.length) { note.textContent = ids.length ? "No readings." : "Pick fermentations to compare."; return; }
    note.textContent = "";
    plot = new uPlot({
      width: el.clientWidth || 600, height: height(),
      scales: { x: { time: false } },
      axes: [
        { stroke: fg, grid: { stroke: line }, ticks: { stroke: line }, label: "days since start",
          values: function (u, v) { return v.map(function (x) { return +x.toFixed(1); }); } },
        { stroke: fg, grid: { stroke: line }, ticks: { stroke: line },
          values: function (u, v) { return v.map(function (x) { return pct ? x.toFixed(0) + "%" : unit === "SG" ? x.toFixed(3) : x.toFixed(1); }); } }
      ],
      series: series,
      cursor: { drag: { x: true, y: false, setScale: true } }
    }, uPlot.join(tables), el);
  }

  function refresh() {
    var ids = selected().slice(0, MAX);
    var full = ids.length >= MAX;
    boxes().forEach(function (c) { c.disabled = full && !c.checked; });
    if (history.replaceState) history.replaceState(null, "", ids.length ? "?ids=" + ids.join(",") : location.pathname);
    var missing = ids.filter(function (id) { return !cache[id]; });
    Promise.all(missing.map(function (id) {
      return json(el.dataset.series.replace("{id}", id)).then(function (s) { cache[id] = s; });
    })).then(function () { draw(ids); })
      .catch(function () { note.textContent = "Could not load the fermentations."; });
  }

  document.documentElement.classList.add("js");
  boxes().forEach(function (c) { c.addEventListener("change", refresh); });
  if (norm) norm.addEventListener("change", refresh);
  if (window.ResizeObserver) {
    var lastW = 0;
    new ResizeObserver(function () {
      var w = el.clientWidth;
      if (plot && w !== lastW) { lastW = w; plot.setSize({ width: w, height: height() }); }
    }).observe(el);
  }

  json(el.dataset.list).then(function (list) {
    list.forEach(function (f) { names[f.id] = f.name; });
    refresh();
  }).catch(function () { note.textContent = "Could not load the fermentations."; });
})();

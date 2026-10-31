// Fermentation graph: gravity + temperature, optional battery/RSSI, live
// updates. Drag on the plot to zoom; presets and "Reset zoom" change the
// window; an admin can act on the zoomed window (exclude / split) or click the
// plot to add an event at that time.
(function () {
  "use strict";
  var el = document.getElementById("chart");
  if (!el || !window.uPlot) return;
  var isAdmin = el.dataset.admin === "1";

  var theme;
  function readTheme() {
    var css = getComputedStyle(document.documentElement);
    var c = function (n) { return css.getPropertyValue(n).trim(); };
    theme = { fg: c("--fg"), muted: c("--muted"), line: c("--line"), surface: c("--surface"),
      gravity: c("--gravity"), temp: c("--temp"), health: c("--health") };
  }
  readTheme();
  var sync = uPlot.sync("fermentation");

  var main = null, health = null;
  var state = { series: null, showExcluded: false, showHealth: false };
  // view.mode: "all" (whole batch), "preset" (last view.span seconds), "zoom" (user drag).
  var view = { mode: "all", span: 0 };
  var expected = null; // the x range we last set ourselves
  var gapThr = 3600;
  var healthEl = document.getElementById("chart-health");
  var liveStatus = document.getElementById("live-status");
  var presets = document.querySelector(".presets");
  var resetBtn = document.getElementById("reset-zoom");
  var rangeBar = document.getElementById("range-bar");
  var rangeText = document.getElementById("range-text");

  function width() { return el.clientWidth || 600; }
  function height() {
    if (window.innerWidth < 768) return 260;
    return Math.round(Math.max(320, Math.min(640, window.innerHeight * 0.55)));
  }

  // ---- data shaping ----
  function gapThreshold(t) {
    if (t.length < 3) return 3600;
    var d = [];
    for (var i = 1; i < t.length; i++) d.push(t[i] - t[i - 1]);
    d.sort(function (a, b) { return a - b; });
    return Math.max(3 * d[d.length >> 1], 3600);
  }
  // Insert a null row inside every reporting gap so lines break instead of
  // drawing a straight ramp across hours of missing data.
  function breakGaps(t, cols, thr) {
    var x = t.length ? [t[0]] : [], out = cols.map(function (c) { return c.length ? [c[0]] : []; });
    for (var i = 1; i < t.length; i++) {
      if (t[i] - t[i - 1] > thr) {
        x.push(t[i - 1] + 1);
        out.forEach(function (o) { o.push(null); });
      }
      x.push(t[i]);
      cols.forEach(function (c, k) { out[k].push(c[i]); });
    }
    return [x].concat(out);
  }
  function mainData(s) {
    var n = s.t.length, g = new Array(n), x = new Array(n);
    for (var i = 0; i < n; i++) {
      var ex = s.excluded && s.excluded[i];
      g[i] = ex ? null : s.gravity[i];
      x[i] = ex ? s.gravity[i] : null;
    }
    gapThr = gapThreshold(s.t);
    var cols = [g, s.temp];
    if (s.excluded) cols.push(x);
    return breakGaps(s.t, cols, gapThr);
  }

  function fmtTime(sec) {
    return new Date(sec * 1000).toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
  }
  // Legend value: blank when the cursor is far from the nearest real point.
  function legendValue(fmt) {
    return function (u, v, si, idx) {
      if (v == null || idx == null) return "–";
      if (u.cursor.left >= 0 && Math.abs(u.posToVal(u.cursor.left, "x") - u.data[0][idx]) > gapThr) return "–";
      return fmt(v);
    };
  }

  // ---- view (zoom) ----
  function dataRange() {
    var t = state.series.t;
    return t.length ? [t[0], t[t.length - 1]] : null;
  }
  function applyView() {
    var r = main && state.series && dataRange();
    if (!r) return;
    var min = r[0], max = r[1];
    if (view.mode === "preset" && view.span) min = Math.max(r[0], r[1] - view.span);
    if (max <= min) max = min + 60;
    expected = { min: min, max: max };
    main.setScale("x", { min: min, max: max });
    updateUI();
  }
  function setView(mode, span) { view.mode = mode; view.span = span || 0; applyView(); }

  function updateUI() {
    if (resetBtn) resetBtn.hidden = view.mode === "all";
    if (presets) presets.querySelectorAll("[data-span]").forEach(function (b) {
      var sp = +b.dataset.span;
      b.setAttribute("aria-pressed", String((view.mode === "all" && sp === 0) || (view.mode === "preset" && view.span === sp)));
    });
    if (!rangeBar || !main) return;
    rangeBar.hidden = view.mode !== "zoom";
    if (view.mode === "zoom") {
      var sc = main.scales.x;
      rangeText.textContent = fmtTime(sc.min) + " → " + fmtTime(sc.max);
    }
  }

  function onScale(u, key) {
    if (key !== "x") return;
    var sc = u.scales.x, r = state.series && dataRange();
    if (health) health.setScale("x", { min: sc.min, max: sc.max });
    var mine = expected && Math.abs(sc.min - expected.min) < 2 && Math.abs(sc.max - expected.max) < 2;
    if (!mine && r) {
      // A drag zoom, or a double-click reset back to the full range.
      var full = Math.abs(sc.min - r[0]) < 2 && Math.abs(sc.max - r[1]) < 2;
      view.mode = full ? "all" : "zoom";
      expected = { min: sc.min, max: sc.max };
    }
    updateUI();
  }

  // ---- charts ----
  function buildMain(s) {
    if (main) main.destroy();
    var series = [
      { value: function (u) { return u.cursor.left >= 0 ? fmtTime(u.posToVal(u.cursor.left, "x")) : "–"; } },
      { label: "Gravity (" + s.gravityUnit + ")", scale: "g", stroke: theme.gravity, width: 2,
        value: legendValue(function (v) { return v.toFixed(s.gravityUnit === "SG" ? 4 : 2); }) },
      { label: "Temp (" + s.tempUnit + ")", scale: "t", stroke: theme.temp, width: 1.5,
        value: legendValue(function (v) { return v.toFixed(1); }) }
    ];
    if (s.excluded) series.push({ label: "Excluded", scale: "g", stroke: theme.muted, paths: function () { return null; },
      points: { show: true, size: 4, fill: theme.muted, stroke: theme.muted } });
    main = new uPlot({
      width: width(), height: height(),
      scales: { x: { time: true }, g: { auto: true }, t: { auto: true } },
      axes: [
        { stroke: theme.fg, grid: { stroke: theme.line }, ticks: { stroke: theme.line } },
        { scale: "g", stroke: theme.gravity, grid: { stroke: theme.line }, ticks: { stroke: theme.line }, size: 76,
          values: function (u, vals) {
            // Enough decimals that neighbouring ticks stay distinct when zoomed in.
            var step = vals.length > 1 ? Math.abs(vals[1] - vals[0]) : 1;
            var d = Math.min(5, Math.max(s.gravityUnit === "SG" ? 3 : 1, Math.ceil(-Math.log10(step || 1))));
            return vals.map(function (v) { return v.toFixed(d); });
          } },
        { scale: "t", side: 1, stroke: theme.temp, grid: { show: false }, ticks: { stroke: theme.line } }
      ],
      series: series,
      cursor: { sync: { key: sync.key }, drag: { x: true, y: false, setScale: true } },
      hooks: { setScale: [onScale], draw: [drawMarkers] }
    }, mainData(s), el);
    el.uplot = main; // exposed for browser-level tests
    bindClick(main);
    applyView();
  }

  function buildHealth(s) {
    if (health) health.destroy();
    var d = breakGaps(s.t, [s.battery, s.rssi], gapThr);
    health = new uPlot({
      width: width(), height: 160,
      scales: { x: { time: true }, b: { auto: true }, r: { auto: true } },
      axes: [
        { stroke: theme.fg, grid: { stroke: theme.line }, ticks: { stroke: theme.line } },
        { scale: "b", stroke: theme.health, grid: { stroke: theme.line }, ticks: { stroke: theme.line },
          values: function (u, v) { return v.map(function (x) { return x.toFixed(2) + " V"; }); }, size: 70 },
        { scale: "r", side: 1, stroke: theme.muted, grid: { show: false }, ticks: { stroke: theme.line },
          values: function (u, v) { return v.map(function (x) { return x + " dBm"; }); }, size: 80 }
      ],
      series: [{ value: function (u) { return u.cursor.left >= 0 ? fmtTime(u.posToVal(u.cursor.left, "x")) : "–"; } },
               { label: "Battery (V)", scale: "b", stroke: theme.health, width: 1.5 },
               { label: "RSSI (dBm)", scale: "r", stroke: theme.muted, width: 1 }],
      cursor: { sync: { key: sync.key }, drag: { x: false, y: false } }
    }, d, healthEl);
    if (main) health.setScale("x", { min: main.scales.x.min, max: main.scales.x.max });
  }

  // ---- manual measurements and timeline events, drawn over the plot ----
  function drawMarkers(u) {
    var s = state.series;
    if (!s) return;
    var ctx = u.ctx, bb = u.bbox, dpr = window.devicePixelRatio || 1;
    ctx.save();
    ctx.beginPath();
    ctx.rect(bb.left, bb.top, bb.width, bb.height);
    ctx.clip();
    ctx.font = (11 * dpr) + "px system-ui, sans-serif";
    ctx.textBaseline = "top";
    (s.annotations || []).forEach(function (a, i) {
      var x = u.valToPos(a.t, "x", true);
      if (x < bb.left || x > bb.left + bb.width) return;
      ctx.strokeStyle = theme.muted;
      ctx.lineWidth = dpr;
      ctx.setLineDash([4 * dpr, 4 * dpr]);
      ctx.beginPath(); ctx.moveTo(x, bb.top); ctx.lineTo(x, bb.top + bb.height); ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = theme.fg;
      ctx.fillText(a.note, x + 4 * dpr, bb.top + (2 + (i % 3) * 14) * dpr);
    });
    (s.manual || []).forEach(function (m) {
      var x = u.valToPos(m.t, "x", true), y = u.valToPos(m.gravity, "g", true);
      if (x < bb.left || x > bb.left + bb.width) return;
      ctx.beginPath();
      ctx.arc(x, y, 5 * dpr, 0, 2 * Math.PI);
      ctx.fillStyle = theme.surface || "#fff";
      ctx.fill();
      ctx.lineWidth = 2 * dpr;
      ctx.strokeStyle = theme.gravity;
      ctx.stroke();
      if (m.kind) {
        ctx.fillStyle = theme.fg;
        ctx.fillText(m.kind, x + 8 * dpr, y - 6 * dpr);
      }
    });
    ctx.restore();
  }

  // ---- admin: act on the zoomed window, click to add an event ----
  function pad(n) { return (n < 10 ? "0" : "") + n; }
  function localInput(sec) {
    var d = new Date(sec * 1000);
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) + "T" +
      pad(d.getHours()) + ":" + pad(d.getMinutes()) + ":" + pad(d.getSeconds());
  }
  function openSheet(id) {
    var s = document.getElementById(id);
    if (s && s.showPopover) s.showPopover();
  }
  function windowBounds() {
    var sc = main.scales.x, r = dataRange();
    return [Math.floor(Math.max(sc.min, r[0])), Math.ceil(Math.min(sc.max, r[1]))];
  }
  if (rangeBar) {
    var rf = rangeBar.querySelector("form");
    rf.addEventListener("submit", function () {
      var b = windowBounds();
      rf.elements.from.value = localInput(b[0]);
      rf.elements.to.value = localInput(b[1]);
    });
    document.getElementById("range-split").addEventListener("click", function () {
      var f = document.getElementById("split-form");
      if (f) f.elements.at.value = localInput(windowBounds()[0]);
      openSheet("sheet-split");
    });
  }
  var clickTimer = null, downX = 0, downY = 0;
  function bindClick(u) {
    if (!isAdmin) return;
    u.over.addEventListener("mousedown", function (e) { downX = e.clientX; downY = e.clientY; });
    u.over.addEventListener("dblclick", function () { clearTimeout(clickTimer); });
    u.over.addEventListener("click", function (e) {
      if (Math.abs(e.clientX - downX) > 3 || Math.abs(e.clientY - downY) > 3 || u.cursor.left < 0) return;
      var t = u.posToVal(u.cursor.left, "x");
      clearTimeout(clickTimer);
      clickTimer = setTimeout(function () { // wait to rule out a double-click reset
        var f = document.querySelector("#sheet-event form");
        if (f) f.elements.at.value = localInput(Math.round(t));
        openSheet("sheet-event");
      }, 260);
    });
  }

  if (presets) {
    presets.hidden = false; // controls need the script
    presets.querySelectorAll("[data-span]").forEach(function (b) {
      b.addEventListener("click", function () {
        var sp = +b.dataset.span;
        setView(sp ? "preset" : "all", sp);
      });
    });
  }
  if (resetBtn) resetBtn.addEventListener("click", function () { setView("all"); });

  // ---- data loading ----
  function load() {
    var url = el.dataset.series + (state.showExcluded ? "?excluded=1" : "");
    return fetch(url, { headers: { Accept: "application/json" } })
      .then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
      .then(function (s) {
        state.series = s;
        buildMain(s);
        if (state.showHealth) buildHealth(s);
      })
      .catch(function () { el.textContent = "Could not load readings."; });
  }

  var tgHealth = document.getElementById("toggle-health");
  if (tgHealth) tgHealth.addEventListener("change", function () {
    state.showHealth = tgHealth.checked;
    healthEl.hidden = !state.showHealth;
    if (state.showHealth && state.series) buildHealth(state.series);
  });
  var tgEx = document.getElementById("toggle-excluded");
  if (tgEx) tgEx.addEventListener("change", function () { state.showExcluded = tgEx.checked; load(); });

  // Follow the container (grid column), not just the window.
  if (window.ResizeObserver) {
    var lastW = 0;
    new ResizeObserver(function () {
      var w = width();
      if (w === lastW) return;
      lastW = w;
      if (main) main.setSize({ width: w, height: height() });
      if (health) health.setSize({ width: w, height: 160 });
    }).observe(el);
  } else {
    window.addEventListener("resize", function () {
      if (main) main.setSize({ width: width(), height: height() });
      if (health) health.setSize({ width: width(), height: 160 });
    });
  }

  // Re-read the colour tokens when the OS theme flips.
  if (window.matchMedia) window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", function () {
    readTheme();
    if (state.series) { buildMain(state.series); if (state.showHealth) buildHealth(state.series); }
  });

  // ---- live updates ----
  function append(p) {
    var s = state.series;
    if (!s) return;
    s.t.push(p.t); s.gravity.push(p.gravity); s.temp.push(p.temp);
    s.battery.push(p.battery); s.rssi.push(p.rssi);
    if (s.excluded) s.excluded.push(false);
    if (main) {
      main.setData(mainData(s), false); // keep the current window
      if (view.mode !== "zoom") applyView(); else main.redraw(false);
    }
    if (health) {
      health.setData(breakGaps(s.t, [s.battery, s.rssi], gapThr), false);
      health.setScale("x", { min: main.scales.x.min, max: main.scales.x.max });
    }
  }
  function connect() {
    if (!el.dataset.live || !window.EventSource) return;
    var es = new EventSource(el.dataset.stream);
    es.onopen = function () { if (liveStatus) { liveStatus.textContent = "live"; liveStatus.className = "muted on"; } };
    es.onerror = function () { if (liveStatus) { liveStatus.textContent = "reconnecting…"; liveStatus.className = "muted"; } };
    es.addEventListener("reading", function (e) { append(JSON.parse(e.data)); });
    // Something structural changed (closed, split, merged...): reload the page.
    // Not while a form is posting: that response is already navigating us.
    var submitting = false;
    document.addEventListener("submit", function () { submitting = true; });
    es.addEventListener("changed", function () { if (!submitting) window.location.reload(); });
  }

  load().then(connect);
})();

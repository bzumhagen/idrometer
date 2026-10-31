// Small progressive enhancements. Pages work without this script.
(function () {
  "use strict";

  document.documentElement.classList.add("js");

  // Show <time datetime=...> in the browser's own time zone.
  document.querySelectorAll("time[datetime]").forEach(function (el) {
    var d = new Date(el.getAttribute("datetime"));
    if (!isNaN(d)) {
      el.textContent = d.toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
      el.title = el.getAttribute("datetime");
    }
  });

  // Tell the server our UTC offset so datetime-local inputs mean local time.
  // "tz" is today's offset; on submit each datetime-local field also gets its
  // own <name>_tz, because the offset differs across a DST change.
  document.querySelectorAll("input[name=tz]").forEach(function (el) {
    el.value = String(new Date().getTimezoneOffset());
    var f = el.form;
    if (!f) return;
    f.addEventListener("submit", function () {
      f.querySelectorAll("input[type=datetime-local][name]").forEach(function (field) {
        var d = new Date(field.value);
        if (isNaN(d)) return;
        var n = field.name + "_tz";
        var h = f.querySelector('input[type=hidden][name="' + n + '"]');
        if (!h) {
          h = document.createElement("input");
          h.type = "hidden";
          h.name = n;
          f.appendChild(h);
        }
        h.value = String(d.getTimezoneOffset());
      });
    });
  });

  // Default "when" fields to the current local time.
  function pad(n) { return (n < 10 ? "0" : "") + n; }
  var now = new Date();
  var nowLocal = now.getFullYear() + "-" + pad(now.getMonth() + 1) + "-" + pad(now.getDate()) + "T" +
    pad(now.getHours()) + ":" + pad(now.getMinutes()) + ":" + pad(now.getSeconds());
  document.querySelectorAll("input[data-now]").forEach(function (el) { if (!el.value) el.value = nowLocal; });

  // Confirm destructive actions.
  document.querySelectorAll("form[data-confirm]").forEach(function (f) {
    f.addEventListener("submit", function (e) {
      if (!window.confirm(f.dataset.confirm)) e.preventDefault();
    });
  });

  // Live dashboard: refresh the device cards when anything changes.
  var live = document.querySelector("[data-live][data-refresh]");
  if (live && window.EventSource && window.htmx) {
    var pending = null;
    var es = new EventSource(live.dataset.live);
    es.addEventListener("changed", function () {
      if (pending) return;
      pending = setTimeout(function () {
        pending = null;
        window.htmx.ajax("GET", live.dataset.refresh, { target: live, swap: "innerHTML" }).then(function () {
          live.querySelectorAll("time[datetime]").forEach(function (el) {
            var d = new Date(el.getAttribute("datetime"));
            if (!isNaN(d)) el.textContent = d.toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
          });
        });
      }, 500);
    });
  }

  // Success messages fade away; errors stay.
  document.querySelectorAll(".flash.ok").forEach(function (f) {
    setTimeout(function () { f.classList.add("gone"); }, 4000);
  });

  // Hint bubbles and the More menu sit next to the button that opened them
  // (centred / top-left without script).
  document.addEventListener("toggle", function (e) {
    var pop = e.target;
    if (!pop.classList || e.newState !== "open") return;
    var isMenu = pop.classList.contains("menu");
    if (!isMenu && !pop.classList.contains("hint-pop")) return;
    var btn = document.querySelector('[popovertarget="' + pop.id + '"]');
    if (!btn) return;
    var r = btn.getBoundingClientRect();
    pop.style.inset = "auto";
    pop.style.margin = "0";
    pop.style.top = Math.min(r.bottom + 6, window.innerHeight - pop.offsetHeight - 8) + "px";
    var left = isMenu ? r.right - pop.offsetWidth : r.left;
    pop.style.left = Math.max(8, Math.min(left, window.innerWidth - pop.offsetWidth - 8)) + "px";
  }, true);

  // Whole table rows are clickable.
  document.querySelectorAll("tr[data-href]").forEach(function (tr) {
    tr.addEventListener("click", function (e) {
      if (e.target.closest("a, button, input, select")) return;
      window.location.href = tr.dataset.href;
    });
  });

  // Live calibration preview: what the latest tilt angle would read.
  document.querySelectorAll("[data-cal]").forEach(function (box) {
    var angle = parseFloat(box.dataset.angle), dev = parseFloat(box.dataset.deviceSg);
    var out = box.querySelector("[data-preview]"), form = box.querySelector("form");
    if (!out || !form || isNaN(angle)) return;
    function num(n) { var v = parseFloat(form.elements[n].value); return isNaN(v) ? 0 : v; }
    function update() {
      var v = num("c0") + angle * (num("c1") + angle * (num("c2") + angle * num("c3")));
      var plato = form.elements.unit.value === "plato";
      var sg = plato ? 1 + v / (258.6 - (v / 258.2) * 227.1) : v;
      var any = ["c0", "c1", "c2", "c3"].some(function (n) { return form.elements[n].value.trim() !== ""; });
      out.textContent = "Latest angle " + angle.toFixed(1) + "° → " + (any ? sg.toFixed(4) + " SG" : "–") +
        (isNaN(dev) ? "" : " (device says " + dev.toFixed(4) + ")");
    }
    form.addEventListener("input", update);
    update();
  });
})();

// Fade the right edge of the nav when it scrolls sideways.
(function () {
  var nav = document.querySelector(".top nav");
  if (!nav) return;
  function check() { nav.classList.toggle("overflows", nav.scrollWidth - nav.clientWidth - nav.scrollLeft > 4); }
  nav.addEventListener("scroll", check);
  window.addEventListener("resize", check);
  check();
})();

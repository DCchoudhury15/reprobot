/* The reprobot owl bot: a geometric badge. A hexagon head with two antenna tufts,
   a goggle mask with lens eyes, a V brow, a beak and three run lamps on its chin.
   One animation loop moves every owl bot on the page. */
(function (root) {
  "use strict";
  var NS = "http://www.w3.org/2000/svg";
  var reduce = typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;
  var uid = 0;

  function hex(cx, cy, R) {
    var d = "";
    for (var i = 0; i < 6; i++) {
      var a = Math.PI / 3 * i;
      d += (i ? "L" : "M") + (cx + R * Math.cos(a)).toFixed(1) + " " + (cy + R * Math.sin(a)).toFixed(1);
    }
    return d + "Z";
  }
  var HEX = hex(100, 112, 80), HEX_IN = hex(100, 112, 68);

  function eye(cx, cy, p) {
    return '<g class="ob-eye" transform="translate(' + cx + " " + cy + ')">' +
      '<circle class="ob-sclera" r="26"/>' +
      '<g clip-path="url(#' + p + 'eye)">' +
        '<g class="ob-pupil"><circle class="ob-iris" r="16"/><circle class="ob-irisring" r="11"/><circle class="ob-pup" r="7"/><circle class="ob-glint" cx="-5" cy="-5.5" r="3"/></g>' +
        '<rect class="ob-lid ob-lidup" x="-30" y="-60" width="60" height="60"/>' +
        '<path class="ob-lid ob-liddn" d="M-30 0Q0-13 30 0V60H-30Z"/>' +
      "</g>" +
      '<circle class="ob-ring" r="26"/>' +
    "</g>";
  }

  function markup(p, mini) {
    return '<defs><clipPath id="' + p + 'eye"><circle r="25"/></clipPath></defs>' +
      (mini ? "" : '<ellipse class="ob-shadow" cx="100" cy="206" rx="64" ry="7"/>') +
      '<g class="ob-all">' +
        '<g class="ob-tuft ob-tuft-l"><path class="ob-ear" d="M36 92L24 4L98 46Z"/><path class="ob-earin" d="M44 66L36 22L80 48Z"/></g>' +
        '<g class="ob-tuft ob-tuft-r"><path class="ob-ear" d="M164 92L176 4L102 46Z"/><path class="ob-earin" d="M156 66L164 22L120 48Z"/></g>' +
        '<path class="ob-hex" d="' + HEX + '"/>' +
        '<path class="ob-hexin" d="' + HEX_IN + '"/>' +
        '<circle class="ob-bolt" cx="30" cy="112" r="4"/><circle class="ob-bolt" cx="170" cy="112" r="4"/>' +
        '<g class="ob-mask"><circle cx="72" cy="105" r="37"/><circle cx="128" cy="105" r="37"/></g>' +
        eye(72, 105, p) + eye(128, 105, p) +
        '<g class="ob-brow ob-brow-l"><path d="M42 60L100 78L100 91L40 72Z"/></g>' +
        '<g class="ob-brow ob-brow-r"><path d="M158 60L100 78L100 91L160 72Z"/></g>' +
        '<path class="ob-beak" d="M88 122L112 122L100 142Z"/><path class="ob-beakhi" d="M92 125L100 125L99 132Z"/>' +
        (mini ? "" : '<g class="ob-lamps"><rect x="73" y="155" width="15" height="10" rx="5"/><rect x="92.5" y="157" width="15" height="10" rx="5"/><rect x="112" y="155" width="15" height="10" rx="5"/></g>') +
        '<text class="ob-mark" x="172" y="44"></text>' +
      "</g>";
  }

  /* lid: upper lid edge (-26 open .. 0 closed); low: lower lid edge (34 open .. 0 closed)
     brow: slant (+ stern, - soft); raise: brow lift; tuft: tuft angle (+ perked, - drooped); tint: eye colour */
  var MOODS = {
    idle:   { lid: -30, low: 34, brow: 0, raise: 0, tuft: 0, tint: "mint" },
    scan:   { lid: -15, low: 22, brow: 6, raise: 0, tuft: 5, tint: "mint", sweep: true },
    work:   { lid: -19, low: 30, brow: 4, raise: 0, tuft: -3, tint: "gold", ly: 0.5, pulse: true },
    hungry: { lid: -30, low: 34, brow: -8, raise: 6, tuft: 12, tint: "gold", ps: 1.15 },
    alarm:  { lid: -30, low: 34, brow: -6, raise: 8, tuft: 16, tint: "melon", ps: 0.62, mark: "!" },
    happy:  { lid: -30, low: 7, brow: -6, raise: 4, tuft: 10, tint: "mint" },
    flat:   { lid: -8, low: 34, brow: 2, raise: -4, tuft: -10, tint: "dim", ly: 0.3 },
    no:     { lid: -12, low: 24, brow: 14, raise: -3, tuft: -14, tint: "melon" },
    unsure: { lid: -18, low: 34, browL: -10, browR: 8, raiseL: 7, raiseR: 0, tuftL: 10, tuftR: -8, tilt: 9, tint: "lilac", lx: 0.5, mark: "?" },
    clock:  { lid: -24, low: 34, brow: -2, raise: 3, tuft: 3, tint: "sky", orbit: true },
    sleep:  { lid: 0.5, low: 0.5, brow: 2, raise: -4, tuft: -16, tilt: 6, tint: "dim", mark: "z" },
    dizzy:  { lid: -28, low: 34, brow: -6, raise: 4, tuft: -6, tint: "lilac", orbit: true }
  };

  var bots = [];
  function el(svg, s) { return svg.querySelector(s); }
  function clamp(v, a, b) { return Math.max(a, Math.min(b, v)); }
  function approach(cur, tgt, rate, dt) { return cur + (tgt - cur) * (1 - Math.exp(-rate * dt)); }
  function f2(n) { return (Math.round(n * 100) / 100).toString(); }

  function Robot(opts) {
    opts = opts || {};
    var p = "ob" + (++uid) + "_";
    var svg = document.createElementNS(NS, "svg");
    svg.setAttribute("viewBox", opts.mini ? "14 -2 172 192" : "10 -6 180 220");
    svg.setAttribute("class", "bot" + (opts.className ? " " + opts.className : ""));
    svg.setAttribute("overflow", "visible");
    svg.setAttribute("aria-hidden", "true");
    svg.innerHTML = markup(p, opts.mini);

    var q = function (s) { return svg.querySelectorAll(s); };
    var parts = {
      all: el(svg, ".ob-all"), shadow: el(svg, ".ob-shadow"), tuftL: el(svg, ".ob-tuft-l"), tuftR: el(svg, ".ob-tuft-r"),
      browL: el(svg, ".ob-brow-l"), browR: el(svg, ".ob-brow-r"), ups: q(".ob-lidup"), dns: q(".ob-liddn"), pupils: q(".ob-pupil"),
      mark: el(svg, ".ob-mark"), lamps: q(".ob-lamps rect")
    };

    var S = { mood: "", lx: 0, ly: 0, busy: false };
    var C = { lid: -30, low: 34, bl: 0, br: 0, rl: 0, rr: 0, tl: 0, tr: 0, tilt: 0, ps: 1, px: 0, py: 0, orbit: 0, wig: 0 };
    var P = { h: 0, hv: 0, sq: 0, sv: 0, tl: 0, tv: 0 };
    var shakes = [], landing = [];
    var blinkUntil = 0, nextBlink = 0, nextIdle = 0, idleLookUntil = 0, idleLook = null, wiggleUntil = 0;
    var temp = null, pokes = [];

    var bot = {
      el: svg,
      track: !!opts.track,
      vis: true,
      mood: function (name) { if (!MOODS[name]) return bot; S.mood = name; temp = null; applyClasses(); return bot; },
      flash: function (name, ms) { if (!MOODS[name]) return bot; temp = { name: name, until: now() + (ms || 600) / 1000 }; applyClasses(); return bot; },
      look: function (x, y) { S.lx = clamp(x, -1, 1); S.ly = clamp(y, -1, 1); return bot; },
      lookAt: function (target) {
        var a = center(), b = target.getBoundingClientRect ? target.getBoundingClientRect() : target;
        var bx = b.left + (b.width || 0) / 2, by = b.top + (b.height || 0) / 2;
        return bot.look((bx - a.x) / 240, (by - a.y) / 240);
      },
      blink: function () { blinkUntil = now() + 0.15; return bot; },
      busy: function (on) { S.busy = !!on; return bot; },
      isBusy: function () { return S.busy; },
      wiggle: function (ms) { if (!reduce) wiggleUntil = now() + (ms || 500) / 1000; return bot; },
      lamps: function (states) {
        for (var i = 0; i < parts.lamps.length; i++) parts.lamps[i].setAttribute("data-s", (states && states[i]) || "");
        return bot;
      },
      hop: function (height) {
        if (reduce) return Promise.resolve();
        if (P.h <= 0.01) { P.hv = Math.sqrt(2 * 1500 * (height || 12)); P.sv -= 5; }
        return new Promise(function (r) { landing.push(r); });
      },
      squash: function (amount) { if (!reduce) P.sv += amount || -5; return bot; },
      shake: function (ms, amp) { return addShake(ms || 500, amp || 5, 9, "x"); },
      rattle: function (ms) { return addShake(ms || 400, 1.8, 22, "y"); },
      wobble: function (ms) { return addShake(ms || 600, 9, 3.4, "tilt"); },
      poke: function () {
        var t = now();
        pokes = pokes.filter(function (x) { return t - x < 2.4; });
        pokes.push(t);
        bot.hop(12); bot.wiggle(500);
        if (pokes.length >= 5) { pokes = []; if (!S.busy) bot.flash("dizzy", 1800); bot.wobble(1400); return bot; }
        if (!S.busy) bot.flash(pokes.length >= 3 ? "no" : "alarm", 700);
        return bot;
      },
      step: step
    };
    bot.pips = bot.lamps;
    bot.ruffle = bot.wiggle;
    bot.lap = function () { return bot.wiggle(400); };

    function now() { return performance.now() / 1000; }
    function addShake(ms, amp, hz, axis) {
      if (reduce) return Promise.resolve();
      return new Promise(function (r) { shakes.push({ t0: now(), dur: ms / 1000, amp: amp, hz: hz, axis: axis, done: r }); });
    }
    function center() {
      var r = svg.getBoundingClientRect(), vb = svg.viewBox.baseVal;
      return { x: r.left + r.width * ((100 - vb.x) / vb.width), y: r.top + r.height * ((105 - vb.y) / vb.height) };
    }
    function current() { return temp ? temp.name : S.mood; }
    function applyClasses() {
      var m = MOODS[current()] || MOODS.idle;
      svg.setAttribute("data-tint", m.tint);
      var mark = m.mark || "";
      if (parts.mark.textContent !== mark) {
        parts.mark.textContent = mark;
        svg.classList.remove("marked");
        if (mark) { void svg.getBoundingClientRect(); svg.classList.add("marked"); }
      }
      svg.setAttribute("data-mood", current());
    }

    function step(dt, t) {
      if (temp && t > temp.until) { temp = null; applyClasses(); }
      var m = MOODS[current()] || MOODS.idle;

      if (t > nextBlink) { nextBlink = t + 2.4 + Math.random() * 4; if (m.lid < -14 && !reduce) blinkUntil = t + 0.15; }
      if (opts.idle !== false && !S.busy && !reduce && t > nextIdle) {
        nextIdle = t + 3 + Math.random() * 4.5;
        var quiet = t - lastPointer > 2.5 || !bot.track, roll = Math.random();
        if (quiet && roll < 0.5) { idleLook = [Math.random() * 1.6 - 0.8, Math.random() * 1 - 0.4]; idleLookUntil = t + 1 + Math.random(); }
        else if (roll < 0.68) bot.wiggle(450);
        else if (roll < 0.78) bot.hop(8);
        else if (roll < 0.88) P.tv += (Math.random() < 0.5 ? -1 : 1) * 45;
      }

      var blink = t < blinkUntil, r = reduce ? 60 : (blink ? 42 : 15);
      C.lid = approach(C.lid, blink ? 0.5 : m.lid, r, dt);
      C.low = approach(C.low, blink ? 0.5 : m.low, r, dt);
      C.bl = approach(C.bl, m.browL !== undefined ? m.browL : m.brow || 0, 11, dt);
      C.br = approach(C.br, m.browR !== undefined ? m.browR : m.brow || 0, 11, dt);
      C.rl = approach(C.rl, m.raiseL !== undefined ? m.raiseL : m.raise || 0, 11, dt);
      C.rr = approach(C.rr, m.raiseR !== undefined ? m.raiseR : m.raise || 0, 11, dt);
      C.tl = approach(C.tl, m.tuftL !== undefined ? m.tuftL : m.tuft || 0, 10, dt);
      C.tr = approach(C.tr, m.tuftR !== undefined ? m.tuftR : m.tuft || 0, 10, dt);
      C.ps = approach(C.ps, m.ps || 1, 12, dt);

      var lx = S.lx, ly = S.ly;
      if (m.sweep) { lx = Math.sin(t * 2.6) * 0.9; ly = 0.2; }
      else if (m.lx !== undefined || m.ly !== undefined) { lx = m.lx !== undefined ? m.lx : lx; ly = m.ly !== undefined ? m.ly : ly; }
      else if (idleLook && t < idleLookUntil) { lx = idleLook[0]; ly = idleLook[1]; }
      C.px = approach(C.px, lx, 11, dt);
      C.py = approach(C.py, ly, 11, dt);
      C.tilt = approach(C.tilt, (m.tilt || 0) + C.px * 6, 7, dt);
      if (m.orbit && !reduce) C.orbit += dt * 6; else C.orbit = 0;

      if (P.h > 0 || P.hv > 0) {
        P.hv -= 1500 * dt; P.h += P.hv * dt;
        if (P.h <= 0) { P.sv -= Math.min(12, Math.abs(P.hv) / 24); P.h = 0; P.hv = 0; var l = landing; landing = []; l.forEach(function (f) { f(); }); }
      }
      P.sv += (-250 * P.sq - 13 * P.sv) * dt; P.sq += P.sv * dt; P.sq = clamp(P.sq, -0.16, 0.16);
      P.tv += (-160 * P.tl - 10 * P.tv) * dt; P.tl += P.tv * dt;
      var sx = 0, sy = 0, st = 0;
      for (var i = shakes.length - 1; i >= 0; i--) {
        var s = shakes[i], k = (t - s.t0) / s.dur;
        if (k >= 1) { shakes.splice(i, 1); s.done(); continue; }
        var v = s.amp * Math.sin(k * s.dur * s.hz * Math.PI * 2) * (1 - k);
        if (s.axis === "x") sx += v; else if (s.axis === "y") sy += v; else st += v;
      }
      var wig = t < wiggleUntil ? Math.sin(t * 30) * 9 : 0;
      C.wig = approach(C.wig, wig, 22, dt);
      var hover = reduce || opts.still ? 0 : Math.sin(t * 1.7) * 3;
      var pulse = m.pulse && !reduce ? Math.abs(Math.sin(t * 5)) * 2.5 : 0;

      parts.all.setAttribute("transform", "translate(" + f2(sx) + " " + f2(hover - P.h + sy - pulse) + ") rotate(" + f2(C.tilt + st + P.tl) + " 100 120) translate(100 190) scale(" + f2(1 - P.sq * 0.45) + " " + f2(1 + P.sq) + ") translate(-100 -190)");
      if (parts.shadow) parts.shadow.setAttribute("transform", "translate(100 206) scale(" + f2(1 - clamp((P.h - hover + 3) / 70, 0, 0.45)) + " 1) translate(-100 -206)");
      parts.tuftL.setAttribute("transform", "rotate(" + f2(-(C.tl + C.wig)) + " 60 60)");
      parts.tuftR.setAttribute("transform", "rotate(" + f2(C.tr + C.wig) + " 140 60)");
      parts.browL.setAttribute("transform", "translate(0 " + f2(-C.rl) + ") rotate(" + f2(-C.bl) + " 100 84)");
      parts.browR.setAttribute("transform", "translate(0 " + f2(-C.rr) + ") rotate(" + f2(C.br) + " 100 84)");
      var ox = C.orbit ? Math.cos(C.orbit) * 0.6 : 0, oy = C.orbit ? Math.sin(C.orbit) * 0.6 : 0;
      for (var e = 0; e < 2; e++) {
        parts.ups[e].setAttribute("transform", "translate(0 " + f2(C.lid) + ")");
        parts.dns[e].setAttribute("transform", "translate(0 " + f2(C.low) + ")");
        parts.pupils[e].setAttribute("transform", "translate(" + f2((C.px + ox) * 9) + " " + f2((C.py + oy) * 8) + ") scale(" + f2(C.ps) + ")");
      }
    }

    bot.mood(opts.mood || "idle");
    if (opts.poke) {
      svg.classList.add("pokeable");
      svg.addEventListener("click", function () { bot.poke(); });
    }
    if (typeof IntersectionObserver === "function") {
      new IntersectionObserver(function (es) { bot.vis = es[es.length - 1].isIntersecting; }, { rootMargin: "80px" }).observe(svg);
    }
    bots.push(bot);
    return bot;
  }

  var last = 0;
  function frame(ms) {
    var dt = last ? Math.min(0.05, (ms - last) / 1000) : 0.016;
    last = ms;
    for (var i = 0; i < bots.length; i++) if (bots[i].vis) bots[i].step(dt, ms / 1000);
    requestAnimationFrame(frame);
  }
  if (typeof requestAnimationFrame === "function") requestAnimationFrame(frame);

  var lastPointer = -10, px = 0, py = 0, queued = false;
  function onMove(e) {
    px = e.clientX; py = e.clientY; lastPointer = performance.now() / 1000;
    if (queued) return;
    queued = true;
    requestAnimationFrame(function () {
      queued = false;
      bots.forEach(function (b) { if (b.track && b.vis && !b.isBusy()) b.lookAt({ left: px, top: py, width: 0, height: 0 }); });
    });
  }
  if (typeof window !== "undefined") window.addEventListener("pointermove", onMove, { passive: true });

  Robot.moods = Object.keys(MOODS);
  root.Robot = Robot;
})(this);

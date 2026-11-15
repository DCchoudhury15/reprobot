/* reprobot site. Every verdict, run and signature on this page comes from data.js
   (a recorded run of the real tool) or parse.js (a port of the Go parser and verdict code). */
(function () {
  "use strict";
  var D = window.RB_DATA, R = D.reports, RB = window.RB, Robot = window.Robot;
  var GH = "https://github.com/DCchoudhury15/reprobot/blob/main/";
  var reduce = matchMedia("(prefers-reduced-motion: reduce)").matches;

  function $(s, r) { return (r || document).querySelector(s); }
  function $$(s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); }
  function esc(t) { return String(t).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;"); }
  function rnd(a, b) { return a + Math.floor(Math.random() * (b - a + 1)); }
  function pad(n) { return (n < 10 ? "0" : "") + n; }
  function wait(ms) { return new Promise(function (r) { setTimeout(r, reduce ? Math.min(ms, 40) : ms); }); }
  function seen(el, cb, th) {
    if (!("IntersectionObserver" in window)) { cb(); return; }
    var io = new IntersectionObserver(function (es) {
      if (es.some(function (e) { return e.isIntersecting; })) { io.disconnect(); cb(); }
    }, { threshold: th || 0.3 });
    io.observe(el);
  }

  var V = {
    REPRODUCED_AS_CLAIMED: { s: "Real crash", c: "real", mood: "alarm" },
    REPRODUCED_DIFFERENT: { s: "Other crash", c: "diff", mood: "unsure" },
    FLAKY: { s: "Flaky", c: "diff", mood: "unsure" },
    NOT_REPRODUCED: { s: "No crash", c: "none", mood: "flat" },
    TIMEOUT: { s: "Hangs", c: "time", mood: "clock" },
    OOM: { s: "Out of memory", c: "time", mood: "clock" },
    INVALID_INPUT: { s: "Rejected", c: "bad", mood: "no" },
    OUT_OF_SCOPE: { s: "Out of scope", c: "none", mood: "flat" },
    BUILD_FAILED: { s: "Build failed", c: "bad", mood: "no" }
  };
  function runState(run) { return run.crash ? "crash" : run.timedOut ? "hang" : "ok"; }
  function stamp(state) { var v = V[state]; return '<span class="stamp v-' + v.c + '">' + esc(v.s) + "</span>"; }
  var totalMs = R.reduce(function (s, r) { return s + (r.wallMs || 0); }, 0);
  $("#heroTime").textContent = (totalMs / 1000).toFixed(1) + " s";

  /* ===================================================================
     confetti
     =================================================================== */
  var cv = $("#confetti"), cx = cv.getContext("2d"), bits = [], cvOn = false;
  var COLS = ["#ff5d73", "#5fe0b5", "#f5c451", "#b59cff", "#7cc4ff", "#f3ece0"];
  function sizeCv() { cv.width = innerWidth * devicePixelRatio; cv.height = innerHeight * devicePixelRatio; }
  sizeCv();
  window.addEventListener("resize", sizeCv);
  function burst(el, n) {
    if (reduce || !el) return;
    var r = el.getBoundingClientRect(), x = r.left + r.width / 2, y = r.top + r.height / 2;
    for (var i = 0; i < (n || 70); i++) {
      var a = Math.random() * Math.PI * 2, sp = 3 + Math.random() * 8;
      bits.push({ x: x, y: y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 5, s: 5 + Math.random() * 6, r: Math.random() * 6, vr: (Math.random() - 0.5) * 0.4, c: COLS[i % COLS.length], life: 1, sq: Math.random() < 0.55 });
    }
    if (!cvOn) { cvOn = true; requestAnimationFrame(drawCv); }
  }
  function drawCv() {
    var d = devicePixelRatio;
    cx.clearRect(0, 0, cv.width, cv.height);
    bits = bits.filter(function (p) { return p.life > 0 && p.y < innerHeight + 40; });
    bits.forEach(function (p) {
      p.vy += 0.26; p.vx *= 0.99; p.x += p.vx; p.y += p.vy; p.r += p.vr; p.life -= 0.009;
      cx.save();
      cx.globalAlpha = Math.max(0, Math.min(1, p.life * 1.6));
      cx.translate(p.x * d, p.y * d); cx.rotate(p.r); cx.fillStyle = p.c;
      if (p.sq) cx.fillRect(-p.s * d / 2, -p.s * d / 4, p.s * d, p.s * d / 2);
      else { cx.beginPath(); cx.arc(0, 0, p.s * d / 2.6, 0, 7); cx.fill(); }
      cx.restore();
    });
    if (bits.length) requestAnimationFrame(drawCv); else { cvOn = false; cx.clearRect(0, 0, cv.width, cv.height); }
  }

  /* reveal on scroll */
  if ("IntersectionObserver" in window && !reduce) {
    var ro = new IntersectionObserver(function (es) {
      es.forEach(function (e) { if (e.isIntersecting) { e.target.classList.add("in"); ro.unobserve(e.target); } });
    }, { threshold: 0.12 });
    $$(".reveal").forEach(function (el) { ro.observe(el); });
  } else $$(".reveal").forEach(function (el) { el.classList.add("in"); });

  /* ===================================================================
     owl bots in the header and footer
     =================================================================== */
  var brandBot = Robot({ mini: true, track: true, still: true });
  $("#brandBot").appendChild(brandBot.el);
  var footBot = Robot({ mood: "sleep", idle: false, poke: true });
  $("#footBot").appendChild(footBot.el);
  var footEl = $(".foot"), sleepT = 0;
  footEl.addEventListener("pointerenter", function () { clearTimeout(sleepT); footBot.mood("happy"); footBot.hop(10); });
  footEl.addEventListener("pointerleave", function () { sleepT = setTimeout(function () { footBot.mood("sleep"); }, 1200); });

  /* ===================================================================
     hero: drag a report onto the owl
     =================================================================== */
  var heroBot = Robot({ track: true, poke: true });
  $("#heroBot").appendChild(heroBot.el);
  var FEED = [0, 2, 6, 3];
  var FCOL = ["var(--melon)", "var(--mint)", "var(--sky)", "var(--gold)"];
  var stage = $("#stage"), drop = $("#drop"), status = $("#feedStatus"), feedWrap = $("#feedCards");
  feedWrap.innerHTML = FEED.map(function (ri, k) {
    var r = R[ri];
    return '<button type="button" class="fcard" data-ri="' + ri + '" style="--c:' + FCOL[k] + ";--r:" + [-3, 2, -2, 3][k] + 'deg" aria-label="Check report ' + (ri + 1) + ": " + esc(r.title) + '">' +
      '<span class="n">#' + pad(ri + 1) + " &middot; " + esc(r.sev) + "</span><b>" + esc(r.title) + '</b><span class="done-badge"></span></button>';
  }).join("");
  var feeding = false, touched = false;
  function overDrop(x, y) {
    var r = drop.getBoundingClientRect(), pad2 = 40;
    return x > r.left - pad2 && x < r.right + pad2 && y > r.top - pad2 && y < r.bottom + pad2;
  }
  $$(".fcard", feedWrap).forEach(function (card) {
    var sx = 0, sy = 0, down = false, moved = false;
    card._dx = card._dy = 0;
    card.addEventListener("pointerdown", function (e) {
      if (feeding || e.button > 0) return;
      touched = true;
      down = true; moved = false; sx = e.clientX; sy = e.clientY;
      try { card.setPointerCapture(e.pointerId); } catch (x) {}
    });
    card.addEventListener("pointermove", function (e) {
      if (!down) return;
      var dx = e.clientX - sx, dy = e.clientY - sy;
      if (!moved && Math.hypot(dx, dy) < 7) return;
      if (!moved) { moved = true; card.classList.add("dragging"); stage.classList.add("dragging"); heroBot.busy(true).mood("hungry"); }
      card._dx = dx; card._dy = dy;
      card.style.transform = "translate(" + dx + "px," + dy + "px) rotate(" + (dx * 0.03).toFixed(2) + "deg) scale(1.06)";
      stage.classList.toggle("over", overDrop(e.clientX, e.clientY));
      heroBot.lookAt({ left: e.clientX, top: e.clientY, width: 0, height: 0 });
    });
    function release(e, cancel) {
      if (!down) return;
      down = false;
      var hit = !cancel && moved && overDrop(e.clientX, e.clientY);
      stage.classList.remove("dragging", "over");
      card.classList.remove("dragging");
      if (moved && hit) { feed(card); return; }
      if (moved) { card.style.transform = ""; card._dx = card._dy = 0; heroBot.busy(false).mood("idle"); }
    }
    card.addEventListener("pointerup", function (e) { release(e, false); });
    card.addEventListener("pointercancel", function (e) { release(e, true); });
    card.addEventListener("click", function () {
      if (moved) { moved = false; return; }
      feed(card);
    });
  });
  async function feed(card) {
    if (feeding) return;
    feeding = true; touched = true;
    var ri = +card.getAttribute("data-ri"), r = R[ri], v = V[r.state];
    var cr = card.getBoundingClientRect(), dr = drop.getBoundingClientRect();
    var tx = dr.left + dr.width / 2 - (cr.left + cr.width / 2) + card._dx, ty = dr.top + dr.height * 0.45 - (cr.top + cr.height / 2) + card._dy;
    card.style.transform = "translate(" + tx + "px," + ty + "px) scale(.15) rotate(25deg)";
    card.classList.add("eaten");
    heroBot.busy(true).mood("hungry");
    await wait(420);
    heroBot.squash(-8); heroBot.mood("work"); heroBot.lamps([]);
    status.innerHTML = '<span class="run-dots"><i></i><i></i><i></i><span class="fs">building ' + esc(r.commit) + "</span></span>";
    var dots = $$(".run-dots i", status), fs = $(".fs", status), lamps = [];
    await wait(650);
    if (!r.runs.length) { fs.textContent = "not run"; heroBot.mood(v.mood); heroBot.wobble(600); await wait(650); }
    for (var j = 0; j < r.runs.length; j++) {
      var run = r.runs[j];
      fs.textContent = "run " + (j + 1) + " of 3";
      heroBot.rattle(450);
      await wait(520);
      var s = runState(run);
      lamps[j] = s; heroBot.lamps(lamps); dots[j].className = s;
      if (run.crash) { heroBot.flash("alarm", 380); heroBot.hop(8); } else if (run.timedOut) heroBot.flash("clock", 420);
      await wait(260);
    }
    heroBot.mood(v.mood);
    if (r.state === "REPRODUCED_AS_CLAIMED") { heroBot.hop(16); burst(drop, 90); } else heroBot.wobble(500);
    status.innerHTML = '<div class="verdict-pop">' + stamp(r.state) + "<small>#" + pad(ri + 1) + " &middot; " + esc(r.reason) + "</small></div>";
    card.style.transition = "none";
    card.style.transform = "rotate(var(--r)) scale(.5)";
    void card.offsetWidth;
    card.style.transition = "";
    card.classList.remove("eaten");
    card.style.transform = "";
    card._dx = card._dy = 0;
    card.classList.add("v-" + v.c);
    card.querySelector(".done-badge").textContent = "✓ " + v.s;
    await wait(900);
    heroBot.busy(false);
    feeding = false;
    setTimeout(function () { if (!feeding) { heroBot.mood("idle"); heroBot.lamps([]); } }, 2200);
  }
  // nudge the first card until someone plays with it
  setTimeout(function nudge() {
    if (touched) return;
    var c = $(".fcard", feedWrap);
    c.classList.remove("nudge"); void c.offsetWidth; c.classList.add("nudge");
    heroBot.lookAt(c);
    setTimeout(nudge, 4200);
  }, 2600);

  /* ===================================================================
     game: real or fake?
     =================================================================== */
  var gBot = Robot({ track: true, poke: true });
  $("#gBot").appendChild(gBot.el);
  var G = { order: [], i: 0, score: 0, streak: 0, busy: false };
  var gCard = $("#gCard"), gFeedback = $("#gFeedback"), gGuess = $("#gGuess");
  function crashes(state) { return state === "REPRODUCED_AS_CLAIMED" || state === "REPRODUCED_DIFFERENT" || state === "FLAKY"; }
  function shuffle(a) { for (var i = a.length - 1; i > 0; i--) { var j = rnd(0, i), t = a[i]; a[i] = a[j]; a[j] = t; } return a; }
  function setScore(bump) {
    $("#gScore").textContent = G.score;
    $("#gStreak").textContent = G.streak;
    $("#gRound").textContent = Math.min(G.i + 1, R.length) + "/" + R.length;
    if (bump) ["#gScore", "#gStreak"].forEach(function (s) { var b = $(s); b.classList.remove("bump"); void b.offsetWidth; b.classList.add("bump"); });
  }
  function showRound() {
    var ri = G.order[G.i], r = R[ri], v = V[r.state];
    gCard.innerHTML =
      '<div class="r-top"><span class="r-id">REPORT #' + pad(ri + 1) + '</span><span class="pill sev-' + esc(r.sev) + '">' + esc(r.sev) + ' severity</span><span class="pill">commit ' + esc(r.commit) + "</span></div>" +
      "<h3>" + esc(r.title) + "</h3><p>" + esc(r.claim) + '</p><div class="files">' + r.files.map(function (f) { return '<span class="pill">' + esc(f) + "</span>"; }).join("") + "</div>" +
      '<span class="r-stamp stamp v-' + v.c + '">' + esc(v.s) + "</span>";
    gFeedback.innerHTML = '<span class="hint">Does it really crash? Make your call.</span>';
    gGuess.style.display = "";
    $$("button", gGuess).forEach(function (b) { b.disabled = false; });
    gBot.busy(false).mood("idle").lamps([]);
    setScore(false);
  }
  function newGame() { G.order = shuffle(R.map(function (_, i) { return i; })); G.i = 0; G.score = 0; G.streak = 0; showRound(); }
  async function answer(guess) {
    if (G.busy) return;
    G.busy = true;
    $$("button", gGuess).forEach(function (b) { b.disabled = true; });
    var ri = G.order[G.i], r = R[ri], v = V[r.state], right = guess === (crashes(r.state) ? "crash" : "safe");
    gBot.busy(true).mood("work");
    gFeedback.innerHTML = '<span class="run-dots"><i></i><i></i><i></i><span class="gs">building ' + esc(r.commit) + "</span></span>";
    var dots = $$(".run-dots i", gFeedback), gs = $(".gs", gFeedback), lamps = [];
    await wait(550);
    if (!r.runs.length) { gs.textContent = "not run"; await wait(500); }
    for (var j = 0; j < r.runs.length; j++) {
      gs.textContent = "run " + (j + 1) + " of 3";
      gBot.rattle(380);
      await wait(430);
      var s = runState(r.runs[j]);
      lamps[j] = s; gBot.lamps(lamps); dots[j].className = s;
      await wait(180);
    }
    var st = $(".r-stamp", gCard);
    st.classList.add("on");
    gBot.mood(v.mood);
    if (right) { G.score++; G.streak++; burst(st, 70); gBot.hop(12); } else { G.streak = 0; gBot.wobble(500); }
    setScore(right);
    var last = G.i === R.length - 1;
    gFeedback.innerHTML = '<b class="' + (right ? "yes" : "no") + '">' + (right ? "Correct!" : "Not quite.") + "</b>" + esc(r.state) + ": " + esc(r.reason) + '.<br><button type="button" class="btn btn-melon sm" id="gNext">' + (last ? "See your score" : "Next report") + "</button>";
    $("#gNext").addEventListener("click", function () { G.i++; if (G.i >= R.length) endGame(); else showRound(); });
    G.busy = false;
  }
  function endGame() {
    gGuess.style.display = "none";
    var msg = G.score === R.length ? "Perfect. You read crash reports like a sanitizer." :
      G.score >= 6 ? "Sharp eye. Most people get fooled at least twice." :
      G.score >= 4 ? "Not bad. Fake reports read well, and that is the problem." :
      "Fake reports read well. That is exactly why reprobot runs them.";
    gCard.innerHTML = '<div class="r-end"><span class="pill">Final score</span><div class="big">' + G.score + "/" + R.length + "</div><p>" + esc(msg) + '</p><button type="button" class="btn btn-melon" id="gAgain">Play again</button></div>';
    $("#gAgain").addEventListener("click", newGame);
    gFeedback.innerHTML = "";
    $("#gRound").textContent = R.length + "/" + R.length;
    gBot.busy(false).mood(G.score >= 6 ? "happy" : "unsure");
    if (G.score >= 6) burst(gCard, 140);
  }
  $$("button", gGuess).forEach(function (b) { b.addEventListener("click", function () { answer(b.getAttribute("data-g")); }); });
  newGame();

  /* ===================================================================
     slot machine: three runs, one verdict
     =================================================================== */
  var crashA = RB.parse(D.lab.vuln), crashB = RB.parse(D.lab.use_after_free);
  var BURST = "M32 3l6.5 15 15-8.5-4 16 15 4.5-13.5 8.5 9.5 12.5-15.5-2-1 15.5L32 53l-11 11.5-1-15.5-15.5 2L14 38.5.5 30l15-4.5-4-16 15 8.5z";
  var SYM = [
    { k: "a", label: "Crash", svg: '<svg viewBox="0 0 64 64"><path d="' + BURST + '" fill="#ff5d73" stroke="#1b1916" stroke-width="2.5" stroke-linejoin="round"/></svg>' },
    { k: "none", label: "No crash", svg: '<svg viewBox="0 0 64 64"><circle cx="32" cy="32" r="26" fill="#5fe0b5" stroke="#1b1916" stroke-width="3"/><path d="M20 33l8 8 16-17" fill="none" stroke="#1b1916" stroke-width="5" stroke-linecap="round" stroke-linejoin="round"/></svg>' },
    { k: "b", label: "Other crash", svg: '<svg viewBox="0 0 64 64"><path d="' + BURST + '" fill="#b59cff" stroke="#1b1916" stroke-width="2.5" stroke-linejoin="round"/></svg>' },
    { k: "timeout", label: "Hang", svg: '<svg viewBox="0 0 64 64"><circle cx="32" cy="35" r="25" fill="#7cc4ff" stroke="#1b1916" stroke-width="3"/><path d="M32 35V20M32 35l10 7M25 5h14" stroke="#1b1916" stroke-width="4.5" stroke-linecap="round" fill="none"/></svg>' },
    { k: "oom", label: "Out of memory", svg: '<svg viewBox="0 0 64 64"><rect x="10" y="14" width="44" height="36" rx="6" fill="#f5c451" stroke="#1b1916" stroke-width="3"/><path d="M19 24v16M27 24v16M35 24v16M43 24v16M18 6v8M30 6v8M42 6v8M18 50v8M30 50v8M42 50v8" stroke="#1b1916" stroke-width="4" stroke-linecap="round"/></svg>' }
  ];
  var N = SYM.length, LOOPS = 10, WEIGHT = [0.42, 0.24, 0.12, 0.12, 0.10];
  var reelsEl = $("#reels"), slotEl = $(".slot"), lever = $("#lever");
  var stripHTML = ""; for (var L = 0; L < LOOPS; L++) SYM.forEach(function (s) { stripHTML += '<div class="sym">' + s.svg + "<span>" + s.label + "</span></div>"; });
  reelsEl.innerHTML = [0, 1, 2].map(function (i) { return '<button type="button" class="reel" data-i="' + i + '" aria-label="Run ' + (i + 1) + ', tap to nudge"><span class="ri">RUN ' + (i + 1) + '</span><div class="strip">' + stripHTML + "</div></button>"; }).join("");
  var reels = $$(".reel", reelsEl), strips = $$(".strip", reelsEl);
  var slotBot = Robot({ track: true, poke: true });
  $("#slotBot").appendChild(slotBot.el);
  var pos = [N, N, N], spinning = false;
  function H() { return reels[0].clientHeight; }
  function setPos(i, p, ms, ease) {
    strips[i].style.transition = ms ? "transform " + ms + "ms " + (ease || "ease") : "none";
    strips[i].style.transform = "translateY(" + (-p * H()) + "px)";
  }
  function norm(i) { var p = pos[i] % N + N; if (p !== pos[i]) { pos[i] = p; setPos(i, p, 0); void strips[i].offsetHeight; } }
  function pick() { var x = Math.random(), a = 0; for (var i = 0; i < N; i++) { a += WEIGHT[i]; if (x < a) return i; } return 0; }
  function evaluate(changed) {
    var keys = pos.map(function (p) { return SYM[p % N].k; });
    var runs = keys.map(function (k) { return { crash: k === "a" ? crashA : k === "b" ? crashB : null, timedOut: k === "timeout", oom: k === "oom" }; });
    var d = RB.decide({ sanitizer: "address", kind: "heap-buffer-overflow", frame: "parse_header" }, runs), v = V[d.state];
    var vb = $("#slotVerdict");
    vb.textContent = v.s.toUpperCase();
    $("#slotScreen").className = "screen v-" + v.c;
    $("#slotReason").textContent = d.state + " · " + d.reason;
    slotBot.lamps(keys.map(function (k) { return k === "a" ? "crash" : k === "b" ? "other" : k === "none" ? "ok" : "hang"; }));
    slotBot.busy(false).mood(v.mood);
    if (changed) {
      if (d.state === "REPRODUCED_AS_CLAIMED") {
        slotBot.hop(14);
        if (keys.every(function (k) { return k === "a"; })) { burst($(".cabinet"), 110); slotEl.classList.add("win"); setTimeout(function () { slotEl.classList.remove("win"); }, 1600); }
      } else slotBot.wobble(500);
    }
  }
  async function pull() {
    if (spinning) return;
    spinning = true;
    lever.classList.add("pulled");
    setTimeout(function () { lever.classList.remove("pulled"); }, 420);
    slotEl.classList.add("spinning");
    slotBot.busy(true).mood("work");
    $("#slotVerdict").textContent = "SPINNING...";
    $("#slotReason").textContent = "three runs, no network";
    $("#slotScreen").className = "screen";
    var longest = 0;
    for (var i = 0; i < 3; i++) {
      norm(i);
      var t = pick(), cur = pos[i];
      var next = cur + N * (4 + i) + ((t - cur % N) + N) % N;
      var ms = 1100 + i * 450;
      longest = Math.max(longest, ms);
      setPos(i, next, ms, "cubic-bezier(.12,.7,.2,1.03)");
      pos[i] = next;
    }
    await wait(longest + 80);
    slotEl.classList.remove("spinning");
    spinning = false;
    evaluate(true);
  }
  lever.addEventListener("click", pull);
  reels.forEach(function (rl, i) {
    rl.addEventListener("click", function () {
      if (spinning) return;
      norm(i);
      pos[i] += 1;
      setPos(i, pos[i], 320, "cubic-bezier(.3,1.4,.5,1)");
      setTimeout(function () { evaluate(true); }, 330);
    });
  });
  pos.forEach(function (p, i) { setPos(i, p, 0); });
  evaluate(false);
  var lastH = H();
  window.addEventListener("resize", function () { if (H() !== lastH) { lastH = H(); pos.forEach(function (p, i) { setPos(i, p, 0); }); } });

  /* ===================================================================
     gauntlet: try to fool it
     =================================================================== */
  var E2E = "internal/runner/runner_e2e_test.go", RT = "internal/runner/runner_test.go";
  var GATES = ["Program check", "Claim check", "Build", "Three runs", "Secret log folder", "Secret exit code"];
  var p = "pass";
  var TRICKS = [
    { t: "Paste a real ASan log", d: "Print a captured report to stderr.", tok: "fake ASan log", c: "var(--melon)", path: [p, p, p, p, "stop"], state: "NOT_REPRODUCED",
      why: "reprobot never reads stderr. A crash only counts when the sanitizer writes it to the secret log folder.", test: ["TestE2EFakeReportOnStderrIsIgnored", E2E, 194] },
    { t: "Run it with a shell", d: "Set the command to sh -c '...'.", tok: "sh -c", c: "var(--gold)", path: ["stop"], state: "INVALID_INPUT",
      why: "Only programs the project's own build makes may run. A shell is rejected before anything is built.", test: ["TestE2EReporterShellIsRejected", E2E, 210] },
    { t: "Write the log file yourself", d: "Copy a report into the log folder.", tok: "asan.log", c: "var(--lilac)", path: [p, p, p, p, "stop"], state: "NOT_REPRODUCED",
      why: "The folder gets a new random name every run, and its parent folder cannot be listed.", test: ["TestMakeOutDirModes", RT, 761] },
    { t: "Fake the exit code", d: "Exit with 1, like most failing programs.", tok: "exit 1", c: "var(--sky)", path: [p, p, p, p, p, "stop"], state: "NOT_REPRODUCED",
      why: "Each run gets a secret exit code from 64 to 120. Exit 1 does not match, so nothing counts.", test: ["TestRunIgnoresReportWithWrongExitCode", RT, 724] },
    { t: "Plant a symlink", d: "Point asan.1 at a report somewhere else.", tok: "symlink", c: "var(--mint)", path: [p, p, p, p, "stop"], state: "NOT_REPRODUCED",
      why: "Log files are opened with O_NOFOLLOW. Symlinks and FIFOs are skipped.", test: ["TestCollectReportSkipsSymlinksAndFifos", RT, 779] },
    { t: "Cite functions that sound right", d: "Name functions that look well researched.", tok: "made-up names", c: "var(--melon)", path: [p, "flag", p, p, "stop"], state: "NOT_REPRODUCED",
      flag: "3 of the named functions do not exist at this commit.", why: "No crash in 3 runs, and the invented names are listed next to the verdict.", test: ["TestE2EInventedFunctionIsAFinding", E2E, 120] }
  ];
  $("#trickGrid").innerHTML = TRICKS.map(function (t, i) {
    return '<button type="button" class="trick" aria-pressed="false" data-i="' + i + '" style="--c:' + t.c + '"><span class="ti">' + (i + 1) + "</span><b>" + esc(t.t) + "</b><small>" + esc(t.d) + "</small></button>";
  }).join("");
  $("#gates").innerHTML = GATES.map(function (g, i) { return '<div class="gate"><span class="gn">' + (i + 1) + "</span><b>" + esc(g) + "</b></div>"; }).join("");
  var gateEls = $$("#gates .gate"), runner = $("#runner"), bubble = $("#bubble"), lane = $("#lane"), trickResult = $("#trickResult");
  var guardBot = Robot({ track: true, poke: true });
  $("#guardBot").appendChild(guardBot.el);
  var trickTok = 0;
  function gatePoint(k) { var g = gateEls[k]; return { x: g.offsetLeft + g.offsetWidth / 2, y: g.offsetTop }; }
  function moveRunner(k, instant) {
    var q = gatePoint(k);
    if (instant) runner.style.transition = "none";
    runner.style.transform = "translate(" + (q.x - runner.offsetWidth / 2) + "px," + (q.y - 44) + "px)";
    if (instant) { void runner.offsetWidth; runner.style.transition = ""; }
  }
  function say(k, text) {
    if (!text) { bubble.classList.remove("on"); return; }
    bubble.textContent = text;
    var q = gatePoint(k), w = bubble.offsetWidth, lw = lane.clientWidth;
    var left = Math.max(0, Math.min(lw - w, q.x - w / 2));
    bubble.style.left = left + "px";
    bubble.style.top = Math.max(-6, q.y - 44 - bubble.offsetHeight - 12) + "px";
    bubble.classList.add("on");
  }
  async function runTrick(i) {
    var tok = ++trickTok, t = TRICKS[i];
    $$("#trickGrid .trick").forEach(function (b) { b.setAttribute("aria-pressed", +b.getAttribute("data-i") === i ? "true" : "false"); });
    gateEls.forEach(function (g, k) { g.className = "gate"; g.querySelector(".gn").textContent = k + 1; });
    say(0, "");
    runner.textContent = t.tok;
    runner.className = "runner on";
    trickResult.innerHTML = '<span class="hint">Running the trick...</span>';
    guardBot.busy(true).mood("scan");
    moveRunner(0, true);
    for (var k = 0; k < t.path.length; k++) {
      moveRunner(k);
      guardBot.lookAt(gateEls[k]);
      await wait(560);
      if (tok !== trickTok) return;
      var st = t.path[k], g = gateEls[k];
      g.classList.add(st);
      if (st === "stop") {
        g.querySelector(".gn").textContent = "✕";
        runner.classList.add("caught");
        guardBot.mood("no"); guardBot.wobble(600);
        say(k, t.why);
      } else if (st === "flag") {
        g.querySelector(".gn").textContent = "!";
        guardBot.flash("unsure", 1100);
        say(k, t.flag);
        await wait(1300);
        if (tok !== trickTok) return;
        say(k, "");
      } else {
        g.querySelector(".gn").textContent = "✓";
        await wait(140);
      }
      if (tok !== trickTok) return;
    }
    for (var m = t.path.length; m < GATES.length; m++) gateEls[m].classList.add("off");
    trickResult.innerHTML = stamp(t.state) + "<span>Stopped at check " + t.path.length + ": " + esc(GATES[t.path.length - 1]) + '.</span><a href="' + GH + t.test[1] + "#L" + t.test[2] + '">' + esc(t.test[0]) + "</a>";
    await wait(1600);
    if (tok === trickTok) guardBot.busy(false);
  }
  $$("#trickGrid .trick").forEach(function (b) { b.addEventListener("click", function () { runTrick(+b.getAttribute("data-i")); }); });
  seen($("#gauntlet"), function () { if (!$('#trickGrid [aria-pressed="true"]')) runTrick(0); }, 0.45);
  var laneW = lane.clientWidth;
  window.addEventListener("resize", function () {
    if (lane.clientWidth === laneW) return;
    laneW = lane.clientWidth;
    trickTok++;
    say(0, "");
    runner.className = "runner";
    gateEls.forEach(function (g, k) { g.className = "gate"; g.querySelector(".gn").textContent = k + 1; });
    $$("#trickGrid .trick").forEach(function (b) { b.setAttribute("aria-pressed", "false"); });
    trickResult.innerHTML = '<span class="hint">Pick a trick above.</span>';
    guardBot.busy(false).mood("idle");
  });

  /* ===================================================================
     fingerprint lab
     =================================================================== */
  var REPORT_A = R[0].crash.signature;
  var PAL = ["#2d2924", "#3f3831", "#5b5147", "#7d7062", "#a7998a", "#d8cdbd", "#f3ece0", "#ffe3a3",
             "#f5c451", "#ff9d7a", "#ff5d73", "#b59cff", "#7cc4ff", "#5fe0b5", "#2fa27f", "#e9a6ff"];
  var lab = { text: D.lab.vuln, src: "report #01 · commit " + D.commits.vuln, last: "" };
  function grid(el, sig, animate) {
    if (!el.children.length) el.innerHTML = new Array(17).join("<span></span>");
    $$("span", el).forEach(function (cell, i) {
      var col = PAL[parseInt(sig[i], 16)];
      if (animate && !reduce) {
        cell.classList.remove("flip"); void cell.offsetWidth;
        cell.style.animationDelay = (i * 28) + "ms";
        cell.classList.add("flip");
        setTimeout(function () { cell.style.background = col; }, 250 + i * 28);
      } else cell.style.background = col;
    });
  }
  grid($("#fpB"), REPORT_A, false);
  $("#hexB").textContent = REPORT_A;
  function renderLab() {
    var c = RB.parse(lab.text), top = RB.projectFrames(c).slice(0, 3);
    $("#labSrc").textContent = lab.src;
    $("#labKind").textContent = c.kind;
    $("#labFrames").innerHTML = top.map(function (f, k) {
      var name = RB.trimArgs(f.function), loc = (f.file || "").split("/").pop() + (f.line ? ":" + f.line : "");
      return '<button type="button" class="frame' + (/_v2$/.test(name) ? " renamed" : "") + '" data-name="' + esc(name) + '"><span class="k">' + (k + 1) + "</span>" + esc(name) + "<small>" + esc(loc) + "</small></button>";
    }).join("");
    var addr = (lab.text.match(/0x[0-9a-f]{8,}/i) || [""])[0];
    $("#labAddr").innerHTML = "<s>" + esc(addr) + "</s> &nbsp;addresses are ignored";
    var changed = lab.last && lab.last !== c.signature;
    grid($("#fpA"), c.signature, changed);
    $("#hexA").textContent = c.signature;
    var same = c.signature === REPORT_A, m = $("#fpMatch");
    m.className = "match " + (same ? "same" : "diff");
    m.textContent = same ? "✓ Same fingerprint, grouped with report 01" : "✕ Different fingerprint, a new bug";
    if (lab.last && same && !changed) { m.classList.remove("same"); void m.offsetWidth; m.classList.add("same"); }
    lab.last = c.signature;
    $$("#labFrames .frame").forEach(function (b) {
      b.addEventListener("click", function () {
        var name = b.getAttribute("data-name");
        var from = name, to = /_v2$/.test(name) ? name.replace(/_v2$/, "") : name + "_v2";
        lab.text = lab.text.split(" in " + from + " ").join(" in " + to + " ").split(" in " + from + "\n").join(" in " + to + "\n");
        lab.src = to.indexOf("_v2") > 0 ? from + " renamed to " + to : "rename undone";
        renderLab();
      });
    });
  }
  function shuffleAddrs(t) {
    return t.replace(/0x[0-9a-fA-F]{6,}/g, function (m) { var s = "0x"; for (var i = 2; i < m.length; i++) s += "0123456789abcdef"[rnd(0, 15)]; return s; });
  }
  $$(".lab-tools .btn").forEach(function (b) {
    b.addEventListener("click", function () {
      var act = b.getAttribute("data-act");
      if (act === "shuffle") { lab.text = shuffleAddrs(lab.text); lab.src = "addresses moved"; }
      if (act === "revert") { lab.text = D.lab.revert; lab.src = "report #06 · commit " + D.commits.revert; }
      if (act === "other") { lab.text = D.lab.use_after_free; lab.src = "a use-after-free test crash"; }
      if (act === "reset") { lab.text = D.lab.vuln; lab.src = "report #01 · commit " + D.commits.vuln; }
      renderLab();
    });
  });
  renderLab();

  /* ===================================================================
     terminal
     =================================================================== */
  var tOut = $("#tOut"), tIn = $("#tInput"), hist = [], hi = 0, busyT = false;
  var FILES = { "repro.yaml": 0, "fake.yaml": 2, "hang.yaml": 6 };
  var frames = RB.projectFrames(crashA).slice(0, 3);
  function line(text, cls) {
    var d = document.createElement("div");
    if (cls) d.className = cls;
    d.textContent = text;
    tOut.appendChild(d);
    tOut.scrollTop = tOut.scrollHeight;
    return d;
  }
  function kv(k, v, cls) {
    var d = document.createElement("div");
    d.innerHTML = '<span class="key">' + esc(k) + "</span>" + '<span class="' + (cls || "") + '">' + esc(v) + "</span>";
    tOut.appendChild(d);
    tOut.scrollTop = tOut.scrollHeight;
  }
  async function typeCmd(cmd) {
    var d = line("", "cmd");
    for (var i = 0; i < cmd.length; i++) { d.textContent = cmd.slice(0, i + 1); await wait(cmd.length > 30 ? 9 : 24); }
  }
  async function simulate(name) {
    var r = R[FILES[name]], v = V[r.state];
    line("reading " + name, "dim");
    await wait(220);
    (r.findings || []).forEach(function (f) { line("finding: " + f, "key"); });
    line("building commit " + r.commit + " with AddressSanitizer" + (r.cacheHit ? " (cached)" : ""), "dim");
    await wait(500);
    for (var j = 0; j < r.runs.length; j++) {
      var run = r.runs[j];
      await wait(380);
      line("run " + (j + 1) + "   " + (run.crash ? "crash     " : run.timedOut ? "time limit" : "no crash  ") + "   exit " + run.exit + "   " + run.ms + " ms", run.crash ? "bad" : run.timedOut ? "sky" : "ok");
    }
    await wait(300);
    line("");
    kv("state:     ", r.state, v.c === "real" ? "bad" : v.c === "time" ? "sky" : v.c === "diff" ? "lil" : "ok");
    kv("reason:    ", r.reason);
    if (r.crash) {
      kv("crash:     ", "address " + r.crash.kind + (r.crash.access ? ", " + r.crash.access + (r.crash.size ? " of size " + r.crash.size : "") : ""));
      line("frames:", "key");
      frames.forEach(function (f, k) { line("  #" + (k + 1) + " " + RB.trimArgs(f.function) + " " + (f.file || "") + (f.line ? ":" + f.line : "")); });
      kv("signature: ", r.crash.signature);
    }
  }
  async function run(raw) {
    var cmd = raw.trim().replace(/\s+/g, " ");
    if (!cmd || busyT) return;
    busyT = true;
    hist.push(cmd); hi = hist.length;
    await typeCmd(cmd);
    var a = cmd.split(" ");
    try {
      if (cmd === "help") {
        ["try these:", "  reprobot run --project project.yaml repro.yaml   a real heap overflow", "  reprobot run --project project.yaml fake.yaml    a pasted ASan log", "  reprobot run --project project.yaml hang.yaml    an input that never ends", "  ls  |  cat repro.yaml  |  clear"].forEach(function (l, i) { line(l, i ? "" : "key"); });
      } else if (cmd === "clear") {
        tOut.innerHTML = "";
      } else if (cmd === "ls") {
        line("project.yaml  repro.yaml  fake.yaml  hang.yaml", "sky");
      } else if (a[0] === "cat") {
        var f = a[1] || "";
        if (FILES[f] !== undefined) R[FILES[f]].yaml.forEach(function (l) { line(l); });
        else if (f === "project.yaml") line("project.yaml is the maintainers' build profile. See the README for its fields.", "dim");
        else line("cat: " + (f || "missing file name") + ": no such file", "bad");
      } else if (a[0] === "reprobot") {
        var file = a.filter(function (x) { return /\.yaml$/.test(x) && x !== "project.yaml"; })[0];
        if (a[1] !== "run" || !file) line("usage: reprobot run --project project.yaml repro.yaml", "key");
        else if (FILES[file] === undefined) line("reprobot: " + file + ": no such file (try ls)", "bad");
        else await simulate(file);
      } else {
        line("command not found: " + a[0] + ". Try help.", "bad");
      }
    } finally { busyT = false; }
  }
  $("#tForm").addEventListener("submit", function (e) { e.preventDefault(); var v = tIn.value; tIn.value = ""; run(v); });
  tIn.addEventListener("keydown", function (e) {
    if (e.key === "ArrowUp" && hist.length) { hi = Math.max(0, hi - 1); tIn.value = hist[hi]; e.preventDefault(); }
    if (e.key === "ArrowDown" && hist.length) { hi = Math.min(hist.length, hi + 1); tIn.value = hist[hi] || ""; e.preventDefault(); }
  });
  var CHIPS = ["help", "ls", "cat repro.yaml", "reprobot run --project project.yaml repro.yaml", "reprobot run --project project.yaml fake.yaml", "reprobot run --project project.yaml hang.yaml", "clear"];
  $("#tChips").innerHTML = CHIPS.map(function (c) { return '<button type="button">' + esc(c) + "</button>"; }).join("");
  $$("#tChips button").forEach(function (b) { b.addEventListener("click", function () { run(b.textContent); }); });
  line("reprobot demo shell. Type help, or tap a command below.", "dim");
  seen($(".term"), function () { run("reprobot run --project project.yaml repro.yaml"); }, 0.5);

  /* ===================================================================
     real CVEs: flip the commit (data from real.js, a recorded eval run)
     =================================================================== */
  (function () {
    var X = window.RB_REAL;
    if (!X || !$("#flip")) return;
    var C = X.real, F = X.fakes, LIB = "https://github.com/GNOME/libxml2/commit/";
    function count(list, at, state) { return list.filter(function (c) { return (at ? c[at] : c).state === state; }).length; }
    $("#tally").innerHTML = [
      [count(C, "vuln", "REPRODUCED_AS_CLAIMED") + "/" + C.length, "crash on the vulnerable commit", "var(--melon)"],
      [count(C, "fix", "NOT_REPRODUCED") + "/" + C.length, "clean on the fix commit", "var(--mint)"],
      [count(F, null, "NOT_REPRODUCED") + "/" + F.length, "fake reports rejected", "var(--gold)"]
    ].map(function (t) { return '<div style="--c: ' + t[2] + '"><b>' + t[0] + "</b><span>" + t[1] + "</span></div>"; }).join("");

    var tabs = $("#cveTabs");
    tabs.innerHTML = C.map(function (c, i) {
      return '<button type="button" class="cve-tab" role="tab" aria-selected="' + (i === 0) + '" data-i="' + i + '"><code>' + esc(c.id) +
        "</code><small>" + esc(c.title) + '</small><span class="dots" aria-hidden="true"><i></i><i></i></span></button>';
    }).join("");
    var tabEls = $$(".cve-tab", tabs);

    var bot = Robot({ track: true, poke: true });
    $("#realBot").appendChild(bot.el);
    var sw = $("#cSwitch"), swBtns = $$("button", sw), lampWrap = $("#cLamps");
    lampWrap.innerHTML = [1, 2, 3].map(function (n) { return '<span class="lamp"><i></i><span>run ' + n + "</span></span>"; }).join("");
    var lamps = $$(".lamp", lampWrap);
    var cur = { i: 0, at: "vuln", token: 0 }, seenRuns = {};

    function twin(c) {
      for (var k = 0; k < C.length; k++) if (C[k] !== c && C[k].vuln.signature === c.vuln.signature) return C[k];
      return null;
    }
    function setLamps(r, hot, upto) {
      lamps.forEach(function (l, k) {
        l.className = "lamp" + (k < upto ? (hot ? " hot" : " cool") : "");
        $("span", l).textContent = k < upto ? r.runsMs[k] + " ms" : "run " + (k + 1);
      });
    }
    function dots() {
      tabEls.forEach(function (t, i) {
        var d = $$(".dots i", t);
        d[0].className = seenRuns[i + "vuln"] ? "hot" : "";
        d[1].className = seenRuns[i + "fix"] ? "cool" : "";
      });
    }
    function frame(c) {
      var r = c.vuln, size = r.size ? " of size " + r.size : "";
      return '<div class="kind-line"><b>' + esc(r.kind) + "</b><span>" + esc(r.access + size) + "</span></div><ol>" +
        r.frames.map(function (f, k) {
          return '<li class="' + (f.fn === c.claim.frame ? "claimed" : "") + '" style="animation-delay:' + (k * 70) + 'ms"><span class="k">' + (k + 1) +
            '</span><span class="fn">' + esc(f.fn) + "</span><small>" + esc(f.at) + "</small></li>";
        }).join("") + "</ol>" +
        (r.line ? '<div class="crash-line"><small>crash line</small><code>' + esc(r.line) + "</code></div>" : "");
    }
    async function show(animate) {
      var token = ++cur.token, c = C[cur.i], at = cur.at, r = c[at], hot = at === "vuln";
      sw.classList.toggle("at-fix", !hot);
      swBtns.forEach(function (b) {
        var which = b.getAttribute("data-at");
        b.setAttribute("aria-pressed", String(which === at));
        $("code", b).textContent = c[which].commit.slice(0, 7);
      });
      tabEls.forEach(function (t, i) { t.setAttribute("aria-selected", String(i === cur.i)); });
      $("#cLinks").innerHTML =
        '<a href="' + LIB + c[at].commit + '" target="_blank" rel="noopener">' + (hot ? "vulnerable" : "fix") + " commit ↗</a>" +
        '<a href="' + GH + "eval/libxml2/" + c.dir + '/repro.yaml" target="_blank" rel="noopener">the report ↗</a>';
      var gridEl = $("#cGrid"), print = gridEl.parentNode, old = $(".twin", print);
      if (old) old.remove();

      if (animate && !reduce) {
        bot.busy(true).mood("work");
        $("#cStamp").innerHTML = "";
        $("#cReason").textContent = "";
        $("#cCrash").innerHTML = '<div class="wait">$ ' + esc(c.cmd) + "</div>";
        if (!gridEl.children.length) grid(gridEl, "0000000000000000", false);
        gridEl.classList.add("off");
        $("#cHex").textContent = "................";
        setLamps(r, hot, 0);
        for (var k = 0; k < 3; k++) {
          lamps[k].className = "lamp run";
          await wait(Math.max(320, r.runsMs[k] * 2.6));
          if (token !== cur.token) return;
          setLamps(r, hot, k + 1);
          if (hot) bot.flash("alarm", 300); else bot.flash("happy", 250);
          bot.hop(hot ? 7 : 4);
        }
        await wait(200);
        if (token !== cur.token) return;
        bot.busy(false);
      } else setLamps(r, hot, 3);

      seenRuns[cur.i + at] = true;
      dots();
      $("#cStamp").innerHTML = stamp(r.state);
      $("#cReason").textContent = r.reason + " · commit " + r.commit.slice(0, 7);
      if (hot) {
        $("#cCrash").innerHTML = frame(c);
        gridEl.classList.remove("off");
        grid(gridEl, r.signature, animate);
        $("#cHex").textContent = r.signature;
        var t = twin(c);
        if (t) print.insertAdjacentHTML("beforeend", '<span class="twin">same print as ' + esc(t.id) +
          (t.vuln.site && c.vuln.site && t.vuln.site !== c.vuln.site ? ", other line: not a duplicate" : "") + "</span>");
        bot.mood("alarm");
        if (animate) burst(lampWrap, 50);
      } else {
        $("#cCrash").innerHTML = '<div class="calm"><div><b>No crash</b><br><span>exit ' + esc(r.exit[0]) + " · no sanitizer report</span></div></div>";
        if (!gridEl.children.length) grid(gridEl, "0000000000000000", false);
        gridEl.classList.add("off");
        $("#cHex").textContent = "no crash, no print";
        bot.mood("happy");
        if (animate) bot.hop(14);
      }
    }
    tabEls.forEach(function (t, i) {
      t.addEventListener("click", function () { cur.i = i; cur.at = "vuln"; show(true); });
      t.addEventListener("keydown", function (e) {
        var d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
        if (!d) return;
        e.preventDefault();
        var n = (i + d + C.length) % C.length;
        tabEls[n].focus();
        cur.i = n; cur.at = "vuln"; show(true);
      });
    });
    swBtns.forEach(function (b) {
      b.addEventListener("click", function () {
        var at = b.getAttribute("data-at");
        cur.at = at === cur.at ? (at === "vuln" ? "fix" : "vuln") : at;
        show(true);
      });
    });
    show(false);
    seen($("#flip"), async function () {
      var token = cur.token;
      cur.at = "vuln";
      await show(true);
      await wait(1300);
      if (token + 1 !== cur.token) return; // the visitor took over
      cur.at = "fix";
      show(true);
    }, 0.2);

    $("#fakes").innerHTML = F.map(function (f) {
      var why = f.findings.length ? f.claim.frame + " is not in the code"
        : f.stderrHasReport ? "the pasted report reached stderr, not counted"
        : "3 runs, no sanitizer report";
      return '<article class="fake"><small>Fake report</small><p>' + esc(f.about.charAt(0).toUpperCase() + f.about.slice(1)) +
        "</p><code>claims " + esc(f.claim.kind) + " in " + esc(f.claim.frame) + "</code>" + stamp(f.state) +
        '<span class="why">' + esc(why) + "</span></article>";
    }).join("");
  })();

  $("#copyBtn").addEventListener("click", function () {
    var b = this;
    if (!navigator.clipboard) return;
    navigator.clipboard.writeText("go install github.com/DCchoudhury15/reprobot/cmd/reprobot@latest").then(function () {
      b.textContent = "Copied"; setTimeout(function () { b.textContent = "Copy"; }, 1400);
    }, function () {});
  });
})();

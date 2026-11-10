/* A line by line port of internal/sanitizer, internal/claim and
   runner.Decide, so the page computes the same answers as reprobot.
   It was checked against the Go code on every file in
   testdata/sanitizer/out. */
(function (root) {
  "use strict";

  var asanErrorRe = /==\d+==ERROR: AddressSanitizer: (\S+)(?:\s+(\S+))?/;
  var asanSummaryRe = /^SUMMARY: AddressSanitizer: ([A-Za-z][A-Za-z0-9_-]*)/;
  var accessRe = /^(READ|WRITE) of size (\d+) at /;
  var signalRe = /The signal is caused by a (READ|WRITE) memory access/;
  var ubsanRe = /^(?:(.+?):(\d+):(\d+)|<unknown>): runtime error: (.+)$/;
  var frameRe = /^\s*#(\d+)\s+0x[0-9a-fA-F]+\s*(.*)$/;
  var buildIDRe = /\s*\(BuildId: [0-9a-fA-F]+\)$/;
  var moduleRe = /\s*\(([^()\s]+)\+0x[0-9a-fA-F]+\)$/;
  var locationRe = /(?:^|\s)(\S+?):(\d+)(?::(\d+))?$/;
  var maxGap = 8;

  function isReportStart(l) { return asanErrorRe.test(l) || ubsanRe.test(l); }

  function parse(stderr) {
    var lines = stderr.split("\n");
    for (var i = 0; i < lines.length; i++) {
      var m = asanErrorRe.exec(lines[i]);
      if (m) return finish(parseASan(lines, i, m));
      m = ubsanRe.exec(lines[i]);
      if (m) return finish(parseUBSan(lines, i, m));
    }
    return null;
  }

  function parseASan(lines, at, m) {
    var c = { sanitizer: "address", kind: asanKind(m[1], m[2] || ""), access: "", size: 0, frames: [] };
    var i, l, s, a;
    for (i = at + 1; i < lines.length; i++) {
      l = lines[i];
      if (isReportStart(l)) break;
      s = asanSummaryRe.exec(l);
      if (s) { c.kind = s[1]; break; }
    }
    for (i = at + 1; i < lines.length; i++) {
      l = lines[i];
      if (frameRe.test(l)) break;
      a = accessRe.exec(l);
      if (a) { c.access = a[1]; c.size = parseInt(a[2], 10) || 0; break; }
      a = signalRe.exec(l);
      if (a) { c.access = a[1]; break; }
    }
    c.frames = firstStack(lines, at + 1);
    return c;
  }

  function asanKind(first, second) {
    if (first === "attempting") {
      if (second === "double-free") return "double-free";
      if (second === "free") return "bad-free";
    }
    return first;
  }

  function parseUBSan(lines, at, m) {
    var c = { sanitizer: "undefined", kind: ubsanKind(m[4]), access: "", size: 0, frames: [] };
    c.frames = firstStack(lines, at + 1);
    if (c.frames.length === 0 && m[1]) {
      c.frames = [{ index: 0, function: "", file: m[1], line: parseInt(m[2], 10) || 0, module: "" }];
    }
    return c;
  }

  function ubsanKind(msg) {
    var has = function () {
      for (var i = 0; i < arguments.length; i++) if (msg.indexOf(arguments[i]) === 0) return true;
      return false;
    };
    if (has("signed integer overflow")) return "signed-integer-overflow";
    if (has("shift exponent", "left shift of")) return "shift-out-of-bounds";
    if (has("load of null pointer", "store to null pointer", "member access within null pointer", "member call on null pointer")) return "null-pointer-dereference";
    if (has("index ") && msg.indexOf(" out of bounds") >= 0) return "index-out-of-bounds";
    if (msg.indexOf("misaligned address") >= 0) return "misaligned-address";
    if (has("division by zero")) return "division-by-zero";
    return "undefined-behavior";
  }

  function firstStack(lines, from) {
    var frames = [], gap = 0;
    for (var i = from; i < lines.length; i++) {
      var l = lines[i];
      var m = frameRe.exec(l);
      if (!m) {
        if (frames.length > 0) break;
        gap++;
        if (gap > maxGap || isReportStart(l)) break;
        continue;
      }
      var f = parseFrame(m[2]);
      f.index = parseInt(m[1], 10) || 0;
      frames.push(f);
    }
    return frames;
  }

  function parseFrame(rest) {
    var f = { index: 0, function: "", file: "", line: 0, module: "" };
    rest = rest.replace(buildIDRe, "").trim();
    var m = moduleRe.exec(rest);
    if (m) {
      f.module = m[1];
      rest = rest.slice(0, m.index);
    } else {
      m = locationRe.exec(rest);
      if (m) {
        f.file = m[1];
        f.line = parseInt(m[2], 10) || 0;
        rest = rest.slice(0, m.index);
      }
    }
    var t = rest.trim();
    if (t.indexOf("in ") === 0) f.function = t.slice(3).trim();
    return f;
  }

  var skipFunctions = ["__asan", "__interceptor_", "___interceptor_", "__sanitizer", "__ubsan", "__libc_"];
  var systemLibs = ["libc", "libasan", "libubsan", "libstdc++", "libgcc_s", "libgcc"];

  // why a frame is not a project frame, or "" when it is one
  function skipReason(f) {
    if (f.function === "") return "no function name";
    if (f.function === "_start") return "program start-up";
    if (systemModule(f.module)) return "inside " + base(f.module);
    for (var i = 0; i < skipFunctions.length; i++) {
      if (f.function.indexOf(skipFunctions[i]) === 0) return "sanitizer or libc runtime";
    }
    return "";
  }

  function projectFrames(c) {
    if (!c) return [];
    return c.frames.filter(function (f) { return skipReason(f) === ""; });
  }

  function base(p) {
    if (p === "") return ".";
    p = p.replace(/\/+$/, "");
    if (p === "") return "/";
    var i = p.lastIndexOf("/");
    return i >= 0 ? p.slice(i + 1) : p;
  }

  function systemModule(module) {
    if (module === "") return false;
    var b = base(module);
    if (b.indexOf("ld-linux") === 0) return true;
    for (var i = 0; i < systemLibs.length; i++) {
      var lib = systemLibs[i];
      if (b.indexOf(lib + ".so") === 0 || (b.indexOf(lib + "-") === 0 && b.indexOf(".so") >= 0)) return true;
    }
    return false;
  }

  function trimArgs(fn) {
    var s = fn.trim();
    var quals = [" const", " volatile", " &&", " &"];
    for (;;) {
      var t = s;
      for (var q = 0; q < quals.length; q++) {
        if (t.length >= quals[q].length && t.slice(-quals[q].length) === quals[q]) t = t.slice(0, -quals[q].length);
      }
      if (t === s) break;
      s = t;
    }
    if (s.slice(-1) !== ")") return s;
    var depth = 0;
    for (var i = s.length - 1; i >= 0; i--) {
      var ch = s[i];
      if (ch === ")") depth++;
      else if (ch === "(") {
        depth--;
        if (depth === 0) {
          var head = s.slice(0, i).trim();
          if (head === "" || head.slice(-8) === "operator") return s;
          return head;
        }
      }
    }
    return s;
  }

  function finish(c) {
    var parts = [];
    var pf = projectFrames(c);
    for (var i = 0; i < pf.length && parts.length < 3; i++) parts.push(trimArgs(pf[i].function));
    if (parts.length === 0 && c.frames.length > 0 && c.frames[0].file !== "") parts.push(base(c.frames[0].file));
    c.signatureText = c.kind + "|" + parts.join("|");
    c.signature = sha256hex(c.signatureText).slice(0, 16);
    return c;
  }

  /* claim.Compare */
  var TopFrames = 5;
  function normalizeKind(k) { return k.trim().toLowerCase().replace(/[ _]/g, "-"); }
  function compare(claim, crash) {
    if (!crash) return { match: false, findings: ["no crash was observed"] };
    var findings = [];
    if (claim.sanitizer !== crash.sanitizer) findings.push('claimed sanitizer "' + claim.sanitizer + '" but saw "' + crash.sanitizer + '"');
    if (normalizeKind(claim.kind) !== normalizeKind(crash.kind)) findings.push('claimed kind "' + claim.kind + '" but saw "' + crash.kind + '"');
    if (claim.frame) {
      var want = trimArgs(claim.frame), top = [];
      var pf = projectFrames(crash);
      for (var i = 0; i < pf.length && top.length < TopFrames; i++) top.push(trimArgs(pf[i].function));
      if (top.length === 0) findings.push('claimed frame "' + claim.frame + '" but the crash has no project frames');
      else if (top.indexOf(want) < 0) findings.push('claimed frame "' + claim.frame + '" is not in the top ' + TopFrames + " project frames: " + top.join(", "));
    }
    return { match: findings.length === 0, findings: findings };
  }

  /* runner.Decide. runs: [{crash, timedOut, oom}] */
  function nRuns(n) { return n === 1 ? "1 run" : n + " runs"; }
  function decide(claim, runs) {
    var n = runs.length;
    if (n === 0) return { state: "NOT_REPRODUCED", reason: "the proof of concept was not run (0 runs)", findings: [] };
    var crashed = 0, timedOut = 0, oom = 0;
    runs.forEach(function (r) {
      if (r.crash) crashed++;
      else if (r.timedOut) timedOut++;
      else if (r.oom) oom++;
    });
    if (crashed === 0) {
      if (timedOut > 0) return { state: "TIMEOUT", reason: timedOut + " of " + nRuns(n) + " hit the time limit", findings: [] };
      if (oom > 0) return { state: "OOM", reason: oom + " of " + nRuns(n) + " hit the memory limit", findings: [] };
      return { state: "NOT_REPRODUCED", reason: "no crash in " + nRuns(n), findings: [] };
    }
    var counts = {}, order = [];
    runs.forEach(function (r) {
      if (!r.crash) return;
      if (!counts[r.crash.signature]) order.push(r.crash);
      counts[r.crash.signature] = (counts[r.crash.signature] || 0) + 1;
    });
    var crash = null, count = 0;
    order.forEach(function (c) { if (counts[c.signature] > count) { crash = c; count = counts[c.signature]; } });
    var cmp = compare(claim, crash);
    var state, reason = "crashed in " + crashed + " of " + nRuns(n);
    if (crashed < n) state = "FLAKY";
    else if (cmp.match) { state = "REPRODUCED_AS_CLAIMED"; reason += ", as claimed"; }
    else { state = "REPRODUCED_DIFFERENT"; reason += ", but not as claimed"; }
    if (order.length > 1) reason += "; " + order.length + " different crash signatures, the most common one was seen in " + nRuns(count);
    return { state: state, reason: reason, crash: crash, findings: cmp.findings };
  }

  /* SHA-256 (FIPS 180-4), synchronous so the page can hash on every keystroke */
  var K = [0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2];
  function utf8(s) {
    if (typeof TextEncoder !== "undefined") return new TextEncoder().encode(s);
    return Uint8Array.from(unescape(encodeURIComponent(s)), function (c) { return c.charCodeAt(0); });
  }
  function sha256hex(str) {
    var msg = utf8(str), len = msg.length;
    var total = ((len + 9 + 63) >> 6) << 6;
    var b = new Uint8Array(total);
    b.set(msg); b[len] = 0x80;
    var bits = len * 8;
    b[total - 4] = (bits >>> 24) & 255; b[total - 3] = (bits >>> 16) & 255; b[total - 2] = (bits >>> 8) & 255; b[total - 1] = bits & 255;
    b[total - 5] = Math.floor(len / 0x20000000) & 255;
    var h = [0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19];
    var w = new Array(64);
    for (var off = 0; off < total; off += 64) {
      for (var i = 0; i < 16; i++) w[i] = (b[off + 4 * i] << 24) | (b[off + 4 * i + 1] << 16) | (b[off + 4 * i + 2] << 8) | b[off + 4 * i + 3];
      for (i = 16; i < 64; i++) {
        var x = w[i - 15], y = w[i - 2];
        var s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3);
        var s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10);
        w[i] = (w[i - 16] + s0 + w[i - 7] + s1) | 0;
      }
      var a = h[0], bb = h[1], c = h[2], d = h[3], e = h[4], f = h[5], g = h[6], hh = h[7];
      for (i = 0; i < 64; i++) {
        var S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
        var ch = (e & f) ^ (~e & g);
        var t1 = (hh + S1 + ch + K[i] + w[i]) | 0;
        var S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
        var maj = (a & bb) ^ (a & c) ^ (bb & c);
        var t2 = (S0 + maj) | 0;
        hh = g; g = f; f = e; e = (d + t1) | 0; d = c; c = bb; bb = a; a = (t1 + t2) | 0;
      }
      h[0] = (h[0] + a) | 0; h[1] = (h[1] + bb) | 0; h[2] = (h[2] + c) | 0; h[3] = (h[3] + d) | 0;
      h[4] = (h[4] + e) | 0; h[5] = (h[5] + f) | 0; h[6] = (h[6] + g) | 0; h[7] = (h[7] + hh) | 0;
    }
    return h.map(function (v) { return ("00000000" + (v >>> 0).toString(16)).slice(-8); }).join("");
  }

  var api = { parse: parse, projectFrames: projectFrames, skipReason: skipReason, trimArgs: trimArgs,
    compare: compare, decide: decide, normalizeKind: normalizeKind, sha256hex: sha256hex };
  if (typeof module !== "undefined" && module.exports) module.exports = api;
  else root.RB = api;
})(this);

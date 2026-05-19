// WLTTS frontend — handles UI, calls backend, syncs word highlight to audio.

const $ = (s) => document.querySelector(s);

const els = {
  url: $("#url"),
  load: $("#load"),
  voice: $("#voice"),
  rate: $("#rate"),
  rateOut: $("#rateOut"),
  prepare: $("#prepare"),
  play: $("#play"),
  pause: $("#pause"),
  stop: $("#stop"),
  prep: $("#prep"),
  article: $("#article"),
  side: $("#sidepanel"),
  applyPrep: $("#applyPrep"),
  closePrep: $("#closePrep"),
  editText: $("#editText"),
  replacements: $("#replacements"),
  fontFace: $("#opt_font_face"),
  fontSize: $("#opt_font_size"),
  theme: $("#opt_theme"),
  topbar: $(".topbar"),
  player: $("#player"),
  status: $("#status"),
  progress: $("#progress"),
  progressFill: $("#progressFill"),
  progressTime: $("#progressTime"),
};

const state = {
  rawText: "",       // text as extracted from the page
  spokenText: "",    // text after preprocessing — what was sent to TTS
  // Each entry: { offset_ms, duration_ms, charStart, charEnd, spanStart, spanEnd }.
  // spanStart/spanEnd are inclusive indices into spans[] for highlighting.
  boundaries: [],
  spans: [],         // word <span> elements in document order
  spanRanges: [],    // [{start, end}] char offsets in spokenText for each span
  paraOf: [],        // span index -> paragraph element
  activeBoundary: -1,
  activePara: null,
  ready: false,
  prepCtrl: null,    // AbortController while audio is being prepared
  prepProgress: null, // {done, total} — drives the top progress bar during prep
};

const BACKEND_TIMEOUT = 120_000;

// ---- Config persistence --------------------------------------------------
// Backed by the Go server (~/.config/wltts/config.json) so settings survive
// across app restarts regardless of the embedded webview's storage policy.

let configCache = {};
let configWriteTimer = null;

async function fetchConfig() {
  try {
    const r = await fetch("/api/config");
    if (!r.ok) return {};
    return (await r.json()) || {};
  } catch {
    return {};
  }
}

function saveConfig(patch) {
  configCache = { ...configCache, ...patch };
  // Coalesce rapid changes (e.g. dragging the rate slider, typing in textarea).
  clearTimeout(configWriteTimer);
  configWriteTimer = setTimeout(() => {
    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(configCache),
    }).catch(() => {});
  }, 200);
}

function snapshotPrepOpts() {
  return {
    normalize_whitespace: $("#opt_normalize_whitespace").checked,
    strip_urls: $("#opt_strip_urls").checked,
    strip_citations: $("#opt_strip_citations").checked,
    strip_emoji: $("#opt_strip_emoji").checked,
    expand_abbreviations: $("#opt_expand_abbreviations").checked,
    replacements: els.replacements.value,
  };
}

function applySavedConfig() {
  const cfg = configCache;
  if (cfg.url) els.url.value = cfg.url;
  if (typeof cfg.rate === "number") {
    els.rate.value = String(cfg.rate);
    els.rateOut.textContent = formatRate(cfg.rate);
  }
  const prep = cfg.prep || {};
  for (const [k, sel] of [
    ["normalize_whitespace", "#opt_normalize_whitespace"],
    ["strip_urls", "#opt_strip_urls"],
    ["strip_citations", "#opt_strip_citations"],
    ["strip_emoji", "#opt_strip_emoji"],
    ["expand_abbreviations", "#opt_expand_abbreviations"],
  ]) {
    if (typeof prep[k] === "boolean") $(sel).checked = prep[k];
  }
  if (typeof prep.replacements === "string") els.replacements.value = prep.replacements;

  if (cfg.fontFace) els.fontFace.value = cfg.fontFace;
  if (cfg.fontSize) els.fontSize.value = String(cfg.fontSize);
  if (cfg.theme) els.theme.value = cfg.theme;
  applyReaderSettings();
}

function applyReaderSettings() {
  const face = els.fontFace.value;
  const size = els.fontSize.value;
  const theme = els.theme.value;
  const root = document.documentElement;

  // Apply font face
  let family = "var(--font-sans)";
  if (face === "serif") family = "var(--font-serif)";
  if (face === "charter") family = "var(--font-charter)";
  if (face === "palatino") family = "var(--font-palatino)";
  if (face === "monospace") family = "var(--font-mono)";
  root.style.setProperty("--article-font-family", family);
  root.style.setProperty("--article-font-size", size + "px");

  // Apply theme class to body
  document.body.className = "theme-" + theme;
}

// ---- API helpers ---------------------------------------------------------

async function api(path, body) {
  const ctrl = new AbortController();
  const t = setTimeout(() => ctrl.abort(), BACKEND_TIMEOUT);
  try {
    const r = await fetch(path, {
      method: body ? "POST" : "GET",
      headers: body ? { "Content-Type": "application/json" } : undefined,
      body: body ? JSON.stringify(body) : undefined,
      signal: ctrl.signal,
    });
    if (!r.ok) throw new Error(await r.text());
    return r.headers.get("content-type")?.includes("json") ? r.json() : r.text();
  } finally {
    clearTimeout(t);
  }
}

function status(msg, kind = "") {
  els.status.textContent = msg;
  els.status.className = "status show " + kind;
  if (msg) {
    clearTimeout(status._t);
    status._t = setTimeout(() => (els.status.className = "status"), 4000);
  }
}

function setTopBarVisible(visible) {
  els.topbar.classList.toggle("hidden", !visible);
}

// ---- Voice list ----------------------------------------------------------

async function loadVoices() {
  const voices = await api("/api/voices");
  // Group by locale for readability.
  const groups = {};
  voices.forEach((v) => (groups[v.locale] = groups[v.locale] || []).push(v));
  els.voice.innerHTML = "";
  Object.keys(groups).sort().forEach((loc) => {
    const og = document.createElement("optgroup");
    og.label = loc;
    groups[loc].forEach((v) => {
      const opt = document.createElement("option");
      opt.value = v.id;
      opt.textContent = v.label;
      og.appendChild(opt);
    });
    els.voice.appendChild(og);
  });
  // Default: saved preference, else pt-BR Francisca for pt locale, else en-US Aria.
  const fallback = navigator.language?.startsWith("pt") ? "pt-BR-FranciscaNeural" : "en-US-AriaNeural";
  const wanted = configCache.voice || fallback;
  if ([...els.voice.options].some((o) => o.value === wanted)) {
    els.voice.value = wanted;
  } else {
    els.voice.value = fallback;
  }
}

// ---- Article load --------------------------------------------------------

async function loadArticle() {
  const url = els.url.value.trim();
  if (!url) return;
  saveConfig({ url });
  status("Loading article…");
  els.article.classList.add("empty");
  els.article.innerHTML = `<p class="hint">Fetching…</p>`;
  try {
    const { article } = await api("/api/fetch", { url });
    state.rawText = article.text || "";
    els.editText.value = state.rawText;
    renderArticle(article, state.rawText);
    status("Loaded. Preparing audio…");
    els.article.focus({ preventScroll: true });
    await prepare();
    if (state.ready) {
      try { await els.player.play(); } catch {}
    }
  } catch (e) {
    els.article.innerHTML = `<p class="hint">Failed: ${escapeHTML(String(e.message || e))}</p>`;
    status("Load failed: " + (e.message || e), "error");
  }
}

function renderArticle(article, text) {
  els.article.classList.remove("empty");
  state.spokenText = text;
  state.boundaries = [];
  state.spans = [];
  state.spanRanges = [];
  state.paraOf = [];
  state.activeBoundary = -1;
  state.activePara = null;

  const meta = [];
  if (article.byline) meta.push(escapeHTML(article.byline));
  if (article.site_name) meta.push(escapeHTML(article.site_name));

  // Find paragraph boundaries with char offsets in the original text so
  // that boundary.charStart maps cleanly to a paragraph and span.
  const paraRanges = []; // [{start, end}] in text
  const paraSplit = /\n{2,}/g;
  let lastEnd = 0;
  let m;
  while ((m = paraSplit.exec(text))) {
    paraRanges.push({ start: lastEnd, end: m.index });
    lastEnd = m.index + m[0].length;
  }
  paraRanges.push({ start: lastEnd, end: text.length });

  let html = "";
  if (article.title) html += `<h1 class="title">${escapeHTML(article.title)}</h1>`;
  if (meta.length) html += `<div class="meta">${meta.join(" · ")}</div>`;
  html += `<div class="body">`;

  let spanIdx = 0;
  paraRanges.forEach((pr, pi) => {
    const slice = text.slice(pr.start, pr.end);
    const para = slice.trim();
    if (!para) return;
    const paraOffset = pr.start + slice.indexOf(para);
    const tokens = tokenizeWithOffsets(para);
    const parts = [];
    for (const tok of tokens) {
      if (tok.kind === "word") {
        const cStart = paraOffset + tok.start;
        const cEnd = paraOffset + tok.end;
        parts.push(`<span class="w" data-i="${spanIdx}" data-cs="${cStart}" data-ce="${cEnd}">${escapeHTML(tok.text)}</span>`);
        spanIdx++;
      } else {
        parts.push(escapeHTML(tok.text));
      }
    }
    html += `<p data-p="${pi}">${parts.join("")}</p>`;
  });
  html += `</div>`;
  els.article.innerHTML = html;

  els.article.querySelectorAll(".w").forEach((sp) => {
    state.spans.push(sp);
    state.spanRanges.push({ start: +sp.dataset.cs, end: +sp.dataset.ce });
    state.paraOf.push(sp.parentElement);
  });
  // New text invalidates any prepared audio; cancels in-flight prep too.
  invalidatePrep();
}

// Split text into word/non-word tokens with char offsets. Returns objects
// with {kind:"word"|"skip", text, start, end} where [start, end) are
// indices into the input string.
const tokenRe = /(\p{L}[\p{L}\p{M}'’]*|\d[\d.,]*)|(\s+|[^\p{L}\p{M}\d\s]+)/gu;
function tokenizeWithOffsets(text) {
  tokenRe.lastIndex = 0;
  const out = [];
  let m;
  while ((m = tokenRe.exec(text))) {
    out.push({
      kind: m[1] ? "word" : "skip",
      text: m[1] || m[2],
      start: m.index,
      end: m.index + (m[1] || m[2]).length,
    });
  }
  return out;
}

// Map TTS word boundaries onto character ranges in the original spoken text.
// Edge TTS sometimes groups multiple visible "words" into a single boundary
// (e.g. "1.50 dollars", "e.g."), so we can't assume 1:1 with our spans.
// Instead we walk the text with a cursor, finding each boundary.text in
// order. Each boundary then records the span range it covers.
function alignBoundaries(text, words) {
  const out = [];
  let cursor = 0;
  for (const w of words) {
    const t = w.text;
    if (!t) continue;
    let idx = text.indexOf(t, cursor);
    if (idx < 0) {
      // Tolerate small mismatches — fall back to a case-insensitive search.
      idx = text.toLowerCase().indexOf(t.toLowerCase(), cursor);
    }
    if (idx < 0) continue;
    const charStart = idx;
    const charEnd = idx + t.length;
    cursor = charEnd;

    // Find span range whose chars overlap [charStart, charEnd).
    let spanStart = -1, spanEnd = -1;
    for (let i = 0; i < state.spanRanges.length; i++) {
      const r = state.spanRanges[i];
      if (r.end <= charStart) continue;
      if (r.start >= charEnd) break;
      if (spanStart === -1) spanStart = i;
      spanEnd = i;
    }
    out.push({
      offset_ms: w.offset_ms,
      duration_ms: w.duration_ms,
      charStart, charEnd,
      spanStart, spanEnd,
    });
  }
  return out;
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c]));
}

// ---- Preprocess panel ----------------------------------------------------

function readPrepOpts() {
  const opts = {
    normalize_whitespace: $("#opt_normalize_whitespace").checked,
    strip_urls: $("#opt_strip_urls").checked,
    strip_citations: $("#opt_strip_citations").checked,
    strip_emoji: $("#opt_strip_emoji").checked,
    expand_abbreviations: $("#opt_expand_abbreviations").checked,
    replacements: {},
  };
  els.replacements.value.split("\n").forEach((line) => {
    const i = line.indexOf("=>");
    if (i < 0) return;
    const k = line.slice(0, i).trim();
    const v = line.slice(i + 2).trim();
    if (k) opts.replacements[k] = v;
  });
  return opts;
}

async function applyPrep() {
  const userText = els.editText.value;
  const opts = readPrepOpts();
  status("Preprocessing…");
  try {
    const { text } = await api("/api/preprocess", { text: userText, opts });
    renderArticle({ title: $("#article h1.title")?.textContent || "", byline: "", site_name: "" }, text);
    els.editText.value = text;
    els.side.hidden = true;
    status("Text updated.");
  } catch (e) {
    status("Preprocess failed: " + (e.message || e), "error");
  }
}

// ---- TTS playback --------------------------------------------------------

// Invalidate cached synthesis. Called when the input text or voice/rate
// changes — the next play needs a fresh Prepare.
function invalidatePrep() {
  if (state.prepCtrl) {
    state.prepCtrl.abort();
    state.prepCtrl = null;
  }
  state.prepProgress = null;
  state.ready = false;
  state.boundaries = [];
  els.player.removeAttribute("src");
  els.player.load();
  refreshTransport();
  updateProgress();
}

// Sync transport button enabled/labels to the current state.
function refreshTransport() {
  const preparing = !!state.prepCtrl;
  els.prepare.textContent = preparing ? "✕ Cancel" : (state.ready ? "✓ Ready" : "⏳ Prepare");
  els.prepare.disabled = !state.spokenText && !preparing;
  els.play.disabled = preparing || !state.ready;
  if (!state.ready) {
    els.pause.disabled = true;
    els.stop.disabled = true;
  }
}

async function prepare() {
  // If a prep is in flight, the button acts as Cancel.
  if (state.prepCtrl) {
    state.prepCtrl.abort();
    return;
  }
  if (!state.spokenText) {
    status("Nothing to prepare. Load an article first.", "error");
    return;
  }
  if (state.ready) return; // already prepared

  const ctrl = new AbortController();
  state.prepCtrl = ctrl;
  state.prepProgress = { done: 0, total: 0 };
  refreshTransport();
  updateProgress();
  status("Preparing audio…");

  let result = null;
  let errMsg = null;
  try {
    const rate = formatRate(parseInt(els.rate.value, 10));
    const r = await fetch("/api/synthesize", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        text: state.spokenText,
        voice: els.voice.value,
        rate,
        pitch: "+0Hz",
      }),
      signal: ctrl.signal,
    });
    if (!r.ok) throw new Error(await r.text());
    const reader = r.body.getReader();
    const decoder = new TextDecoder();
    let buf = "";
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      // NDJSON: process complete lines, keep the trailing partial.
      let nl;
      while ((nl = buf.indexOf("\n")) >= 0) {
        const line = buf.slice(0, nl).trim();
        buf = buf.slice(nl + 1);
        if (!line) continue;
        let ev;
        try { ev = JSON.parse(line); } catch { continue; }
        if (ev.type === "progress") {
          state.prepProgress = { done: ev.done, total: ev.total };
          updateProgress();
          if (ev.total > 0) status(`Preparing audio… ${ev.done}/${ev.total}`);
        } else if (ev.type === "result") {
          result = ev;
        } else if (ev.type === "error") {
          errMsg = ev.message || "synthesis failed";
        }
      }
    }
  } catch (e) {
    if (e.name === "AbortError") {
      status("Preparation canceled.");
    } else {
      errMsg = e.message || String(e);
    }
  } finally {
    state.prepCtrl = null;
    state.prepProgress = null;
  }

  if (result) {
    state.boundaries = alignBoundaries(state.spokenText, result.words || []);
    els.player.src = result.audio_url + "?t=" + Date.now();
    state.ready = true;
    status("Ready. Press Play.");
  } else if (errMsg) {
    status("Synthesis failed: " + errMsg, "error");
  }
  refreshTransport();
  updateProgress();
}

async function play() {
  if (!state.ready) {
    // Haven't prepared yet — kick off prep, then play when it finishes.
    await prepare();
    if (!state.ready) return;
  }
  try {
    await els.player.play();
    els.pause.disabled = false;
    els.stop.disabled = false;
  } catch (e) {
    status("Playback failed: " + (e.message || e), "error");
  }
}

function pause() {
  els.player.pause();
}

function stop() {
  els.player.pause();
  els.player.currentTime = 0;
  clearHighlight();
  els.pause.disabled = true;
  els.stop.disabled = true;
}

function formatRate(pct) {
  return (pct >= 0 ? "+" : "") + pct + "%";
}

function setActiveBoundary(idx) {
  // Clear previously active spans (active boundary may cover multiple).
  if (state.activeBoundary >= 0) {
    const prev = state.boundaries[state.activeBoundary];
    if (prev) {
      for (let i = prev.spanStart; i <= prev.spanEnd; i++) {
        state.spans[i]?.classList.remove("active-word");
      }
    }
  }
  state.activeBoundary = idx;
  if (idx < 0) {
    if (state.activePara) state.activePara.classList.remove("active-para");
    state.activePara = null;
    return;
  }
  const b = state.boundaries[idx];
  if (!b || b.spanStart < 0) return;
  for (let i = b.spanStart; i <= b.spanEnd; i++) {
    state.spans[i]?.classList.add("active-word");
  }
  const firstSpan = state.spans[b.spanStart];
  const para = state.paraOf[b.spanStart];
  if (para !== state.activePara) {
    if (state.activePara) state.activePara.classList.remove("active-para");
    state.activePara = para;
    para?.classList.add("active-para");
  }
  if (!firstSpan) return;
  const r = firstSpan.getBoundingClientRect();
  const ar = els.article.getBoundingClientRect();
  if (r.bottom > ar.bottom - 80 || r.top < ar.top + 60) {
    firstSpan.scrollIntoView({ behavior: "smooth", block: "center" });
  }
}

function clearHighlight() {
  setActiveBoundary(-1);
}

function indexAt(currentMs) {
  const bs = state.boundaries;
  if (!bs.length) return -1;
  let i = state.activeBoundary >= 0 ? state.activeBoundary : 0;
  if (bs[i] && bs[i].offset_ms > currentMs) i = 0;
  while (i + 1 < bs.length && bs[i + 1].offset_ms <= currentMs) i++;
  if (bs[i] && bs[i].offset_ms > currentMs) return -1;
  return i;
}

function onTimeUpdate() {
  updateProgress();
  if (!state.ready || !state.boundaries.length) return;
  const ms = els.player.currentTime * 1000;
  const i = indexAt(ms);
  if (i !== state.activeBoundary) setActiveBoundary(i);
}

function fmtTime(sec) {
  if (!isFinite(sec) || sec < 0) sec = 0;
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return m + ":" + (s < 10 ? "0" : "") + s;
}

function updateProgress() {
  // Preparation in progress: show chunk-based progress instead of audio time.
  if (state.prepProgress) {
    const { done, total } = state.prepProgress;
    const pct = total > 0 ? (done / total) * 100 : 0;
    els.progressFill.style.width = pct + "%";
    els.progress.setAttribute("aria-valuenow", String(Math.round(pct)));
    els.progressTime.textContent = total > 0
      ? `Preparing ${done}/${total}`
      : "Preparing…";
    els.progress.classList.add("has-audio", "preparing");
    els.progress.classList.add("preparing");
    return;
  }
  els.progress.classList.remove("preparing");
  const cur = els.player.currentTime || 0;
  const dur = els.player.duration || 0;
  const pct = dur > 0 ? (cur / dur) * 100 : 0;
  els.progressFill.style.width = pct + "%";
  els.progress.setAttribute("aria-valuenow", String(Math.round(pct)));
  els.progressTime.textContent = fmtTime(cur) + " / " + fmtTime(dur);
  els.progress.classList.toggle("has-audio", dur > 0);
}

function seekFromEvent(e) {
  if (state.prepProgress) return; // bar is showing prep progress, not audio
  const dur = els.player.duration || 0;
  if (!dur) return;
  const rect = els.progress.getBoundingClientRect();
  const x = Math.min(Math.max(e.clientX - rect.left, 0), rect.width);
  els.player.currentTime = (x / rect.width) * dur;
  updateProgress();
}

// ---- Wire up UI ----------------------------------------------------------

els.load.addEventListener("click", loadArticle);
els.url.addEventListener("keydown", (e) => { if (e.key === "Enter") loadArticle(); });

els.rate.addEventListener("input", () => {
  const v = parseInt(els.rate.value, 10);
  els.rateOut.textContent = formatRate(v);
  saveConfig({ rate: v });
  // Changing rate invalidates the cached synthesis — re-prepare next time.
  invalidatePrep();
});
els.voice.addEventListener("change", () => {
  saveConfig({ voice: els.voice.value });
  invalidatePrep();
});

// Persist preprocess panel state on change.
[
  "#opt_normalize_whitespace",
  "#opt_strip_urls",
  "#opt_strip_citations",
  "#opt_strip_emoji",
  "#opt_expand_abbreviations",
].forEach((sel) => $(sel).addEventListener("change", () => saveConfig({ prep: snapshotPrepOpts() })));
els.replacements.addEventListener("input", () => saveConfig({ prep: snapshotPrepOpts() }));

els.fontFace.addEventListener("change", () => {
  applyReaderSettings();
  saveConfig({ fontFace: els.fontFace.value });
});
els.fontSize.addEventListener("input", () => {
  applyReaderSettings();
  saveConfig({ fontSize: parseInt(els.fontSize.value, 10) });
});
els.theme.addEventListener("change", () => {
  applyReaderSettings();
  saveConfig({ theme: els.theme.value });
});

  // Ctrl+L: focus and select the address bar (browser-style).
  // Vim-style article navigation: j/k scroll, Ctrl-F/Ctrl-B page, gg/G top/bottom.

let lastG = 0;
window.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "l") {
    e.preventDefault();
    setTopBarVisible(true);
    els.url.focus();
    els.url.select();
    return;
  }
  // Don't hijack keys while typing in inputs/textareas/selects.
  const t = e.target;
  if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) {
    return;
  }
  const a = els.article;
  const step = 60;
  const page = Math.max(a.clientHeight - 40, 100);
  if (e.ctrlKey && !e.shiftKey && !e.altKey && !e.metaKey && e.key.toLowerCase() === "f") {
    e.preventDefault();
    a.scrollBy({ top: page, behavior: "smooth" });
    return;
  }
  if (e.ctrlKey && !e.shiftKey && !e.altKey && !e.metaKey && e.key.toLowerCase() === "b") {
    e.preventDefault();
    a.scrollBy({ top: -page, behavior: "smooth" });
    return;
  }
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  if (e.key === " ") {
    e.preventDefault();
    if (els.player.paused) play(); else pause();
    return;
  }
  if (e.key === "q") {
    e.preventDefault();
    if (typeof window.wlttsQuit === "function") window.wlttsQuit();
    else window.close();
    return;
  }
  if (e.key === ",") {
    e.preventDefault();
    if (els.side.hidden) els.editText.value = state.spokenText || state.rawText;
    els.side.hidden = !els.side.hidden;
    return;
  }
  if (e.key === "j") {
    e.preventDefault();
    a.scrollBy({ top: step, behavior: "smooth" });
  } else if (e.key === "k") {
    e.preventDefault();
    a.scrollBy({ top: -step, behavior: "smooth" });
  } else if (e.key === "G") {
    e.preventDefault();
    a.scrollTo({ top: a.scrollHeight, behavior: "smooth" });
  } else if (e.key === "g") {
    e.preventDefault();
    const now = Date.now();
    if (now - lastG < 500) {
      a.scrollTo({ top: 0, behavior: "smooth" });
      lastG = 0;
    } else {
      lastG = now;
    }
  }
});

els.prepare.addEventListener("click", prepare);
els.play.addEventListener("click", play);
els.pause.addEventListener("click", pause);
els.stop.addEventListener("click", stop);

els.prep.addEventListener("click", () => {
  els.editText.value = state.spokenText || state.rawText;
  els.side.hidden = !els.side.hidden;
});
els.closePrep.addEventListener("click", () => (els.side.hidden = true));
els.applyPrep.addEventListener("click", applyPrep);

els.player.addEventListener("timeupdate", onTimeUpdate);
els.player.addEventListener("seeked", () => { setActiveBoundary(-1); onTimeUpdate(); });
els.player.addEventListener("ended", () => {
  els.pause.disabled = true;
  els.stop.disabled = true;
  setTopBarVisible(true);
});
els.player.addEventListener("playing", () => {
  els.pause.disabled = false;
  els.stop.disabled = false;
  setTopBarVisible(false);
});
els.player.addEventListener("pause", () => {
  setTopBarVisible(true);
});
els.player.addEventListener("loadedmetadata", updateProgress);
els.player.addEventListener("durationchange", updateProgress);
els.player.addEventListener("emptied", updateProgress);

els.progress.addEventListener("click", seekFromEvent);

(async () => {
  configCache = await fetchConfig();
  applySavedConfig();
  els.rateOut.textContent = formatRate(parseInt(els.rate.value, 10) || 0);
  refreshTransport();
  els.url.focus();
  els.url.select();
  try {
    await loadVoices();
  } catch (e) {
    status("Voice list failed: " + e.message, "error");
  }
})();

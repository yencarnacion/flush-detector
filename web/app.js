const statusPill = document.getElementById("statusPill");
const statusMeta = document.getElementById("statusMeta");
const searchInput = document.getElementById("searchInput");
const sortSelect = document.getElementById("sortSelect");
const soundBtn = document.getElementById("soundBtn");
const soundStateBadge = document.getElementById("soundStateBadge");
const soundStateText = document.getElementById("soundStateText");
const reloadBtn = document.getElementById("reloadBtn");
const applyBtn = document.getElementById("applyBtn");
const watchlistSection = document.getElementById("watchlistSection");
const watchlistToggle = document.getElementById("watchlistToggle");
const watchlistSummary = document.getElementById("watchlistSummary");
const watchlistTags = document.getElementById("watchlistTags");
const pinnedList = document.getElementById("pinnedList");
const liveList = document.getElementById("liveList");
const historyList = document.getElementById("historyList");
const pinnedCount = document.getElementById("pinnedCount");
const liveCount = document.getElementById("liveCount");
const historyCount = document.getElementById("historyCount");
const alertAudio = document.getElementById("alertAudio");
const minAlertScore = document.getElementById("minAlertScore");
const startTime = document.getElementById("startTime");
const endTime = document.getElementById("endTime");
const minBars = document.getElementById("minBars");
const requireVWAP = document.getElementById("requireVWAP");
const requireDrop = document.getElementById("requireDrop");

let ws;
let configState = null;
let alerts = [];
let soundEnabled = localStorage.getItem("flush-detector.sound") !== "off";
let audioPrimed = false;
let audioPriming = false;
let watchlistExpanded = localStorage.getItem("flush-detector.watchlist-expanded") === "true";
let extraCache = new Map();
let pinSet = new Set(JSON.parse(localStorage.getItem("flush-detector.pins") || "[]"));

updateSoundUI();
updateWatchlistUI();

function connectWS() {
  const protocol = location.protocol === "https:" ? "wss" : "ws";
  ws = new WebSocket(`${protocol}://${location.host}/ws`);
  ws.onopen = () => setStatus("Connected", "Live websocket session");
  ws.onclose = () => {
    setStatus("Disconnected", "Retrying websocket in 2s");
    setTimeout(connectWS, 2000);
  };
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data);
    if (msg.type === "status") {
      applyStatus(msg.payload);
    } else if (msg.type === "history") {
      alerts = Array.isArray(msg.payload) ? msg.payload : [];
      render();
    } else if (msg.type === "flush_alert") {
      alerts.unshift(msg.payload);
      alerts = alerts.slice(0, 200);
      render();
      playAlertSound();
    } else if (msg.type === "config") {
      configState = msg.payload;
      hydrateControls();
    } else if (msg.type === "watchlist") {
      renderWatchlist(msg.payload);
    }
  };
}

function setStatus(label, meta) {
  statusPill.textContent = label;
  statusMeta.textContent = meta || "";
}

function updateSoundUI() {
  soundBtn.textContent = `Sound: ${soundEnabled ? "On" : "Off"}`;
  if (!soundEnabled) {
    soundStateBadge.textContent = "Disabled";
    soundStateBadge.className = "sound-badge disabled";
    soundStateText.textContent = "Alert audio is muted in this browser tab.";
    return;
  }
  if (audioPriming) {
    soundStateBadge.textContent = "Priming";
    soundStateBadge.className = "sound-badge priming";
    soundStateText.textContent = "Unlocking browser audio for alert playback.";
    return;
  }
  if (audioPrimed) {
    soundStateBadge.textContent = "Ready";
    soundStateBadge.className = "sound-badge ready";
    soundStateText.textContent = "Alert audio is unlocked and ready to play.";
    return;
  }
  soundStateBadge.textContent = "Click To Enable";
  soundStateBadge.className = "sound-badge blocked";
  soundStateText.textContent = "Chrome usually needs one click or key press before alert audio can play.";
}

async function primeAudio() {
  if (!soundEnabled || audioPrimed || audioPriming) return;
  audioPriming = true;
  updateSoundUI();
  try {
    alertAudio.muted = true;
    alertAudio.currentTime = 0;
    await alertAudio.play();
    alertAudio.pause();
    alertAudio.currentTime = 0;
    alertAudio.muted = false;
    audioPrimed = true;
  } catch {
    audioPrimed = false;
  } finally {
    alertAudio.muted = false;
    audioPriming = false;
    updateSoundUI();
  }
}

function requestAudioPrime() {
  if (!soundEnabled || audioPrimed) return;
  primeAudio().catch(() => {});
}

async function playAlertSound() {
  if (!soundEnabled) return;
  if (!audioPrimed) {
    updateSoundUI();
    return;
  }
  try {
    alertAudio.currentTime = 0;
    await alertAudio.play();
  } catch {
    audioPrimed = false;
    updateSoundUI();
  }
}

function applyStatus(payload) {
  if (!payload) return;
  statusPill.textContent = payload.text || "Live";
  statusMeta.textContent = `${payload.symbols || 0} symbols • ${payload.alerts || 0} alerts • ${payload.updated_at || ""}`;
}

function hydrateControls() {
  if (!configState) return;
  minAlertScore.value = configState.flush.min_alert_score;
  startTime.value = configState.flush.start_time;
  endTime.value = configState.flush.end_time;
  minBars.value = configState.flush.min_bars_before_alerts;
  requireVWAP.checked = !!configState.flush.require_below_vwap;
  requireDrop.checked = !!configState.flush.require_drop_from_recent_high;
}

function renderWatchlist(payload) {
  const list = Array.isArray(payload) ? payload : payload?.watchlist || [];
  const sourceSet = new Set();
  watchlistTags.innerHTML = "";
  list.forEach((item) => {
    const tag = document.createElement("span");
    tag.className = "tag";
    const sources = item.sources?.length ? ` · ${item.sources.join(", ")}` : "";
    (item.sources || []).forEach((source) => sourceSet.add(source));
    tag.textContent = `${item.symbol}${sources}`;
    watchlistTags.appendChild(tag);
  });
  const sourceCount = sourceSet.size;
  watchlistSummary.textContent = sourceCount > 0
    ? `${list.length} symbols from ${sourceCount} watchlist${sourceCount === 1 ? "" : "s"}`
    : `${list.length} symbols loaded`;
}

function updateWatchlistUI() {
  watchlistSection.classList.toggle("collapsed", !watchlistExpanded);
  watchlistToggle.setAttribute("aria-expanded", String(watchlistExpanded));
}

function formatTime(value) {
  try {
    return new Date(value).toLocaleString();
  } catch {
    return value;
  }
}

function minuteBucket(value) {
  const ts = new Date(value).getTime();
  if (Number.isNaN(ts)) return Number.NEGATIVE_INFINITY;
  return Math.floor(ts / 60000);
}

function filterAlerts() {
  const q = searchInput.value.trim().toUpperCase();
  let rows = alerts.slice();
  if (q) {
    rows = rows.filter((alert) => {
      return (alert.symbol || "").includes(q) || (alert.name || "").toUpperCase().includes(q);
    });
  }
  if (sortSelect.value === "score") {
    rows.sort((a, b) => (b.flush_score || 0) - (a.flush_score || 0));
  } else {
    rows.sort((a, b) => {
      const minuteDiff = minuteBucket(b.alert_time) - minuteBucket(a.alert_time);
      if (minuteDiff !== 0) return minuteDiff;

      const scoreDiff = (b.flush_score || 0) - (a.flush_score || 0);
      if (scoreDiff !== 0) return scoreDiff;

      return new Date(b.alert_time) - new Date(a.alert_time);
    });
  }
  return rows;
}

function render() {
  const filtered = filterAlerts();
  const live = filtered.slice(0, 20);
  const history = filtered.slice(0, 80);
  const pinned = filtered.filter((alert) => pinSet.has(alert.id));

  renderList(pinnedList, pinned, true);
  renderList(liveList, live, false);
  renderList(historyList, history, false);

  pinnedCount.textContent = String(pinned.length);
  liveCount.textContent = String(live.length);
  historyCount.textContent = String(history.length);
}

function renderList(root, items, pinnedMode) {
  root.innerHTML = "";
  if (!items.length) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = pinnedMode ? "Pin any alert to keep it at the top." : "No alerts match the current filter.";
    root.appendChild(empty);
    return;
  }
  items.forEach((alert) => root.appendChild(renderCard(alert)));
}

function renderCard(alert) {
  const score = Number(alert.flush_score || 0);
  const tone = (alert.tier || "low").toLowerCase();
  const card = document.createElement("article");
  card.className = `alert-card ${tone}`;
  card.innerHTML = `
    <div class="alert-inner">
      <div class="card-top">
        <div>
          <div class="symbol-line">
            <h3 class="symbol">${alert.symbol}</h3>
            <span class="tier">${alert.tier}</span>
            ${(alert.sources || []).map((s) => `<span class="chip">${s}</span>`).join("")}
          </div>
          <div class="name">${alert.name || "No company name provided"} · ${formatTime(alert.alert_time)}</div>
        </div>
        <div class="score">
          <span>Flush Score</span>
          <strong>${score.toFixed(1)}</strong>
          <div>$${Number(alert.price || 0).toFixed(2)}</div>
        </div>
      </div>
      <p class="summary">${alert.summary || ""}</p>
      <div class="metric-grid">
        ${metric("30m drop", `${alert.metrics.drop_from_prior_30m_high_pct}%`)}
        ${metric("Below VWAP", `${alert.metrics.distance_below_vwap_pct}%`)}
        ${metric("5m ROC", `-${alert.metrics.roc_5m_pct}%`)}
        ${metric("10m ROC", `-${alert.metrics.roc_10m_pct}%`)}
        ${metric("Slope", `${alert.metrics.down_slope_20m_pct_per_bar}%`)}
        ${metric("Range", `x${alert.metrics.range_expansion}`)}
        ${metric("Volume", `x${alert.metrics.volume_expansion}`)}
      </div>
      <div class="card-actions">
        <button class="card-link chart-btn" type="button">Open Chart</button>
        <a class="card-link" href="/news.html?ticker=${encodeURIComponent(alert.symbol)}&date=${encodeURIComponent(alert.session_date)}&days=2" target="_blank" rel="noreferrer">Open Extras</a>
        <button class="pin-btn" type="button">${pinSet.has(alert.id) ? "Unpin" : "Pin"}</button>
      </div>
      <div class="extras">
        <h3>Recent News</h3>
        <div class="extras-list news-list"><div class="mini-meta">Loading…</div></div>
        <h3>SEC Filings</h3>
        <div class="extras-list filings-list"><div class="mini-meta">Loading…</div></div>
      </div>
    </div>
  `;

  card.querySelector(".chart-btn").addEventListener("click", () => {
    const base = configState?.ui?.chart_opener_base_url || "http://localhost:8081";
    window.open(`${base}/api/open-chart/${encodeURIComponent(alert.symbol)}/${encodeURIComponent(alert.session_date)}`, "_blank");
  });

  card.querySelector(".pin-btn").addEventListener("click", () => {
    if (pinSet.has(alert.id)) {
      pinSet.delete(alert.id);
    } else {
      pinSet.add(alert.id);
    }
    localStorage.setItem("flush-detector.pins", JSON.stringify(Array.from(pinSet)));
    render();
  });

  hydrateExtras(alert, card).catch(() => {});
  return card;
}

function metric(label, value) {
  return `<div class="metric"><span class="metric-label">${label}</span><span class="metric-value">${value}</span></div>`;
}

async function hydrateExtras(alert, card) {
  const key = `${alert.symbol}|${alert.session_date}`;
  let payload = extraCache.get(key);
  if (!payload) {
    const res = await fetch(`/api/extra?ticker=${encodeURIComponent(alert.symbol)}&date=${encodeURIComponent(alert.session_date)}&days=2`);
    payload = await res.json();
    extraCache.set(key, payload);
  }
  const newsRoot = card.querySelector(".news-list");
  const filingsRoot = card.querySelector(".filings-list");
  renderExtras(newsRoot, payload.news, (item) => `
    <div class="extra-item">
      <a href="${item.url}" target="_blank" rel="noreferrer">${item.title}</a>
      <div class="mini-meta">${item.source || "Unknown"} • ${item.published_utc || ""}</div>
    </div>
  `, "No recent news found.");
  renderExtras(filingsRoot, payload.filings, (item) => `
    <div class="extra-item">
      <a href="${item.url}" target="_blank" rel="noreferrer">${item.form || "Filing"}</a>
      <div class="mini-meta">${item.filed_at || ""}</div>
      <p>${item.description || ""}</p>
    </div>
  `, "No recent filings found.");
}

function renderExtras(root, items, template, emptyText) {
  root.innerHTML = "";
  if (!items || !items.length) {
    root.innerHTML = `<div class="mini-meta">${emptyText}</div>`;
    return;
  }
  root.innerHTML = items.map(template).join("");
}

async function bootstrap() {
  const [configRes, watchlistRes, historyRes] = await Promise.all([
    fetch("/api/config"),
    fetch("/api/watchlist"),
    fetch("/api/history"),
  ]);
  configState = await configRes.json();
  hydrateControls();
  renderWatchlist(await watchlistRes.json());
  const historyPayload = await historyRes.json();
  alerts = historyPayload.alerts || [];
  render();
  connectWS();
}

searchInput.addEventListener("input", render);
sortSelect.addEventListener("change", render);

soundBtn.addEventListener("click", () => {
  soundEnabled = !soundEnabled;
  localStorage.setItem("flush-detector.sound", soundEnabled ? "on" : "off");
  if (soundEnabled) {
    primeAudio().catch(() => {});
  } else {
    alertAudio.pause();
    alertAudio.currentTime = 0;
  }
  updateSoundUI();
});

reloadBtn.addEventListener("click", async () => {
  reloadBtn.disabled = true;
  try {
    await fetch("/api/watchlist/reload", { method: "POST" });
  } finally {
    reloadBtn.disabled = false;
  }
});

applyBtn.addEventListener("click", async () => {
  if (!configState) return;
  const payload = {
    flush: {
      ...configState.flush,
      min_alert_score: Number(minAlertScore.value),
      start_time: startTime.value,
      end_time: endTime.value,
      min_bars_before_alerts: Number(minBars.value),
      require_below_vwap: requireVWAP.checked,
      require_drop_from_recent_high: requireDrop.checked,
    },
    alert: {
      ...configState.alert,
      enable_sound: soundEnabled,
    },
  };
  const res = await fetch("/api/config/apply", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  configState = await res.json();
  hydrateControls();
});

watchlistToggle.addEventListener("click", () => {
  watchlistExpanded = !watchlistExpanded;
  localStorage.setItem("flush-detector.watchlist-expanded", String(watchlistExpanded));
  updateWatchlistUI();
});

document.addEventListener("pointerdown", requestAudioPrime, { capture: true });
document.addEventListener("keydown", requestAudioPrime, { capture: true });

bootstrap().catch((err) => {
  setStatus("Error", err.message);
});

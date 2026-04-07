const statusPill = document.getElementById("statusPill");
const statusMeta = document.getElementById("statusMeta");
const searchInput = document.getElementById("searchInput");
const sortSelect = document.getElementById("sortSelect");
const soundBtn = document.getElementById("soundBtn");
const soundStateBadge = document.getElementById("soundStateBadge");
const soundStateText = document.getElementById("soundStateText");
const replayDayBtn = document.getElementById("replayDayBtn");
const reloadBtn = document.getElementById("reloadBtn");
const applyBtn = document.getElementById("applyBtn");
const watchlistSection = document.getElementById("watchlistSection");
const watchlistToggle = document.getElementById("watchlistToggle");
const watchlistSummary = document.getElementById("watchlistSummary");
const watchlistTags = document.getElementById("watchlistTags");
const pinnedList = document.getElementById("pinnedList");
const liveList = document.getElementById("liveList");
const alertColumns = document.getElementById("alertColumns");
const appViewport = document.getElementById("appViewport");
const pageShell = document.querySelector(".page-shell");
const pinnedCount = document.getElementById("pinnedCount");
const liveCount = document.getElementById("liveCount");
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
let replayDayPending = false;
let watchlistExpanded = localStorage.getItem("flush-detector.watchlist-expanded") === "true";
let pinSet = new Set(JSON.parse(localStorage.getItem("flush-detector.pins") || "[]"));
let expandedMinutes = new Set();
let initialViewportPositioned = false;
let viewportPositionAttempts = 0;

updateSoundUI();
updateWatchlistUI();

if ("scrollRestoration" in history) {
  history.scrollRestoration = "manual";
}

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
      return;
    }
    if (msg.type === "history") {
      alerts = Array.isArray(msg.payload) ? msg.payload : [];
      render();
      return;
    }
    if (msg.type === "flush_alert") {
      alerts.unshift(msg.payload);
      alerts = alerts.slice(0, 200);
      render();
      playAlertSound();
      return;
    }
    if (msg.type === "config") {
      configState = msg.payload;
      hydrateControls();
      render();
      return;
    }
    if (msg.type === "watchlist") {
      renderWatchlist(msg.payload);
    }
  };
}

function setStatus(label, meta) {
  statusPill.textContent = label;
  statusMeta.textContent = meta || "";
  syncReplayButton(label);
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
  const text = payload.text || "Live";
  statusPill.textContent = text;
  statusMeta.textContent = `${payload.symbols || 0} symbols • ${payload.alerts || 0} alerts • ${payload.updated_at || ""}`;
  if (!/^replay day/i.test(text)) {
    replayDayPending = false;
  }
  syncReplayButton(text);
}

function syncReplayButton(statusText) {
  if (!replayDayBtn) return;
  replayDayBtn.textContent = "Replay Day";
  replayDayBtn.disabled = replayDayPending || /^replay day/i.test(statusText || "");
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

function currentTimezone() {
  return configState?.timezone || "America/New_York";
}

function timeParts(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return {
      minuteKey: String(value || ""),
      minuteLabel: String(value || ""),
      display: String(value || ""),
      chartTime: "0000",
    };
  }

  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: currentTimezone(),
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).formatToParts(date).reduce((acc, part) => {
    if (part.type !== "literal") {
      acc[part.type] = part.value;
    }
    return acc;
  }, {});

  const display = new Intl.DateTimeFormat("en-US", {
    timeZone: currentTimezone(),
    hour: "numeric",
    minute: "2-digit",
    second: "2-digit",
    hour12: true,
  }).format(date);

  return {
    minuteKey: `${parts.year}-${parts.month}-${parts.day} ${parts.hour}:${parts.minute}`,
    minuteLabel: `${parts.hour}:${parts.minute}`,
    display: `${display} ET`,
    chartTime: `${parts.hour}${parts.minute}`,
  };
}

function formatVolume(value) {
  const n = Number(value || 0);
  if (!Number.isFinite(n)) return "0";
  return new Intl.NumberFormat("en-US", { maximumFractionDigits: 0 }).format(Math.round(n));
}

function chartURL(alert) {
  const base = configState?.ui?.chart_opener_base_url || "http://localhost:8081";
  const signalTime = timeParts(alert.alert_time).chartTime;
  return `${base}/api/open-chart/${encodeURIComponent(alert.symbol)}/${encodeURIComponent(alert.session_date)}/${signalTime}?signal=buy`;
}

function minuteTimestamp(value) {
  const ts = new Date(value).getTime();
  if (Number.isNaN(ts)) return Number.NEGATIVE_INFINITY;
  return Math.floor(ts / 60000);
}

function filterAlerts() {
  const q = searchInput.value.trim().toUpperCase();
  if (!q) return alerts.slice();
  return alerts.filter((alert) => {
    return (alert.symbol || "").includes(q) || (alert.name || "").toUpperCase().includes(q);
  });
}

function compareAlertsWithinMinute(a, b) {
  const scoreDiff = (b.flush_score || 0) - (a.flush_score || 0);
  if (scoreDiff !== 0) return scoreDiff;
  return new Date(b.alert_time) - new Date(a.alert_time);
}

function buildMinuteGroups(rows) {
  const groupsByKey = new Map();

  rows.forEach((alert) => {
    const parts = timeParts(alert.alert_time);
    let group = groupsByKey.get(parts.minuteKey);
    if (!group) {
      group = {
        key: parts.minuteKey,
        minuteLabel: parts.minuteLabel,
        minuteTS: minuteTimestamp(alert.alert_time),
        alerts: [],
      };
      groupsByKey.set(parts.minuteKey, group);
    }
    group.alerts.push(alert);
  });

  const groups = Array.from(groupsByKey.values());
  groups.forEach((group) => {
    group.alerts.sort(compareAlertsWithinMinute);
    group.topScore = group.alerts[0]?.flush_score || 0;
  });

  if (sortSelect.value === "score") {
    groups.sort((a, b) => (b.topScore - a.topScore) || (b.minuteTS - a.minuteTS));
  } else {
    groups.sort((a, b) => b.minuteTS - a.minuteTS);
  }

  return groups;
}

function render() {
  const filtered = filterAlerts();
  const pinned = filtered
    .filter((alert) => pinSet.has(alert.id))
    .sort(compareAlertsWithinMinute);

  renderPinnedList(pinnedList, pinned);
  renderFeed(liveList, filtered);

  pinnedCount.textContent = String(pinned.length);
  liveCount.textContent = String(filtered.length);
}

function renderPinnedList(root, items) {
  root.innerHTML = "";
  if (!items.length) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "Pin any alert to keep it at the top.";
    root.appendChild(empty);
    return;
  }
  items.forEach((alert) => root.appendChild(renderCard(alert)));
}

function renderFeed(root, rows) {
  root.innerHTML = "";
  if (!rows.length) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "No alerts match the current filter.";
    root.appendChild(empty);
    return;
  }

  const groups = buildMinuteGroups(rows);
  groups.forEach((group) => {
    const hiddenCount = Math.max(group.alerts.length - 2, 0);
    const expanded = expandedMinutes.has(group.key);
    const visibleAlerts = expanded ? group.alerts : group.alerts.slice(0, 2);

    const section = document.createElement("section");
    section.className = "minute-group";

    const header = document.createElement("div");
    header.className = "minute-group-row";
    header.innerHTML = `
      <div class="minute-group-meta">
        <span class="minute-label">${escapeHTML(group.minuteLabel)}</span>
        <span class="minute-summary">${group.alerts.length} alerts this minute${hiddenCount ? ` · ${hiddenCount} hidden` : ""}</span>
      </div>
    `;

    if (hiddenCount > 0) {
      const button = document.createElement("button");
      button.className = "minute-toggle";
      button.type = "button";
      button.textContent = expanded ? "Hide extras" : `Show ${hiddenCount} more`;
      button.addEventListener("click", () => {
        if (expandedMinutes.has(group.key)) {
          expandedMinutes.delete(group.key);
        } else {
          expandedMinutes.add(group.key);
        }
        render();
      });
      header.appendChild(button);
    }

    const cardGrid = document.createElement("div");
    cardGrid.className = "minute-alert-grid";
    visibleAlerts.forEach((alert) => cardGrid.appendChild(renderCard(alert)));

    section.appendChild(header);
    section.appendChild(cardGrid);
    root.appendChild(section);
  });
}

function renderCard(alert) {
  const score = Number(alert.flush_score || 0);
  const scoreClass = scoreClassName(score);
  const parts = timeParts(alert.alert_time);
  const card = document.createElement("article");
  card.className = `detail-alert-card ${String(alert.tier || "candidate").toLowerCase()}`;
  card.innerHTML = `
    <div class="detail-alert-header">
      <div>
        <div class="detail-symbol-row">
          <h3 class="detail-symbol">${escapeHTML(alert.symbol)}</h3>
          <span class="tier-pill">${escapeHTML(alert.tier || "Candidate")}</span>
        </div>
        <p class="detail-meta">${escapeHTML(parts.display)} · $${Number(alert.price || 0).toFixed(2)}</p>
        <div class="source-line">${escapeHTML((alert.sources || []).join(", ") || "single source")}</div>
      </div>
      <div class="detail-score">
        <span class="detail-score-label">Flush Score</span>
        <strong class="detail-score-value ${scoreClass}">${score.toFixed(1)}</strong>
      </div>
    </div>
    <div class="detail-metrics">
      ${metric("30m Drop", `${Number(alert.metrics?.drop_from_prior_30m_high_pct || 0).toFixed(1)}%`)}
      ${metric("Below VWAP", `${Number(alert.metrics?.distance_below_vwap_pct || 0).toFixed(1)}%`)}
      ${metric("5m ROC", `-${Number(alert.metrics?.roc_5m_pct || 0).toFixed(1)}%`)}
      ${metric("10m ROC", `-${Number(alert.metrics?.roc_10m_pct || 0).toFixed(1)}%`)}
      ${metric("20m Slope", `${Number(alert.metrics?.down_slope_20m_pct_per_bar || 0).toFixed(1)}% / bar`)}
      ${metric("Range Exp", `x${Number(alert.metrics?.range_expansion || 0).toFixed(1)}`)}
      ${metric("Vol Exp", `x${Number(alert.metrics?.volume_expansion || 0).toFixed(1)}`)}
      ${metric("4am Vol", formatVolume(alert.volume_since_4am))}
    </div>
    <div class="detail-summary">${escapeHTML(alert.summary || "")}</div>
    <div class="card-actions">
      <button class="card-link chart-btn" type="button">Open Chart</button>
      <a class="card-link" href="/news.html?ticker=${encodeURIComponent(alert.symbol)}&date=${encodeURIComponent(alert.session_date)}&days=2" target="_blank" rel="noreferrer">Open Extras</a>
      <button class="pin-btn" type="button">${pinSet.has(alert.id) ? "Unpin" : "Pin"}</button>
    </div>
  `;

  card.querySelector(".chart-btn").addEventListener("click", () => {
    window.open(chartURL(alert), "_blank", "noopener,noreferrer");
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

  return card;
}

function metric(label, value) {
  return `<div class="detail-metric"><span class="detail-metric-label">${label}</span><span class="detail-metric-value">${value}</span></div>`;
}

function scoreClassName(score) {
  if (score < 40) return "score-0";
  if (score < 60) return "score-1";
  if (score < 75) return "score-2";
  if (score < 90) return "score-3";
  return "score-4";
}

function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll("\"", "&quot;")
    .replaceAll("'", "&#39;");
}

function landingTargetTop() {
  if (!alertColumns || !appViewport) return 0;
  return Math.max(
    0,
    Math.round(
      alertColumns.getBoundingClientRect().top
        - appViewport.getBoundingClientRect().top
        + appViewport.scrollTop
    )
  );
}

function ensureLandingScrollSpace() {
  if (!appViewport || !pageShell) return;
  pageShell.style.removeProperty("--landing-scroll-space");
}

function positionDefaultViewport() {
  if (initialViewportPositioned || !alertColumns || !appViewport) return;

  ensureLandingScrollSpace();
  const targetTop = landingTargetTop();
  appViewport.scrollTop = targetTop;

  if (Math.abs(appViewport.scrollTop - targetTop) <= 2) {
    initialViewportPositioned = true;
    return;
  }

  viewportPositionAttempts += 1;
  if (viewportPositionAttempts >= 8) {
    initialViewportPositioned = true;
    return;
  }

  requestAnimationFrame(positionDefaultViewport);
}

function scheduleDefaultViewportPosition() {
  requestAnimationFrame(positionDefaultViewport);
  window.setTimeout(positionDefaultViewport, 50);
  window.setTimeout(positionDefaultViewport, 200);
  window.setTimeout(positionDefaultViewport, 500);
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
  scheduleDefaultViewportPosition();
  connectWS();
}

searchInput.addEventListener("input", render);
sortSelect.addEventListener("change", render);
window.addEventListener("resize", () => {
  initialViewportPositioned = false;
  viewportPositionAttempts = 0;
  scheduleDefaultViewportPosition();
});

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

replayDayBtn?.addEventListener("click", async () => {
  replayDayPending = true;
  alerts = [];
  expandedMinutes = new Set();
  render();
  setStatus("Replay Day", "Clearing current alerts and requesting day replay");

  try {
    const res = await fetch("/api/replay-day", { method: "POST" });
    if (!res.ok) {
      const payload = await res.json().catch(() => ({}));
      throw new Error(payload.error || "Replay day request failed");
    }
  } catch (err) {
    replayDayPending = false;
    setStatus("Error", err.message);
    syncReplayButton("");
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
  render();
});

watchlistToggle.addEventListener("click", () => {
  watchlistExpanded = !watchlistExpanded;
  localStorage.setItem("flush-detector.watchlist-expanded", String(watchlistExpanded));
  updateWatchlistUI();
});

document.addEventListener("pointerdown", requestAudioPrime, { capture: true });
document.addEventListener("keydown", requestAudioPrime, { capture: true });
window.addEventListener("load", positionDefaultViewport);

bootstrap().catch((err) => {
  setStatus("Error", err.message);
});

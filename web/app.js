const statusPill = document.getElementById("statusPill");
const statusMeta = document.getElementById("statusMeta");
const searchInput = document.getElementById("searchInput");
const sortSelect = document.getElementById("sortSelect");
const soundBtn = document.getElementById("soundBtn");
const soundStateBadge = document.getElementById("soundStateBadge");
const soundStateText = document.getElementById("soundStateText");
const replayDayBtn = document.getElementById("replayDayBtn");
const returnLiveBtn = document.getElementById("returnLiveBtn");
const replayPrevMonthBtn = document.getElementById("replayPrevMonthBtn");
const replayNextMonthBtn = document.getElementById("replayNextMonthBtn");
const replayMonthLabel = document.getElementById("replayMonthLabel");
const replaySummary = document.getElementById("replaySummary");
const replayCalendar = document.getElementById("replayCalendar");
const reloadBtn = document.getElementById("reloadBtn");
const generateDashboardBtn = document.getElementById("generateDashboardBtn");
const applyBtn = document.getElementById("applyBtn");
const gapperPercent = document.getElementById("gapperPercent");
const gapperMode = document.getElementById("gapperMode");
const gapperSummary = document.getElementById("gapperSummary");
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
let replayResumePending = false;
let dashboardPending = false;
let replayMonth = calendarMonthKey(new Date());
let selectedReplayDate = "";
let statusState = { mode: "live", replaying: false, replay_date: "" };
let watchlistItems = [];
let gapperState = { enabled: false, pending: false, results: [], count: 0, gap_percent: 4 };
let gapperSortState = { key: "gap", direction: "desc" };
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
      return;
    }
    if (msg.type === "gappers") {
      applyGappers(msg.payload);
    }
  };
}

function setStatus(label, meta) {
  statusPill.textContent = label;
  statusMeta.textContent = meta || "";
  syncReplayControls();
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
  statusState = {
    mode: payload.mode || "live",
    replaying: !!payload.replaying,
    replay_date: payload.replay_date || "",
  };
  const text = payload.text || "Live";
  statusPill.textContent = text;
  const meta = [];
  if (statusState.mode === "historical" && statusState.replay_date) {
    meta.push(`Historical ${statusState.replay_date}`);
  }
  meta.push(`${payload.symbols || 0} symbols`);
  meta.push(`${payload.alerts || 0} alerts`);
  if (statusState.mode === "historical") {
    meta.push("live alerts paused");
  }
  if (payload.updated_at) {
    meta.push(payload.updated_at);
  }
  statusMeta.textContent = meta.join(" • ");
  if (!statusState.replaying) {
    replayDayPending = false;
    replayResumePending = false;
  }
  if (statusState.replay_date && statusState.replay_date.slice(0, 7) !== replayMonth) {
    setReplayMonth(statusState.replay_date.slice(0, 7), statusState.replay_date);
  } else {
    if (statusState.mode === "historical" && statusState.replay_date) {
      selectedReplayDate = statusState.replay_date;
    }
    syncReplayControls();
    renderReplayCalendar();
  }
}

function syncReplayControls() {
  if (!replayDayBtn || !returnLiveBtn) return;
  const selectable = replaySelectableSet();
  const hasSelection = selectedReplayDate && selectable.has(selectedReplayDate);
  replayDayBtn.textContent = statusState.replaying && statusState.mode === "historical" ? "Replaying..." : "Replay Day";
  replayDayBtn.disabled = replayDayPending || replayResumePending || statusState.replaying || !hasSelection;
  returnLiveBtn.hidden = statusState.mode !== "historical";
  returnLiveBtn.disabled = replayDayPending || replayResumePending || statusState.replaying;
  replayPrevMonthBtn.disabled = false;
  replayNextMonthBtn.disabled = replayCurrentMonthIsToday();
  if (generateDashboardBtn) {
    generateDashboardBtn.textContent = dashboardPending ? "Generating..." : "Generate Dashboard";
    generateDashboardBtn.disabled = dashboardPending || statusState.replaying;
  }
  updateReplaySummary();
}

function hydrateControls() {
  if (!configState) return;
  minAlertScore.value = configState.flush.min_alert_score;
  startTime.value = configState.flush.start_time;
  endTime.value = configState.flush.end_time;
  minBars.value = configState.flush.min_bars_before_alerts;
  requireVWAP.checked = !!configState.flush.require_below_vwap;
  requireDrop.checked = !!configState.flush.require_drop_from_recent_high;
  if (configState.gapper) {
    gapperPercent.value = configState.gapper.gap_percent ?? 4;
    gapperMode.checked = !!configState.gapper.enabled;
  }
}

function updateReplaySummary() {
  if (!replaySummary) return;

  if (statusState.mode === "historical" && statusState.replay_date) {
    replaySummary.textContent = `Historical view is locked to ${statusState.replay_date}. Replay wrote log/alerts_${statusState.replay_date.replaceAll("-", "")}.csv for that date. Return Live to resume real-time alerts.`;
    return;
  }

  if (selectedReplayDate) {
    replaySummary.textContent = `Selected ${selectedReplayDate}. Replay rebuilds alerts with the current live settings and overwrites log/alerts_${selectedReplayDate.replaceAll("-", "")}.csv for that date.`;
    return;
  }

  replaySummary.textContent = "Pick a highlighted session date to rebuild alerts and overwrite that day's alert log.";
}

function renderReplayCalendar() {
  if (!replayCalendar) return;
  const selectable = replaySelectableSet();
  const { offset, daysInMonth } = monthGridStart(replayMonth);
  const weekdayLabels = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

  if (!selectable.size) {
    replayCalendar.innerHTML = '<div class="replay-empty">No market days are selectable for this month.</div>';
    syncReplayControls();
    return;
  }

  replayCalendar.innerHTML = "";
  const grid = document.createElement("div");
  grid.className = "replay-calendar-grid";

  weekdayLabels.forEach((label) => {
    const cell = document.createElement("div");
    cell.className = "replay-weekday";
    cell.textContent = label;
    grid.appendChild(cell);
  });

  for (let i = 0; i < offset; i += 1) {
    const filler = document.createElement("div");
    filler.className = "replay-day empty";
    filler.setAttribute("aria-hidden", "true");
    grid.appendChild(filler);
  }

  for (let day = 1; day <= daysInMonth; day += 1) {
    const date = `${replayMonth}-${String(day).padStart(2, "0")}`;
    const isSelectable = selectable.has(date);
    const isSelected = date === selectedReplayDate;
    const isActive = statusState.mode === "historical" && statusState.replay_date === date;
    const button = document.createElement("button");
    button.type = "button";
    button.className = `replay-day${isSelectable ? " enabled" : ""}${isSelected ? " selected" : ""}${isActive ? " active" : ""}`;
    button.disabled = !isSelectable;
    button.innerHTML = `
      <div class="replay-day-top">
        <span class="replay-day-number">${day}</span>
        ${isActive ? '<span class="replay-day-flag">Active</span>' : isSelectable ? '<span class="replay-day-flag">Open</span>' : ""}
      </div>
      <div class="replay-day-meta">${isSelectable ? "Market day" : "Closed"}</div>
    `;
    if (isSelectable) {
      button.addEventListener("click", () => {
        selectedReplayDate = date;
        renderReplayCalendar();
        syncReplayControls();
      });
    }
    grid.appendChild(button);
  }

  replayCalendar.appendChild(grid);
  syncReplayControls();
}

function renderWatchlist(payload) {
  watchlistItems = Array.isArray(payload) ? payload : payload?.watchlist || [];
  renderWatchlistTags();
}

function applyGappers(payload) {
  gapperState = payload || { enabled: false, pending: false, results: [], count: 0, gap_percent: 4 };
  renderWatchlistTags();
}

function renderWatchlistTags() {
  const list = watchlistItems;
  const sourceSet = new Set();
  watchlistTags.innerHTML = "";
  list.forEach((item) => (item.sources || []).forEach((source) => sourceSet.add(source)));
  watchlistTags.classList.toggle("gapper-table-wrap", !!gapperState.enabled);

  if (gapperState.enabled) {
    renderGapperTags();
    updateGapperSummary(list.length);
    return;
  }

  list.forEach((item) => {
    const tag = document.createElement("span");
    tag.className = "tag";
    const sources = item.sources?.length ? ` · ${item.sources.join(", ")}` : "";
    tag.textContent = `${item.symbol}${sources}`;
    watchlistTags.appendChild(tag);
  });
  const sourceCount = sourceSet.size;
  watchlistSummary.textContent = sourceCount > 0
    ? `${list.length} symbols from ${sourceCount} watchlist${sourceCount === 1 ? "" : "s"}`
    : `${list.length} symbols loaded`;
  if (gapperSummary) {
    gapperSummary.textContent = "Gapper-mode is off. Alerts, logs, and dashboards use the full watchlist.";
  }
}

function renderGapperTags() {
  const results = sortedGappers();
  if (gapperState.pending) {
    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = "Scanning gappers...";
    watchlistTags.appendChild(tag);
    return;
  }
  if (gapperState.failed) {
    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = "Gapper scan failed";
    watchlistTags.appendChild(tag);
    return;
  }
  if (!results.length) {
    const tag = document.createElement("span");
    tag.className = "tag";
    tag.textContent = `No gappers at ${formatGapThreshold(gapperState.gap_percent)}`;
    watchlistTags.appendChild(tag);
    return;
  }

  const table = document.createElement("div");
  table.className = "gapper-table";
  table.setAttribute("role", "table");
  table.setAttribute("aria-label", "Gapper watchlist");
  table.innerHTML = `
    <div class="gapper-row gapper-head" role="row">
      ${gapperHeaderCell("Ticker", "symbol")}
      ${gapperHeaderCell("Gap", "gap")}
      ${gapperHeaderCell("Price", "price")}
      ${gapperHeaderCell("4am Vol", "volume")}
      ${gapperHeaderCell("Time", "time")}
      <div role="columnheader">Chart</div>
    </div>
  `;
  table.querySelectorAll(".gapper-sort-btn").forEach((button) => {
    button.addEventListener("click", (event) => {
      event.stopPropagation();
      setGapperSort(button.dataset.sortKey);
    });
  });

  results.forEach((item) => {
    const row = document.createElement("button");
    row.className = `gapper-row gapper-data-row${Number(item.gap_percent || 0) < 0 ? " down" : ""}`;
    row.type = "button";
    row.setAttribute("role", "row");
    row.title = `${item.name || item.symbol} gap ${formatGap(item.gap_percent)}. Open chart.`;
    row.innerHTML = `
      <div class="gapper-symbol-cell" role="cell">
        <strong>${escapeHTML(item.symbol)}</strong>
        <span>${escapeHTML(item.name || "")}</span>
      </div>
      <div class="gapper-gap-cell" role="cell">${escapeHTML(formatGap(item.gap_percent))}</div>
      <div class="gapper-price-cell" role="cell">${escapeHTML(formatPrice(item.open))}</div>
      <div class="gapper-volume-cell" role="cell">${escapeHTML(formatVolume(item.volume_since_4am))}</div>
      <div role="cell">${escapeHTML(gapperTimeLabel(item))}</div>
      <div class="gapper-chart-cell" role="cell">Open</div>
    `;
    row.addEventListener("click", () => {
      window.open(gapperChartURL(item), "_blank", "noopener,noreferrer");
    });
    table.appendChild(row);
  });
  watchlistTags.appendChild(table);
}

function sortedGappers() {
  const results = Array.isArray(gapperState.results) ? gapperState.results.slice() : [];
  const dir = gapperSortState.direction === "asc" ? 1 : -1;
  results.sort((a, b) => {
    let diff = 0;
    if (gapperSortState.key === "symbol") {
      diff = String(a.symbol || "").localeCompare(String(b.symbol || ""));
    } else if (gapperSortState.key === "price") {
      diff = Number(a.open || 0) - Number(b.open || 0);
    } else if (gapperSortState.key === "volume") {
      diff = Number(a.volume_since_4am || 0) - Number(b.volume_since_4am || 0);
    } else if (gapperSortState.key === "time") {
      diff = new Date(a.open_at || 0).getTime() - new Date(b.open_at || 0).getTime();
    } else {
      diff = Number(a.gap_percent || 0) - Number(b.gap_percent || 0);
    }
    if (diff !== 0) return diff * dir;
    return String(a.symbol || "").localeCompare(String(b.symbol || ""));
  });
  return results;
}

function gapperHeaderCell(label, key) {
  const active = gapperSortState.key === key;
  const direction = active ? gapperSortState.direction : "";
  const marker = active ? (direction === "asc" ? " asc" : " desc") : "";
  const ariaSort = active ? (direction === "asc" ? "ascending" : "descending") : "none";
  return `<div role="columnheader" aria-sort="${ariaSort}"><button class="gapper-sort-btn" type="button" data-sort-key="${escapeHTML(key)}">${escapeHTML(label + marker)}</button></div>`;
}

function setGapperSort(key) {
  if (gapperSortState.key === key) {
    gapperSortState = {
      key,
      direction: gapperSortState.direction === "desc" ? "asc" : "desc",
    };
  } else {
    gapperSortState = {
      key,
      direction: key === "symbol" ? "asc" : "desc",
    };
  }
  renderWatchlistTags();
}

function updateGapperSummary(totalSymbols) {
  const threshold = formatGapThreshold(gapperState.gap_percent);
  const target = gapperState.target_date ? ` for ${gapperState.target_date}` : "";
  const updated = gapperState.updated_at ? ` · ${timeParts(gapperState.updated_at).display}` : "";
  if (gapperState.pending) {
    watchlistSummary.textContent = `Scanning ${totalSymbols} symbols for ${threshold} gappers${target}`;
    gapperSummary.textContent = `Scanning ${totalSymbols} symbols from prior regular close${target}.`;
    return;
  }
  if (gapperState.failed) {
    watchlistSummary.textContent = `Gapper scan failed${target}`;
    gapperSummary.textContent = gapperState.note || "Gapper scan failed.";
    return;
  }
  watchlistSummary.textContent = `${gapperState.count || 0} gappers at ${threshold}${target}`;
  const source = sortedGappers().some((item) => item.provisional) ? "Live premarket gaps are updating from the latest minute." : "Gaps use the 09:30 ET open when available.";
  gapperSummary.textContent = `${source}${updated}`;
}

function formatGap(value) {
  const n = Number(value || 0);
  const sign = n > 0 ? "+" : "";
  return `${sign}${n.toFixed(2)}%`;
}

function formatPrice(value) {
  const n = Number(value || 0);
  if (!Number.isFinite(n) || n <= 0) return "--";
  return `$${n.toFixed(2)}`;
}

function formatGapThreshold(value) {
  const n = Number(value ?? 0);
  const sign = n > 0 ? "+" : "";
  return `${sign}${n.toFixed(1)}%`;
}

function gapperTimeLabel(item) {
  if (!item.open_at) return "--";
  const parts = timeParts(item.open_at);
  return parts.minuteLabel || "--";
}

function chartOpenerBaseURL() {
  const configured = configState?.ui?.chart_opener_base_url || "http://localhost:8081";
  try {
    const base = new URL(configured, window.location.origin);
    const localChartHost = base.hostname === "localhost" || base.hostname === "127.0.0.1" || base.hostname === "[::1]";
    const localPageHost = window.location.hostname === "localhost" || window.location.hostname === "127.0.0.1" || window.location.hostname === "[::1]";
    if (localChartHost && (!localPageHost || window.location.port === "9000")) {
      return `${window.location.origin}/second`;
    }
    return base.href.replace(/\/$/, "");
  } catch {
    return String(configured).replace(/\/$/, "");
  }
}

function gapperChartURL(item) {
  const base = chartOpenerBaseURL();
  const date = item.target_date || todayDateKey();
  const parts = timeParts(item.open_at);
  const signalTime = parts.chartTime === "0000" ? "0930" : parts.chartTime;
  const signal = Number(item.gap_percent || 0) < 0 ? "sell" : "buy";
  return `${base}/api/open-chart/${encodeURIComponent(item.symbol)}/${encodeURIComponent(date)}/${signalTime}?signal=${signal}`;
}

function updateWatchlistUI() {
  watchlistSection.classList.toggle("collapsed", !watchlistExpanded);
  watchlistToggle.setAttribute("aria-expanded", String(watchlistExpanded));
}

function currentTimezone() {
  return configState?.timezone || "America/New_York";
}

function zonedParts(value) {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: currentTimezone(),
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).formatToParts(value).reduce((acc, part) => {
    if (part.type !== "literal") {
      acc[part.type] = part.value;
    }
    return acc;
  }, {});
}

function calendarMonthKey(value) {
  const parts = zonedParts(value);
  return `${parts.year}-${parts.month}`;
}

function calendarDateKey(value) {
  const parts = zonedParts(value);
  return `${parts.year}-${parts.month}-${parts.day}`;
}

function todayDateKey() {
  return calendarDateKey(new Date());
}

function monthLabel(monthKey) {
  const [year, month] = monthKey.split("-").map(Number);
  return new Date(year, month - 1, 1, 12, 0, 0).toLocaleDateString("en-US", {
    month: "long",
    year: "numeric",
  });
}

function dateFromKey(dateKey) {
  const [year, month, day] = dateKey.split("-").map(Number);
  return new Date(year, month - 1, day, 12, 0, 0);
}

function shiftMonth(monthKey, delta) {
  const [year, month] = monthKey.split("-").map(Number);
  const shifted = new Date(year, month - 1 + delta, 1, 12, 0, 0);
  const nextYear = shifted.getFullYear();
  const nextMonth = String(shifted.getMonth() + 1).padStart(2, "0");
  return `${nextYear}-${nextMonth}`;
}

function monthGridStart(monthKey) {
  const [year, month] = monthKey.split("-").map(Number);
  const first = new Date(year, month - 1, 1, 12, 0, 0);
  return {
    first,
    offset: first.getDay(),
    daysInMonth: new Date(year, month, 0).getDate(),
  };
}

function nthWeekdayOfMonth(year, monthIndex, weekday, nth) {
  const first = new Date(year, monthIndex, 1, 12, 0, 0);
  const offset = (weekday - first.getDay() + 7) % 7;
  return new Date(year, monthIndex, 1 + offset + (nth - 1) * 7, 12, 0, 0);
}

function lastWeekdayOfMonth(year, monthIndex, weekday) {
  const last = new Date(year, monthIndex + 1, 0, 12, 0, 0);
  const offset = (last.getDay() - weekday + 7) % 7;
  return new Date(year, monthIndex + 1, -offset, 12, 0, 0);
}

function observedFixedHoliday(year, monthIndex, day) {
  const actual = new Date(year, monthIndex, day, 12, 0, 0);
  const observed = new Date(actual);
  if (actual.getDay() === 6) {
    observed.setDate(actual.getDate() - 1);
  } else if (actual.getDay() === 0) {
    observed.setDate(actual.getDate() + 1);
  }
  return calendarDateKey(observed);
}

function easterSunday(year) {
  const a = year % 19;
  const b = Math.floor(year / 100);
  const c = year % 100;
  const d = Math.floor(b / 4);
  const e = b % 4;
  const f = Math.floor((b + 8) / 25);
  const g = Math.floor((b - f + 1) / 3);
  const h = (19 * a + b - d - g + 15) % 30;
  const i = Math.floor(c / 4);
  const k = c % 4;
  const l = (32 + 2 * e + 2 * i - h - k) % 7;
  const m = Math.floor((a + 11 * h + 22 * l) / 451);
  const month = Math.floor((h + l - 7 * m + 114) / 31);
  const day = ((h + l - 7 * m + 114) % 31) + 1;
  return new Date(year, month - 1, day, 12, 0, 0);
}

const marketHolidayCache = new Map();

function marketHolidaySetForYear(year) {
  if (marketHolidayCache.has(year)) {
    return marketHolidayCache.get(year);
  }

  const holidays = new Set();
  holidays.add(observedFixedHoliday(year, 0, 1));
  holidays.add(calendarDateKey(nthWeekdayOfMonth(year, 0, 1, 3)));
  holidays.add(calendarDateKey(nthWeekdayOfMonth(year, 1, 1, 3)));

  const goodFriday = easterSunday(year);
  goodFriday.setDate(goodFriday.getDate() - 2);
  holidays.add(calendarDateKey(goodFriday));

  holidays.add(calendarDateKey(lastWeekdayOfMonth(year, 4, 1)));
  holidays.add(observedFixedHoliday(year, 5, 19));
  holidays.add(observedFixedHoliday(year, 6, 4));
  holidays.add(calendarDateKey(nthWeekdayOfMonth(year, 8, 1, 1)));
  holidays.add(calendarDateKey(nthWeekdayOfMonth(year, 10, 4, 4)));
  holidays.add(observedFixedHoliday(year, 11, 25));

  marketHolidayCache.set(year, holidays);
  return holidays;
}

function isMarketHoliday(dateKey) {
  const year = Number(dateKey.slice(0, 4));
  return [
    marketHolidaySetForYear(year - 1),
    marketHolidaySetForYear(year),
    marketHolidaySetForYear(year + 1),
  ].some((set) => set.has(dateKey));
}

function isMarketDay(dateKey) {
  if (dateKey > todayDateKey()) return false;
  const weekday = dateFromKey(dateKey).getDay();
  if (weekday === 0 || weekday === 6) return false;
  return !isMarketHoliday(dateKey);
}

function marketDaysForMonth(monthKey) {
  const { daysInMonth } = monthGridStart(monthKey);
  const out = [];
  for (let day = 1; day <= daysInMonth; day += 1) {
    const dateKey = `${monthKey}-${String(day).padStart(2, "0")}`;
    if (isMarketDay(dateKey)) {
      out.push(dateKey);
    }
  }
  return out;
}

function replaySelectableSet() {
  return new Set(marketDaysForMonth(replayMonth));
}

function defaultReplayDate(monthKey) {
  const dates = marketDaysForMonth(monthKey);
  return dates.at(-1) || "";
}

function setReplayMonth(monthKey, preferredDate = "") {
  replayMonth = monthKey;
  replayMonthLabel.textContent = monthLabel(monthKey);

  const selectable = replaySelectableSet();
  if (preferredDate && selectable.has(preferredDate)) {
    selectedReplayDate = preferredDate;
  } else if (selectedReplayDate && selectable.has(selectedReplayDate)) {
    // Keep the existing selection.
  } else {
    selectedReplayDate = defaultReplayDate(monthKey);
  }

  renderReplayCalendar();
  syncReplayControls();
}

function replayCurrentMonthIsToday() {
  return replayMonth >= calendarMonthKey(new Date());
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

  const parts = zonedParts(date);

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
  const base = chartOpenerBaseURL();
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
  const metrics = alert.metrics || {};
  const drop30m = Number(metrics.drop_from_prior_30m_high_pct || 0);
  const belowVwap = Number(metrics.distance_below_vwap_pct || 0);
  const roc5m = Number(metrics.roc_5m_pct || 0);
  const roc10m = Number(metrics.roc_10m_pct || 0);
  const slope20m = Number(metrics.down_slope_20m_pct_per_bar || 0);
  const rangeExpansion = Number(metrics.range_expansion || 0);
  const volumeExpansion = Number(metrics.volume_expansion || 0);
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
      <div class="detail-score-wrap">
        ${gapBadge(alert)}
        <div class="detail-score">
          <span class="detail-score-label">Flush Score</span>
          <strong class="detail-score-value ${scoreClass}">${score.toFixed(1)}</strong>
        </div>
      </div>
    </div>
    <div class="detail-metrics">
      ${scoredMetric("30m Drop", drop30m, `${drop30m.toFixed(1)}%`, METRIC_SCORE_SCALES.drop30m)}
      ${scoredMetric("Below VWAP", belowVwap, `${belowVwap.toFixed(1)}%`, METRIC_SCORE_SCALES.belowVwap)}
      ${scoredMetric("5m ROC", roc5m, `-${roc5m.toFixed(1)}%`, METRIC_SCORE_SCALES.roc5m)}
      ${scoredMetric("10m ROC", roc10m, `-${roc10m.toFixed(1)}%`, METRIC_SCORE_SCALES.roc10m)}
      ${scoredMetric("20m Slope", slope20m, `${slope20m.toFixed(2)}% / bar`, METRIC_SCORE_SCALES.slope20m)}
      ${scoredMetric("Range Exp", rangeExpansion, `x${rangeExpansion.toFixed(1)}`, METRIC_SCORE_SCALES.rangeExpansion)}
      ${scoredMetric("Vol Exp", volumeExpansion, `x${volumeExpansion.toFixed(1)}`, METRIC_SCORE_SCALES.volumeExpansion)}
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

const METRIC_SCORE_SCALES = {
  drop30m: {
    bounds: [1.0, 2.0, 3.0, 4.0],
    labels: ["noise", "pullback", "flush", "hard flush", "washout"],
  },
  belowVwap: {
    bounds: [0.5, 1.0, 2.0, 3.0],
    labels: ["near value", "weak", "stretched", "deeply stretched", "dislocated"],
  },
  roc5m: {
    bounds: [0.3, 0.7, 1.2, 1.8],
    labels: ["slow", "selling", "aggressive selling", "flush impulse", "panic burst"],
  },
  roc10m: {
    bounds: [0.5, 1.0, 2.0, 3.0],
    labels: ["drift", "sustained weakness", "strong pressure", "trend flush", "one-sided pressure"],
  },
  slope20m: {
    bounds: [0.03, 0.07, 0.12, 0.18],
    labels: ["drift", "controlled bleed", "trend pressure", "heavy pressure", "relentless trend"],
  },
  rangeExpansion: {
    bounds: [1.0, 1.3, 1.6, 2.0],
    labels: ["normal", "building", "expanding", "emotional", "washout-like"],
    options: { firstInclusive: true },
  },
  volumeExpansion: {
    bounds: [1.0, 1.5, 2.0, 3.0],
    labels: ["routine", "active", "crowded", "forced", "capitulation-like"],
    options: { firstInclusive: true },
  },
};

function bandScore(value, bounds, options = {}) {
  const numeric = Number.isFinite(value) ? value : 0;
  const firstInclusive = !!options.firstInclusive;
  const lastInclusive = options.lastInclusive !== false;
  if ((firstInclusive && numeric <= bounds[0]) || (!firstInclusive && numeric < bounds[0])) return 1;
  if (numeric < bounds[1]) return 2;
  if (numeric < bounds[2]) return 3;
  if ((lastInclusive && numeric <= bounds[3]) || (!lastInclusive && numeric < bounds[3])) return 4;
  return 5;
}

function scoredMetric(label, numericValue, displayValue, scale) {
  const score = bandScore(numericValue, scale.bounds, scale.options);
  const bandLabel = scale.labels[score - 1];
  return `
    <div class="detail-metric scored metric-score-${score}" title="${escapeHTML(`Score ${score}/5 · ${bandLabel}`)}">
      <div class="detail-metric-top">
        <span class="detail-metric-label">${escapeHTML(label)}</span>
        <span class="detail-metric-score" aria-label="${escapeHTML(`${label} score ${score} out of 5`)}">${score}</span>
      </div>
      <span class="detail-metric-value">${escapeHTML(displayValue)}</span>
      <span class="detail-metric-band">${escapeHTML(bandLabel)}</span>
    </div>
  `;
}

function metric(label, value) {
  return `<div class="detail-metric"><span class="detail-metric-label">${escapeHTML(label)}</span><span class="detail-metric-value">${escapeHTML(value)}</span></div>`;
}

function gapBadge(alert) {
  if (!alert.gap_percent) return "";
  return `
    <div class="detail-gap">
      <span class="detail-gap-label">Gap</span>
      <strong class="detail-gap-value">${escapeHTML(formatGap(alert.gap_percent))}</strong>
    </div>
  `;
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
  const [configRes, watchlistRes, gappersRes, historyRes] = await Promise.all([
    fetch("/api/config"),
    fetch("/api/watchlist"),
    fetch("/api/gappers"),
    fetch("/api/history"),
  ]);
  configState = await configRes.json();
  hydrateControls();
  renderWatchlist(await watchlistRes.json());
  applyGappers(await gappersRes.json());
  const historyPayload = await historyRes.json();
  alerts = historyPayload.alerts || [];
  setReplayMonth(replayMonth);
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

generateDashboardBtn?.addEventListener("click", async () => {
  let dashboardWindow = window.open("about:blank", "_blank");
  if (dashboardWindow) {
    dashboardWindow.document.title = "Generating Dashboard";
    dashboardWindow.document.body.textContent = "Generating dashboard...";
  }
  dashboardPending = true;
  syncReplayControls();

  const targetDate = statusState.mode === "historical" && statusState.replay_date
    ? statusState.replay_date
    : todayDateKey();
  setStatus("Generating Dashboard", `Building dashboard for ${targetDate}`);

  try {
    const res = await fetch("/api/dashboard/generate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ date: targetDate }),
    });
    const payload = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(payload.error || "Dashboard generation failed");
    }
    if (payload.dashboard_url) {
      if (dashboardWindow && !dashboardWindow.closed) {
        dashboardWindow.location.replace(payload.dashboard_url);
      } else if (!window.open(payload.dashboard_url, "_blank", "noopener,noreferrer")) {
        window.location.assign(payload.dashboard_url);
      }
      dashboardWindow = null;
    }
    setStatus("Dashboard Ready", `${payload.dashboard_file || "dashboard"} for ${payload.date || targetDate}`);
  } catch (err) {
    if (dashboardWindow && !dashboardWindow.closed) {
      dashboardWindow.close();
    }
    setStatus("Error", err.message);
  } finally {
    dashboardPending = false;
    syncReplayControls();
  }
});

replayDayBtn?.addEventListener("click", async () => {
  replayDayPending = true;
  alerts = [];
  expandedMinutes = new Set();
  render();
  setStatus("Historical Replay", `Requesting replay for ${selectedReplayDate}`);

  try {
    const res = await fetch("/api/replay-day", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ date: selectedReplayDate }),
    });
    if (!res.ok) {
      const payload = await res.json().catch(() => ({}));
      throw new Error(payload.error || "Replay day request failed");
    }
  } catch (err) {
    replayDayPending = false;
    setStatus("Error", err.message);
    syncReplayControls();
  }
});

returnLiveBtn?.addEventListener("click", async () => {
  replayResumePending = true;
  setStatus("Resuming Live", "Restoring live detector state");
  try {
    const res = await fetch("/api/replay-live", { method: "POST" });
    if (!res.ok) {
      const payload = await res.json().catch(() => ({}));
      throw new Error(payload.error || "Resume live request failed");
    }
  } catch (err) {
    replayResumePending = false;
    setStatus("Error", err.message);
    syncReplayControls();
  }
});

replayPrevMonthBtn?.addEventListener("click", () => {
  setReplayMonth(shiftMonth(replayMonth, -1));
});

replayNextMonthBtn?.addEventListener("click", () => {
  setReplayMonth(shiftMonth(replayMonth, 1));
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
    gapper: {
      ...(configState.gapper || { enabled: true, gap_percent: 4, lookback_days: 7 }),
      enabled: gapperMode.checked,
      gap_percent: Number(gapperPercent.value),
    },
  };
  const res = await fetch("/api/config/apply", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  configState = await res.json();
  hydrateControls();
  renderWatchlistTags();
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

const assetsHash = document.querySelector('meta[name="rigscope-assets-hash"]').content;
const dashboard = document.getElementById('dashboard');
const summaryGrid = document.getElementById('summaryGrid');
const socketState = document.getElementById('socketState');
const buildText = document.getElementById('buildText');
const rangeButtons = document.getElementById('rangeButtons');
const pauseButton = document.getElementById('pauseButton');
const updateBanner = document.getElementById('updateBanner');
const reloadButton = document.getElementById('reloadButton');
const collectorAlert = document.getElementById('collectorAlert');
const addGraphButton = document.getElementById('addGraphButton');
const graphDrawerButton = document.getElementById('graphDrawerButton');
const graphDrawer = document.getElementById('graphDrawer');
const graphDrawerClose = document.getElementById('graphDrawerClose');
const graphDrawerList = document.getElementById('graphDrawerList');
const graphLightbox = document.getElementById('graphLightbox');
const lightboxTitle = document.getElementById('lightboxTitle');
const lightboxSubtitle = document.getElementById('lightboxSubtitle');
const lightboxClose = document.getElementById('lightboxClose');
const lightboxCanvas = document.getElementById('lightboxCanvas');
const graphDialog = document.getElementById('graphDialog');
const graphForm = document.getElementById('graphForm');
const dialogTitle = document.getElementById('dialogTitle');
const dialogSubtitle = document.getElementById('dialogSubtitle');
const graphTitle = document.getElementById('graphTitle');
const graphYLabel = document.getElementById('graphYLabel');
const graphYScale = document.getElementById('graphYScale');
const graphYMin = document.getElementById('graphYMin');
const graphYMax = document.getElementById('graphYMax');
const graphStacked = document.getElementById('graphStacked');
const metricSearch = document.getElementById('metricSearch');
const unitFilter = document.getElementById('unitFilter');
const metricPicker = document.getElementById('metricPicker');
const seriesList = document.getElementById('seriesList');

const ranges = [
  ['1m', 60_000],
  ['5m', 300_000],
  ['15m', 900_000],
  ['30m', 1_800_000],
  ['1h', 3_600_000],
  ['3h', 10_800_000],
  ['6h', 21_600_000],
  ['24h', 86_400_000],
  ['3d', 259_200_000],
  ['5d', 432_000_000],
  ['1w', 604_800_000],
  ['2w', 1_209_600_000],
  ['4w', 2_419_200_000],
];

const gapThresholdMs = 10_000;
const maxHistoricalPointSpacingMs = 300_000;

let catalog = { metrics: [], defaults: [] };
let layout = { version: 1, time_range: '15m', order: [] };
let charts = new Map();
let graphData = new Map();
let graphRanges = new Map();
let graphHistoryLoads = new Map();
let lightboxChart = null;
let lightboxGraph = null;
let liveCounters = new Map();
let catalogRefreshTimer = null;
let catalogRefreshInFlight = false;
let paused = false;
let dirtyEditor = false;
let editingGraph = null;
let selectedSeries = [];
let selectedUnitFilter = '';
let reconnectDelay = 1000;
let lastPointTime = 0;

Chart.defaults.color = '#9aa8bb';
Chart.defaults.borderColor = 'rgba(148, 163, 184, 0.16)';
Chart.defaults.font.family = 'Inter, ui-sans-serif, system-ui, sans-serif';

const missingDataPlugin = {
  id: 'missingDataBackground',
  beforeDatasetsDraw(chart) {
    const xScale = chart.scales.x;
    const area = chart.chartArea;
    if (!xScale || !area) return;
    const gaps = missingDataSpans(chart);
    if (!gaps.length) return;
    const ctx = chart.ctx;
    ctx.save();
    ctx.beginPath();
    ctx.rect(area.left, area.top, area.right - area.left, area.bottom - area.top);
    ctx.clip();
    for (const gap of gaps) {
      const left = Math.max(area.left, xScale.getPixelForValue(gap.start));
      const right = Math.min(area.right, xScale.getPixelForValue(gap.end));
      if (!Number.isFinite(left) || !Number.isFinite(right) || right - left < 2) continue;
      drawMissingDataBand(ctx, left, area.top, right - left, area.bottom - area.top);
    }
    ctx.restore();
  },
};

Chart.register(missingDataPlugin);

function wsURL() {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${location.host}/ws`;
}

async function api(path, options = {}) {
  const res = await fetch(path, {
    headers: { 'content-type': 'application/json', ...(options.headers || {}) },
    ...options,
  });
  if (!res.ok) throw new Error(`${path}: ${res.status}`);
  return res.json();
}

function timeRangeMs() {
  const hit = ranges.find(([name]) => name === layout.time_range);
  return hit ? hit[1] : 900_000;
}

function setSocketState(text, cls) {
  socketState.textContent = text;
  socketState.className = `pill ${cls || ''}`;
}

function formatValue(value, symbol) {
  if (!Number.isFinite(value)) return 'n/a';
  if (isByteSymbol(symbol)) return formatByteValue(value, symbol);
  if (Math.abs(value) >= 100) return `${value.toFixed(0)} ${symbol || ''}`.trim();
  if (Math.abs(value) >= 10) return `${value.toFixed(1)} ${symbol || ''}`.trim();
  return `${value.toFixed(2)} ${symbol || ''}`.trim();
}

function formatBytes(value) {
  const units = ['B', 'kB', 'MB', 'GB', 'TB'];
  let n = Math.abs(value);
  let i = 0;
  while (n >= 1000 && i < units.length - 1) { n /= 1000; i++; }
  return `${Math.sign(value) < 0 ? '-' : ''}${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function formatByteValue(value, symbol) {
  if (symbol === 'MiB') return `${formatBytes(value * 1024 * 1024).replace(' ', '')}`;
  if (symbol === 'MiB/s') return `${formatBytes(value * 1024 * 1024).replace(' ', '')}/s`;
  if (symbol === 'B/s') return `${formatBytes(value).replace(' ', '')}/s`;
  return formatBytes(value).replace(' ', '');
}

function isByteSymbol(symbol) {
  return ['B', 'B/s', 'MiB', 'MiB/s'].includes(symbol);
}

function labelForMetric(metric) {
  const transform = metric.name.endsWith('_total') ? 'rate' : '';
  return metricLegend(metric, transform);
}

function metricLegend(metric, transform = '') {
  const summary = metricLabelSummary(metric);
  if (summary) {
    if (metric.name.startsWith('gpu_')) return summary;
    if (metric.name === 'cpu_package_power_w') return summary;
    if (metric.name === 'hwmon_power_w') return summary;
    if (metric.name === 'temperature_celsius' || metric.name.startsWith('gpu_temp')) return summary;
    if (metric.name.startsWith('disk_') || metric.name.startsWith('network_')) return `${compactMetricName(metric.name, transform)} ${summary}`;
    if (metric.name === 'filesystem_used_bytes' || metric.name === 'filesystem_free_bytes') return `${compactMetricName(metric.name, transform)} ${summary}`;
    return `${displayMetricName(metric, transform)} ${summary}`;
  }
  const labels = Object.entries(metric.labels || {})
    .filter(([key]) => key !== 'collector' && key !== 'index')
    .map(([, value]) => shortLabelValue(value))
    .filter(Boolean)
    .filter((value, index, values) => values.indexOf(value) === index)
    .join(' ');
  if (!labels) {
    const compact = compactMetricName(metric.name, transform);
    const display = displayMetricName(metric, transform);
    if (compact !== display) return compact;
  }
  return `${displayMetricName(metric, transform)}${labels ? ` ${labels}` : ''}`;
}

function metricLabelSummary(metric) {
  const labels = metric.labels || {};
  switch (labels.collector) {
    case 'zenpower':
      if (metric.name === 'cpu_package_power_w') return 'CPU package';
      break;
    case 'nvidia':
      return compactDeviceName(labels.device);
    case 'drm':
    case 'rocm':
      if (labels.driver === 'amdgpu' || labels.chip === 'amdgpu' || labels.collector === 'rocm') {
        return labels.sensor ? `AMD GPU ${labels.sensor}` : 'AMD GPU';
      }
      break;
    case 'thermal':
      if (labels.chip === 'nvme') return labels.sensor ? `NVMe ${labels.sensor}` : 'NVMe';
      if (labels.type) return shortLabelValue(labels.type);
      if (labels.chip && labels.sensor) return `${shortLabelValue(labels.chip)} ${shortLabelValue(labels.sensor)}`;
      break;
    case 'network':
      return labels.interface || '';
    case 'disk':
      return labels.device || '';
    case 'filesystem':
      return labels.mount || '';
    case 'xdna':
      return labels.vbnv || labels.driver || '';
  }
  if (metric.name === 'hwmon_power_w' && labels.chip) {
    return labels.sensor ? `${shortLabelValue(labels.chip)} ${shortLabelValue(labels.sensor)}` : shortLabelValue(labels.chip);
  }
  return '';
}

function compactDeviceName(value) {
  return shortLabelValue(value)
    .replace(/^NVIDIA\\s+/i, '')
    .replace(/^GeForce\\s+/i, '')
    .replace(/^RTX PRO\\s+/i, 'RTX ')
    .replace(/Blackwell Workstation Edition/gi, '')
    .replace(/Workstation Edition/gi, '')
    .replace(/Graphics \/ Radeon /gi, '/')
    .replace(/\\s+Graphics\\b/gi, '')
    .replace(/\\s+/g, ' ')
    .trim();
}

function shortLabelValue(value) {
  return String(value || '')
    .replace(/^NVIDIA\\s+/i, '')
    .replace(/Advanced Micro Devices, Inc\\.\\s*/gi, 'AMD ')
    .replace(/Graphics \/ Radeon /gi, '/')
    .replace(/\\s+Graphics\\b/gi, '')
    .replace(/\\s+/g, ' ')
    .trim();
}

function displayMetricName(metric, transform = '') {
  const name = transform === 'rate' ? metric.name.replace(/_total$/, '') : metric.name;
  return name.replaceAll('_', ' ');
}

function compactMetricName(name, transform = '') {
  const key = transform === 'rate' ? name.replace(/_total$/, '') : name;
  return {
    disk_read_bytes_per_second: 'Read',
    disk_written_bytes_per_second: 'Write',
    disk_reads_per_second: 'Reads',
    disk_writes_per_second: 'Writes',
    network_rx_bytes_per_second: 'RX',
    network_tx_bytes_per_second: 'TX',
    network_rx_packets_per_second: 'RX packets',
    network_tx_packets_per_second: 'TX packets',
    network_rx_errors_per_second: 'RX errors',
    network_tx_errors_per_second: 'TX errors',
    network_rx_drops_per_second: 'RX drops',
    network_tx_drops_per_second: 'TX drops',
    filesystem_used_bytes: 'Used',
    filesystem_free_bytes: 'Free',
    sockets_used: 'Sockets',
    tcp_sockets_in_use: 'TCP sockets',
    tcp_sockets_time_wait: 'TCP socket TW',
    udp_sockets_in_use: 'UDP sockets',
    tcp_connections_established: 'TCP established',
    tcp_connections_listen: 'TCP listen',
    tcp_connections_time_wait: 'TCP time-wait',
  }[key] || key.replaceAll('_', ' ');
}

function metricForTransform(metric, transform) {
  const next = JSON.parse(JSON.stringify(metric));
  if (transform !== 'rate') return next;
  if (next.unit === 'byte') {
    next.symbol = 'B/s';
  } else if (next.symbol) {
    next.symbol = `${next.symbol}/s`;
  } else {
    next.symbol = 'rate';
  }
  return next;
}

function allGraphs() {
  const defaults = catalog.defaults || [];
  const custom = layout.custom_graphs || [];
  const hidden = new Set(layout.hidden_default || []);
  const hiddenCustom = new Set(layout.hidden_custom || []);
  const explicitOrder = new Set(layout.order || []);
  for (const graph of custom) {
    if (graph.source_id && !explicitOrder.has(graph.source_id)) hidden.add(graph.source_id);
  }
  const byID = new Map();
  for (const graph of defaults) if (!hidden.has(graph.id)) byID.set(graph.id, graph);
  for (const graph of custom) if (!hiddenCustom.has(graph.id)) byID.set(graph.id, graph);
  const order = layout.order || [];
  const ordered = [];
  for (const id of order) {
    if (byID.has(id)) {
      ordered.push(byID.get(id));
      byID.delete(id);
    }
  }
  return [...ordered, ...byID.values()];
}

function unusedGraphs() {
  const visible = new Set(allGraphs().map(graph => graph.id));
  const defaults = (catalog.defaults || []).filter(graph => !visible.has(graph.id));
  const custom = (layout.custom_graphs || []).filter(graph => !visible.has(graph.id));
  return [...defaults, ...custom];
}

function renderRangeButtons() {
  rangeButtons.innerHTML = '';
  const label = document.createElement('span');
  label.textContent = 'Range';
  label.className = 'range-label';
  const select = document.createElement('select');
  select.id = 'rangeSelect';
  select.setAttribute('aria-label', 'Graph time range');
  for (const [name] of ranges) {
    const option = document.createElement('option');
    option.value = name;
    option.textContent = name;
    option.selected = layout.time_range === name;
    select.append(option);
  }
  select.onchange = async () => {
    layout.time_range = select.value;
    await saveLayout();
    await refreshAllGraphs();
  };
  rangeButtons.append(label, select);
}

function renderSummary(points = []) {
  const totalPower = totalKnownPower(points);
  const wanted = [
    ['Total Power', () => null, totalPower !== null ? formatValue(totalPower, 'W') : 'n/a'],
    ['GPU Load', p => p.name === 'gpu_util_pct'],
    ['NPU', p => p.name.startsWith('npu_')],
    ['Thermals', p => p.kind === 'temperature' || p.name.includes('temp')],
  ];
  summaryGrid.innerHTML = '';
  for (const [title, pick, value] of wanted) {
    const point = [...points].reverse().find(p => pick(p));
    const tile = document.createElement('div');
    tile.className = 'summary-tile';
    tile.innerHTML = `<span class="subtle">${title}</span><b>${value || (point ? formatValue(point.value, point.symbol || point.unit) : 'n/a')}</b>`;
    summaryGrid.append(tile);
  }
}

function renderCollectorAlert(errors = []) {
  const active = (errors || []).filter(item => item?.collector && item?.error);
  if (!active.length) {
    collectorAlert.hidden = true;
    collectorAlert.innerHTML = '';
    return;
  }
  const shown = active.slice(0, 4);
  const extra = active.length - shown.length;
  const details = shown
    .map(item => `<strong>${escapeHTML(item.collector)}</strong>: ${escapeHTML(item.error)}`)
    .join(' · ');
  collectorAlert.hidden = false;
  collectorAlert.innerHTML = `
    <span>${active.length === 1 ? 'Collector issue' : `${active.length} collector issues`}</span>
    <span class="collector-alert-details">${details}${extra > 0 ? ` · +${extra} more` : ''}</span>
  `;
}

function totalKnownPower(points) {
  let total = 0;
  let count = 0;
  for (const point of points) {
    if (!isUsagePowerPoint(point) || !Number.isFinite(point.value)) continue;
    total += point.value;
    count++;
  }
  return count ? total : null;
}

function isUsagePowerPoint(point) {
  const name = String(point.name || '').toLowerCase();
  const labels = point.labels || {};
  if (point.kind !== 'power' && point.unit !== 'watt' && point.symbol !== 'W') return false;
  if (name.includes('limit') || name.includes('cap')) return false;
  if (labels.collector === 'power_supply') return false;
  return true;
}

function renderDashboard() {
  dashboard.innerHTML = '';
  const graphs = allGraphs();
  if (graphs.length === 0) {
    dashboard.innerHTML = '<div class="empty">No graph presets match the metrics collected yet.</div>';
    return;
  }
  for (const graph of graphs) dashboard.append(graphCard(graph));
  Sortable.get(dashboard)?.destroy();
  Sortable.create(dashboard, {
    animation: 150,
    handle: '.drag-handle',
    group: { name: 'graphs', put: true },
    onAdd: event => {
      const id = event.item.dataset.graphId;
      event.item.remove();
      if (!id) return;
      restoreDefaultGraphAt(id, event.newIndex);
    },
    onEnd: () => {
      if ([...dashboard.children].some(child => !child.classList.contains('graph-card'))) return;
      layout.order = [...dashboard.querySelectorAll('.graph-card')].map(card => card.dataset.graphId);
      saveLayout();
    },
  });
  renderGraphDrawer();
}

function renderGraphDrawer() {
  const unused = unusedGraphs();
  graphDrawerButton.textContent = `Graphs${unused.length ? ` (${unused.length})` : ''}`;
  graphDrawerList.innerHTML = '';
  if (unused.length === 0) {
    graphDrawerList.innerHTML = '<div class="empty">No unused graphs.</div>';
    return;
  }
  for (const graph of unused) {
    const item = document.createElement('div');
    item.className = 'drawer-graph';
    item.dataset.graphId = graph.id;
    const subtitle = (graph.series || []).map(s => s.legend || s.metric.name).join(', ');
    const badge = graphBadge(graph);
    item.innerHTML = `
      <div class="drawer-graph-head">
        <div><strong>${escapeHTML(graph.title)}</strong><span class="graph-badge ${badge.className}">${escapeHTML(badge.label)}</span></div>
        ${graph.kind === 'custom' ? '<button type="button" class="trash-button" title="Delete custom graph" aria-label="Delete custom graph">🗑</button>' : ''}
      </div>
      <span>${escapeHTML(subtitle)}</span>
    `;
    const trash = item.querySelector('.trash-button');
    if (trash) {
      trash.onclick = event => {
        event.stopPropagation();
        deleteCustomGraph(graph.id);
      };
    }
    graphDrawerList.append(item);
  }
  Sortable.get(graphDrawerList)?.destroy();
  Sortable.create(graphDrawerList, {
    animation: 150,
    sort: false,
    group: { name: 'graphs', pull: 'clone', put: false },
  });
}

function restoreDefaultGraphAt(id, index) {
  layout.hidden_default = (layout.hidden_default || []).filter(hiddenID => hiddenID !== id);
  layout.hidden_custom = (layout.hidden_custom || []).filter(hiddenID => hiddenID !== id);
  const current = [...dashboard.querySelectorAll('.graph-card')]
    .map(card => card.dataset.graphId)
    .filter(existingID => existingID && existingID !== id);
  const boundedIndex = Math.max(0, Math.min(index, current.length));
  current.splice(boundedIndex, 0, id);
  layout.order = current;
  saveLayout().then(refreshAllGraphs);
}

function toggleGraphLegend(graph) {
  const next = graph.show_legend === false;
  if (graph.kind === 'builtin') {
    const custom = cloneGraphAsCustom(graph);
    custom.show_legend = next;
    layout.custom_graphs = (layout.custom_graphs || []).filter(item => item.id !== custom.id);
    layout.hidden_custom = (layout.hidden_custom || []).filter(id => id !== custom.id);
    layout.custom_graphs.push(custom);
    layout.hidden_default = [...new Set([...(layout.hidden_default || []), graph.id])];
    layout.order = (layout.order || allGraphs().map(item => item.id)).map(id => id === graph.id ? custom.id : id);
  } else {
    const custom = (layout.custom_graphs || []).find(item => item.id === graph.id);
    if (custom) custom.show_legend = next;
    graph.show_legend = next;
  }
  saveLayout().then(refreshAllGraphs);
}

function deleteCustomGraph(id) {
  layout.custom_graphs = (layout.custom_graphs || []).filter(graph => graph.id !== id);
  layout.hidden_custom = (layout.hidden_custom || []).filter(hiddenID => hiddenID !== id);
  layout.order = (layout.order || []).filter(existingID => existingID !== id);
  saveLayout().then(refreshAllGraphs);
}

function graphCard(graph) {
  const card = document.createElement('article');
  card.className = `graph-card ${graph.size || ''}`;
  card.dataset.graphId = graph.id;
  const subtitle = (graph.series || []).map(s => s.legend || s.metric.name).join(', ');
  const badge = graphBadge(graph);
  card.innerHTML = `
    <div class="graph-head">
      <div class="graph-title">
        <div class="graph-title-row"><h3>${escapeHTML(graph.title)}</h3><span class="graph-badge ${badge.className}">${escapeHTML(badge.label)}</span></div>
        <span>${escapeHTML(subtitle)}</span>
      </div>
      <div class="graph-tools">
        <button class="graph-icon-button drag-handle" title="Drag to reorder" aria-label="Drag to reorder">⠿</button>
        <button class="graph-icon-button ${graph.show_legend === false ? '' : 'active'}" data-action="legend" title="Toggle labels" aria-label="Toggle labels">Aa</button>
        <button class="graph-icon-button" data-action="reset" title="Reset zoom" aria-label="Reset zoom">↺</button>
        <button class="graph-icon-button" data-action="edit" title="Edit graph" aria-label="Edit graph">✎</button>
        <button class="graph-icon-button" data-action="hide" title="Hide graph" aria-label="Hide graph">−</button>
      </div>
    </div>
    <div class="canvas-wrap"><canvas></canvas></div>
  `;
  card.querySelector('[data-action="edit"]').onclick = () => openEditor(graph);
  card.querySelector('[data-action="hide"]').onclick = () => hideGraph(graph);
  card.querySelector('[data-action="reset"]').onclick = () => charts.get(graph.id)?.resetZoom?.();
  card.querySelector('[data-action="legend"]').onclick = () => toggleGraphLegend(graph);
  const canvas = card.querySelector('canvas');
  makeChart(graph, canvas);
  installGraphLightboxTrigger(canvas, graph);
  loadGraphHistory(graph);
  return card;
}

function graphBadge(graph) {
  if (graph.source_id) return { label: 'Customized', className: 'customized' };
  if (graph.kind === 'custom') return { label: 'Custom', className: 'custom' };
  return { label: 'Default', className: 'default' };
}

function installGraphLightboxTrigger(canvas, graph) {
  let start = null;
  let dragged = false;
  canvas.addEventListener('pointerdown', event => {
    if (event.button !== 0) return;
    start = { x: event.clientX, y: event.clientY };
    dragged = false;
  });
  canvas.addEventListener('pointermove', event => {
    if (!start) return;
    if (Math.hypot(event.clientX - start.x, event.clientY - start.y) > 5) dragged = true;
  });
  canvas.addEventListener('pointerup', () => {
    start = null;
  });
  canvas.addEventListener('pointercancel', () => {
    start = null;
    dragged = true;
  });
  canvas.addEventListener('click', event => {
    if (dragged) {
      event.preventDefault();
      event.stopPropagation();
      dragged = false;
      return;
    }
    if (!eventInChartArea(canvas, event)) return;
    openGraphLightbox(graph);
  });
}

function eventInChartArea(canvas, event) {
  const chart = Chart.getChart(canvas);
  if (!chart?.chartArea) return false;
  const rect = canvas.getBoundingClientRect();
  const x = event.clientX - rect.left;
  const y = event.clientY - rect.top;
  const area = chart.chartArea;
  return x >= area.left && x <= area.right && y >= area.top && y <= area.bottom;
}

function makeChart(graph, canvas, options = {}) {
  const stacked = !!graph.stacked && graph.axes?.y?.mode !== 'logarithmic';
  const showLegend = graph.show_legend !== false;
  const datasets = (graph.series || []).map((item, index) => ({
    label: item.legend || item.metric.name,
    data: [],
    borderColor: item.color,
    backgroundColor: `${item.color || '#38bdf8'}33`,
    borderWidth: 2,
    pointRadius: 0,
    tension: 0.25,
    fill: stacked,
    spanGaps: false,
    stack: stacked ? item.axis || 'y' : undefined,
    yAxisID: item.axis === 'y2' ? 'y2' : 'y',
  }));
  const y = graph.axes?.y || {};
  const y2 = graph.axes?.y2 || {};
  const chart = new Chart(canvas, {
    type: 'line',
    data: { datasets },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      normalized: true,
      parsing: false,
      interaction: { mode: 'nearest', intersect: false },
      plugins: {
        legend: { display: showLegend, labels: { boxWidth: 10, boxHeight: 10 } },
        tooltip: { callbacks: { label: ctx => `${ctx.dataset.label}: ${formatValue(ctx.parsed.y, axisSymbol(graph, ctx.dataset.yAxisID))}` } },
        zoom: {
          pan: { enabled: true, mode: 'x', onPanComplete: ({ chart }) => ensureGraphHistoryForViewport(graph, chart) },
          zoom: { wheel: { enabled: true, modifierKey: 'ctrl' }, pinch: { enabled: true }, mode: 'x' },
        },
      },
      scales: {
        x: { type: 'time', ticks: { maxRotation: 0 }, title: { display: true, text: graph.axes?.x?.label || 'Time' } },
        y: axisOptions(y, stacked),
        y2: { ...axisOptions(y2, stacked), display: !!y2.label, position: 'right', grid: { drawOnChartArea: false } },
      },
    },
  });
  if (options.store !== false) charts.set(graph.id, chart);
  return chart;
}

function axisOptions(axis, stacked = false) {
  const symbol = axis.symbol || axis.unit || '';
  const mode = axis.mode === 'logarithmic' ? 'logarithmic' : 'linear';
  const opts = {
    type: mode,
    beginAtZero: shouldBeginAtZero(axis),
    stacked,
    title: { display: !!axis.label, text: axis.symbol ? `${axis.label} (${axis.symbol})` : axis.label || '' },
    ticks: {
      callback: value => formatAxisTick(Number(value), symbol),
    },
  };
  if (Number.isFinite(axis.min) && (mode !== 'logarithmic' || axis.min > 0)) opts.min = axis.min;
  if (Number.isFinite(axis.max)) opts.max = axis.max;
  return opts;
}

function shouldBeginAtZero(axis) {
  if (axis.mode === 'logarithmic') return false;
  const unit = normalizeUnit(axis.unit || axis.symbol);
  if (unit === 'celsius' || unit === 'megahertz') return false;
  return !!axis.begin_zero;
}

function defaultBeginZeroForAxis(axis) {
  if (axis.mode === 'logarithmic') return false;
  const unit = normalizeUnit(axis.unit || axis.symbol);
  return ['watt', 'percent', 'byte', 'byte/second', 'count', 'ratio', 'second'].includes(unit);
}

function formatAxisTick(value, symbol) {
  if (!Number.isFinite(value)) return '';
  if (isByteSymbol(symbol)) return formatByteValue(value, symbol);
  if (Math.abs(value) >= 1000) return Intl.NumberFormat(undefined, { maximumFractionDigits: 1, notation: 'compact' }).format(value);
  if (Math.abs(value) >= 100) return value.toFixed(0);
  if (Math.abs(value) >= 10) return value.toFixed(1);
  return value.toFixed(2);
}

function axisSymbol(graph, axisID) {
  const axis = axisID === 'y2' ? graph.axes?.y2 : graph.axes?.y;
  return axis?.symbol || axis?.unit || '';
}

function openGraphLightbox(graph) {
  closeGraphLightbox();
  lightboxGraph = graph;
  lightboxTitle.textContent = graph.title || 'Graph';
  lightboxSubtitle.textContent = (graph.series || []).map(s => s.legend || s.metric.name).join(', ');
  lightboxChart = makeChart(graph, lightboxCanvas, { store: false });
  graphLightbox.showModal();
  requestAnimationFrame(() => requestAnimationFrame(() => {
    lightboxChart?.resize();
    if (lightboxChart) applyGraphDataToChart(graph, lightboxChart);
  }));
}

function closeGraphLightbox() {
  if (graphLightbox.open) {
    graphLightbox.close();
    return;
  }
  if (lightboxChart) {
    lightboxChart.destroy();
    lightboxChart = null;
  }
  lightboxGraph = null;
}

function missingDataSpans(chart) {
  const spans = [];
  for (const dataset of chart.data.datasets || []) {
    const data = dataset.data || [];
    for (let i = 0; i < data.length; i++) {
      if (data[i]?.y !== null || data[i]?.missing !== true) continue;
      const start = data[i].x;
      while (i + 1 < data.length && data[i + 1]?.y === null && data[i + 1]?.missing === true) i++;
      const end = data[i].x;
      if (Number.isFinite(start) && Number.isFinite(end) && end > start) {
        spans.push({ start, end });
      }
    }
  }
  return mergeSpans(spans);
}

function mergeSpans(spans) {
  if (spans.length < 2) return spans;
  spans.sort((a, b) => a.start - b.start || a.end - b.end);
  const merged = [spans[0]];
  for (const span of spans.slice(1)) {
    const prev = merged[merged.length - 1];
    if (span.start <= prev.end) {
      prev.end = Math.max(prev.end, span.end);
    } else {
      merged.push({ ...span });
    }
  }
  return merged;
}

function drawMissingDataBand(ctx, x, y, width, height) {
  ctx.fillStyle = 'rgba(148, 163, 184, 0.07)';
  ctx.fillRect(x, y, width, height);
  ctx.strokeStyle = 'rgba(148, 163, 184, 0.11)';
  ctx.lineWidth = 1;
  ctx.beginPath();
  for (let px = x - height; px < x + width + height; px += 12) {
    ctx.moveTo(px, y + height);
    ctx.lineTo(px + height, y);
  }
  ctx.stroke();
}

async function loadGraphHistory(graph) {
  const end = Date.now();
  const start = end - timeRangeMs();
  const res = await queryGraphRange(graph, start, end);
  graphData.set(graph.id, res.series || []);
  graphRanges.set(graph.id, { start: res.start || start, end: res.end || end });
  applyGraphData(graph);
}

async function queryGraphRange(graph, start, end) {
  start = Math.floor(start);
  end = Math.floor(end);
  const res = await api('/api/query/batch', {
    method: 'POST',
    body: JSON.stringify({ start, end, max_points: maxPointsForRange(start, end), series: graph.series || [] }),
  });
  return res;
}

function maxPointsForRange(start, end) {
  return Math.max(900, Math.ceil(Math.max(0, end - start) / maxHistoricalPointSpacingMs) + 1);
}

async function ensureGraphHistoryForViewport(graph, chart) {
  const x = chart.scales.x;
  if (!x || !Number.isFinite(x.min) || !Number.isFinite(x.max)) return;
  const range = graphRanges.get(graph.id);
  if (!range) return;
  const visibleSpan = Math.max(1, x.max - x.min);
  const margin = Math.min(visibleSpan * 0.25, timeRangeMs() * 0.5);
  const wantedStart = x.min - margin;
  if (wantedStart >= range.start) return;

  const key = `${graph.id}:${Math.floor(wantedStart)}:${Math.floor(range.start)}`;
  if (graphHistoryLoads.has(key)) return graphHistoryLoads.get(key);
  const load = (async () => {
    const res = await queryGraphRange(graph, wantedStart, range.start);
    mergeGraphData(graph, res.series || []);
    graphRanges.set(graph.id, { start: Math.min(res.start || wantedStart, range.start), end: range.end });
    applyGraphData(graph);
  })().finally(() => graphHistoryLoads.delete(key));
  graphHistoryLoads.set(key, load);
  return load;
}

function mergeGraphData(graph, olderSeries) {
  const current = graphData.get(graph.id) || [];
  const byID = new Map(current.map(item => [item.id, item]));
  for (const older of olderSeries) {
    const existing = byID.get(older.id);
    if (!existing) {
      byID.set(older.id, older);
      continue;
    }
    existing.points = mergePoints(older.points || [], existing.points || []);
  }
  graphData.set(graph.id, (graph.series || []).map(item => byID.get(item.id)).filter(Boolean));
}

function mergePoints(a, b) {
  const seen = new Set();
  const out = [];
  for (const point of [...a, ...b]) {
    const key = point[0];
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(point);
  }
  out.sort((left, right) => left[0] - right[0]);
  return out;
}

async function refreshAllGraphs() {
  charts.forEach(chart => chart.destroy());
  charts = new Map();
  renderDashboard();
}

function applyGraphData(graph) {
  const chart = charts.get(graph.id);
  if (chart) applyGraphDataToChart(graph, chart);
  if (lightboxGraph?.id === graph.id && lightboxChart) applyGraphDataToChart(graph, lightboxChart);
}

function applyGraphDataToChart(graph, chart) {
  const data = graphData.get(graph.id) || [];
  chart.data.datasets.forEach((dataset, index) => {
    const item = graph.series?.[index];
    dataset.data = pointsWithGaps(data[index]?.points || [], item, graph);
  });
  chart.update('none');
}

function pointsWithGaps(points, item, graph) {
  const out = [];
  let previousX = null;
  const threshold = gapThresholdForPoints(points);
  for (const [x, y] of points) {
    if (previousX !== null && x - previousX > threshold) {
      out.push({ x: previousX + 1, y: null, missing: true });
      out.push({ x: x - 1, y: null, missing: true });
    }
    out.push({ x, y: normalizeSeriesValue(y, item, graph) });
    previousX = x;
  }
  return out;
}

function gapThresholdForPoints(points) {
  if (!Array.isArray(points) || points.length < 3) return gapThresholdMs;
  const deltas = [];
  let previousX = null;
  for (const point of points) {
    const x = point?.[0];
    if (!Number.isFinite(x)) continue;
    if (previousX !== null && x > previousX) deltas.push(x - previousX);
    previousX = x;
  }
  if (!deltas.length) return gapThresholdMs;
  deltas.sort((a, b) => a - b);
  const median = deltas[Math.floor(deltas.length / 2)];
  return Math.max(gapThresholdMs, median * 3);
}

function normalizeSeriesValue(value, item, graph) {
  const axis = item?.axis === 'y2' ? graph.axes?.y2 : graph.axes?.y;
  if (axis?.mode === 'logarithmic' && value <= 0) return null;
  if ((axis?.symbol || axis?.unit) === 'B' && item?.metric?.symbol === 'MiB') return value * 1024 * 1024;
  if ((axis?.symbol || axis?.unit) === 'B/s' && item?.metric?.symbol === 'MiB/s') return value * 1024 * 1024;
  return value;
}

function applyLivePoints(points, timestamp = Date.now()) {
  renderSummary(points);
  const now = timestamp;
  const liveRates = new Map();
  for (const graph of allGraphs()) {
    const cardChart = charts.get(graph.id);
    const targets = [];
    if (cardChart) targets.push(cardChart);
    if (lightboxGraph?.id === graph.id && lightboxChart) targets.push(lightboxChart);
    if (!targets.length) continue;
    applyLivePointsToCharts(graph, targets, points, now, liveRates);
  }
}

function applyLivePointsToCharts(graph, targets, points, now, liveRates) {
  const updates = [];
  (graph.series || []).forEach((item, index) => {
    const point = points.find(p => p.name === item.metric.name && sameLabels(p.labels || {}, item.metric.labels || {}));
    if (!point) return;
    const transformed = liveSeriesValue(point, item, now, liveRates);
    if (!transformed.ok || paused) return;
    updates.push({ index, value: normalizeSeriesValue(transformed.value, item, graph) });
  });
  if (!updates.length) return;
  const loadedRange = graphRanges.get(graph.id);
  const cutoff = Math.min(now - timeRangeMs(), loadedRange?.start || now - timeRangeMs());
  let anyChanged = false;
  for (const chart of targets) {
    let changed = false;
    for (const update of updates) {
      const data = chart.data.datasets[update.index]?.data;
      if (!data) continue;
      const previous = lastFinitePoint(data);
      if (previous && now - previous.x > gapThresholdMs) {
        data.push({ x: previous.x + 1, y: null, missing: true });
        data.push({ x: now - 1, y: null, missing: true });
      }
      data.push({ x: now, y: update.value });
      while (data.length && data[0].x < cutoff) data.shift();
      changed = true;
    }
    if (changed) {
      anyChanged = true;
      chart.update('none');
    }
  }
  if (anyChanged) {
    graphRanges.set(graph.id, {
      start: loadedRange?.start || cutoff,
      end: Math.max(loadedRange?.end || now, now),
    });
  }
}

function lastFinitePoint(data) {
  for (let i = data.length - 1; i >= 0; i--) {
    if (data[i].y !== null && Number.isFinite(data[i].y)) return data[i];
  }
  return null;
}

function liveSeriesValue(point, item, timestamp, liveRates) {
  if (item.transform !== 'rate' && !point.name.endsWith('_total')) {
    return { ok: true, value: point.value };
  }
  const key = metricKey(point);
  if (liveRates.has(key)) return liveRates.get(key);
  const prev = liveCounters.get(key);
  liveCounters.set(key, { value: point.value, timestamp });
  if (!prev) {
    const result = { ok: false };
    liveRates.set(key, result);
    return result;
  }
  const dt = (timestamp - prev.timestamp) / 1000;
  if (dt <= 0 || point.value < prev.value) {
    const result = { ok: false };
    liveRates.set(key, result);
    return result;
  }
  const result = { ok: true, value: (point.value - prev.value) / dt };
  liveRates.set(key, result);
  return result;
}

function metricKey(point) {
  const labels = Object.entries(point.labels || {}).sort(([a], [b]) => a.localeCompare(b));
  return `${point.name}:${labels.map(([key, value]) => `${key}=${value}`).join(',')}`;
}

function sameLabels(a, b) {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  for (const key of keys) if ((a[key] || '') !== (b[key] || '')) return false;
  return true;
}

function catalogHasPoint(point) {
  const key = metricKey(point);
  return (catalog.metrics || []).some(metric => metricKey(metric) === key);
}

function scheduleCatalogRefreshFromPoints(points) {
  if (!points.some(point => point?.name && !catalogHasPoint(point))) return;
  if (catalogRefreshTimer) return;
  catalogRefreshTimer = setTimeout(refreshCatalogFromServer, 150);
}

async function refreshCatalogFromServer() {
  catalogRefreshTimer = null;
  if (catalogRefreshInFlight) {
    catalogRefreshTimer = setTimeout(refreshCatalogFromServer, 250);
    return;
  }
  catalogRefreshInFlight = true;
  const previousDefaults = new Map((catalog.defaults || []).map(graph => [graph.id, graph.series?.length || 0]));
  try {
    const next = await api('/api/catalog');
    catalog = next;
    const changed = (catalog.defaults || []).some(graph => previousDefaults.get(graph.id) !== (graph.series?.length || 0));
    if (changed) await refreshAllGraphs();
  } catch (err) {
    console.warn('catalog refresh failed', err);
  } finally {
    catalogRefreshInFlight = false;
  }
}

async function saveLayout() {
  await api('/api/graphs/layout', { method: 'PUT', body: JSON.stringify(layout) });
}

async function hideGraph(graph) {
  if (graph.kind === 'builtin') {
    layout.hidden_default = [...new Set([...(layout.hidden_default || []), graph.id])];
  } else {
    layout.hidden_custom = [...new Set([...(layout.hidden_custom || []), graph.id])];
  }
  layout.order = (layout.order || []).filter(id => id !== graph.id);
  await saveLayout();
  await refreshAllGraphs();
}

function openEditor(graph = null) {
  editingGraph = graph;
  const custom = graph ? cloneGraphAsCustom(graph) : newCustomGraph();
  selectedSeries = [...(custom.series || [])];
  graphTitle.value = custom.title || '';
  graphYLabel.value = custom.axes?.y?.label || '';
  graphYScale.value = custom.axes?.y?.mode === 'logarithmic' ? 'logarithmic' : 'linear';
  graphYMin.value = Number.isFinite(custom.axes?.y?.min) ? custom.axes.y.min : '';
  graphYMax.value = Number.isFinite(custom.axes?.y?.max) ? custom.axes.y.max : '';
  graphStacked.checked = graphYScale.value === 'logarithmic' ? false : !!custom.stacked;
  graphStacked.disabled = graphYScale.value === 'logarithmic';
  selectedUnitFilter = defaultUnitFilter(custom);
  dialogTitle.textContent = graph?.kind === 'builtin' ? 'Customize built-in graph' : graph ? 'Edit custom graph' : 'Add custom graph';
  dialogSubtitle.textContent = graph?.kind === 'builtin' ? 'Saving creates a custom graph based on this built-in preset.' : 'Choose metrics, labels, colors, and axis text.';
  dirtyEditor = false;
  renderUnitFilter();
  renderMetricPicker();
  renderSeriesList();
  graphDialog.showModal();
}

function newCustomGraph() {
  return {
    id: `custom-${crypto.randomUUID()}`,
    title: 'Custom graph',
    kind: 'custom',
    size: 'normal',
    stacked: false,
    show_legend: true,
    series: [],
    axes: { x: { label: 'Time', mode: 'time' }, y: { label: 'Value', mode: 'auto' } },
  };
}

function cloneGraphAsCustom(graph) {
  return JSON.parse(JSON.stringify({
    ...graph,
    id: graph.kind === 'custom' ? graph.id : `custom-${crypto.randomUUID()}`,
    kind: 'custom',
    source_id: graph.kind === 'builtin' ? graph.id : graph.source_id,
  }));
}

function renderMetricPicker() {
  const q = metricSearch.value.trim().toLowerCase();
  const selectedMetricKeys = new Set(selectedSeries.map(item => metricKey(item.metric)));
  metricPicker.innerHTML = '';
  const metrics = catalog.metrics
    .filter(metric => !selectedMetricKeys.has(metricKey(metric)))
    .filter(metric => !selectedUnitFilter || metricUnitKey(metric) === selectedUnitFilter)
    .filter(metric => !q || JSON.stringify(metric).toLowerCase().includes(q))
    .slice(0, 80);
  for (const metric of metrics) {
    const row = document.createElement('div');
    row.className = 'metric-choice';
    row.innerHTML = `<div><b>${escapeHTML(metric.name)}</b><small>${escapeHTML(labelForMetric(metric))} ${escapeHTML(unitLabel(metric))}</small></div><button type="button">Add</button>`;
    row.querySelector('button').onclick = () => {
      const transform = metric.name.endsWith('_total') ? 'rate' : '';
      selectedSeries.push({
        id: `series-${crypto.randomUUID()}`,
        metric: metricForTransform(metric, transform),
        legend: labelForMetric(metric),
        color: palette(selectedSeries.length),
        axis: 'y',
        transform,
      });
      dirtyEditor = true;
      renderUnitFilter();
      renderMetricPicker();
      renderSeriesList();
    };
    metricPicker.append(row);
  }
  if (metrics.length === 0) {
    const empty = document.createElement('div');
    empty.className = 'empty metric-empty';
    empty.textContent = 'No matching metrics';
    metricPicker.append(empty);
  }
}

function renderSeriesList() {
  seriesList.innerHTML = '';
  for (const item of selectedSeries) {
    const row = document.createElement('div');
    row.className = 'series-chip';
    row.innerHTML = `<div><b style="color:${item.color}">${escapeHTML(item.legend || item.metric.name)}</b><small>${escapeHTML(item.metric.name)}</small></div><button type="button">Remove</button>`;
    row.querySelector('button').onclick = () => {
      selectedSeries = selectedSeries.filter(s => s.id !== item.id);
      dirtyEditor = true;
      renderUnitFilter();
      renderMetricPicker();
      renderSeriesList();
    };
    seriesList.append(row);
  }
}

function renderUnitFilter() {
  const units = unitFilterOptions();
  unitFilter.innerHTML = '';
  for (const unit of units) {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = unit.key === selectedUnitFilter ? 'active' : '';
    button.textContent = unit.label;
    button.title = unit.key ? `Show ${unit.label} metrics` : 'Show all metrics';
    button.onclick = () => {
      selectedUnitFilter = unit.key;
      dirtyEditor = true;
      renderUnitFilter();
      renderMetricPicker();
    };
    unitFilter.append(button);
  }
}

function unitFilterOptions() {
  const seen = new Map([['', 'All']]);
  const add = metric => {
    const key = metricUnitKey(metric);
    if (!key || seen.has(key)) return;
    seen.set(key, unitLabel(metric));
  };
  for (const item of selectedSeries) add(item.metric);
  for (const metric of catalog.metrics) add(metric);
  return [...seen.entries()]
    .sort(([a], [b]) => {
      if (a === '') return -1;
      if (b === '') return 1;
      return unitPriority(a) - unitPriority(b) || seen.get(a).localeCompare(seen.get(b));
    })
    .slice(0, 13)
    .map(([key, label]) => ({ key, label }));
}

function defaultUnitFilter(graph) {
  const axis = graph?.axes?.y || {};
  const axisKey = normalizeUnit(axis.unit || axis.symbol);
  if (axisKey) return axisKey;
  const counts = new Map();
  for (const item of graph?.series || []) {
    const key = metricUnitKey(item.metric);
    if (!key) continue;
    counts.set(key, (counts.get(key) || 0) + 1);
  }
  return [...counts.entries()].sort((a, b) => b[1] - a[1])[0]?.[0] || '';
}

function metricUnitKey(metric) {
  return normalizeUnit(metric?.unit || metric?.symbol);
}

function normalizeUnit(raw) {
  const value = String(raw || '').trim();
  if (!value) return '';
  const lower = value.toLowerCase();
  if (['w', 'watt', 'watts'].includes(lower)) return 'watt';
  if (['%', 'percent', 'percentage'].includes(lower)) return 'percent';
  if (['b', 'byte', 'bytes', 'mib', 'mebibyte', 'gib', 'gibibyte'].includes(lower)) return 'byte';
  if (['b/s', 'byte/second', 'bytes/second', 'mib/s', 'mebibyte/second'].includes(lower)) return 'byte/second';
  if (['c', '°c', 'celsius'].includes(lower)) return 'celsius';
  if (['mhz', 'megahertz'].includes(lower)) return 'megahertz';
  if (['s', 'second', 'seconds'].includes(lower)) return 'second';
  if (['count', 'counts'].includes(lower)) return 'count';
  if (['ratio', '1'].includes(lower)) return 'ratio';
  return lower;
}

function unitLabel(metric) {
  const key = metricUnitKey(metric);
  const symbol = metric?.symbol || metric?.unit || '';
  if (!key) return '';
  if (key === 'byte') return 'B';
  if (key === 'byte/second') return 'B/s';
  if (key === 'watt') return 'W';
  if (key === 'percent') return '%';
  if (key === 'celsius') return '°C';
  if (key === 'megahertz') return 'MHz';
  return symbol || key;
}

function unitPriority(key) {
  return {
    watt: 1,
    percent: 2,
    celsius: 3,
    byte: 4,
    'byte/second': 5,
    megahertz: 6,
    second: 7,
    count: 8,
    ratio: 9,
  }[key] || 50;
}

function saveGraphFromDialog() {
  const base = editingGraph ? cloneGraphAsCustom(editingGraph) : newCustomGraph();
  base.title = graphTitle.value.trim() || 'Custom graph';
  base.stacked = graphYScale.value === 'logarithmic' ? false : graphStacked.checked;
  if (base.show_legend === undefined || base.show_legend === null) base.show_legend = true;
  base.series = selectedSeries;
  base.axes = base.axes || { x: { label: 'Time', mode: 'time' }, y: {} };
  base.axes.y = {
    ...(base.axes.y || {}),
    label: graphYLabel.value.trim() || 'Value',
    mode: graphYScale.value === 'logarithmic' ? 'logarithmic' : 'linear',
    min: parseNumber(graphYMin.value),
    max: parseNumber(graphYMax.value),
  };
  base.axes.y.begin_zero = defaultBeginZeroForAxis(base.axes.y);
  layout.custom_graphs = (layout.custom_graphs || []).filter(g => g.id !== base.id);
  layout.hidden_custom = (layout.hidden_custom || []).filter(id => id !== base.id);
  layout.custom_graphs.push(base);
  if (editingGraph?.kind === 'builtin') {
    layout.hidden_default = [...new Set([...(layout.hidden_default || []), editingGraph.id])];
    layout.order = (layout.order || allGraphs().map(g => g.id)).map(id => id === editingGraph.id ? base.id : id);
  } else if (!layout.order?.includes(base.id)) {
    layout.order = [...(layout.order || allGraphs().map(g => g.id)), base.id];
  }
  dirtyEditor = false;
  saveLayout().then(refreshAllGraphs);
}

function parseNumber(raw) {
  if (String(raw).trim() === '') return undefined;
  const n = Number(raw);
  return Number.isFinite(n) ? n : undefined;
}

function connectWS() {
  const ws = new WebSocket(wsURL());
  setSocketState('connecting', 'stale');
  ws.onopen = () => {
    reconnectDelay = 1000;
    setSocketState('live', 'live');
  };
  ws.onmessage = event => {
    const msg = JSON.parse(event.data);
    if (msg.type === 'hello') {
      const nextHash = msg.data?.assets_hash;
      if (nextHash && nextHash !== assetsHash) handleAssetChange();
    }
    if (msg.type === 'sample') {
      const points = msg.data?.points || [];
      renderCollectorAlert(msg.data?.collector_errors || []);
      if (points.length) lastPointTime = msg.time || Date.now();
      scheduleCatalogRefreshFromPoints(points);
      applyLivePoints(points, msg.time || Date.now());
    }
  };
  ws.onclose = () => {
    setSocketState('reconnecting', 'down');
    setTimeout(connectWS, reconnectDelay);
    reconnectDelay = Math.min(30_000, reconnectDelay * 1.8);
  };
  ws.onerror = () => ws.close();
}

function handleAssetChange() {
  if (dirtyEditor) {
    updateBanner.hidden = false;
  } else {
    location.reload();
  }
}

function palette(i) {
  return ['#38bdf8', '#22c55e', '#f59e0b', '#ef4444', '#a78bfa', '#14b8a6', '#f97316', '#e879f9'][i % 8];
}

function escapeHTML(value) {
  return String(value || '').replace(/[&<>"']/g, ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch]));
}

async function init() {
  const build = await api('/api/build');
  buildText.textContent = `${build.version} · ${build.assets_hash.slice(0, 12)} · pid ${build.pid}`;
  catalog = await api('/api/catalog');
  layout = await api('/api/graphs/layout');
  renderRangeButtons();
  renderSummary();
  renderDashboard();
  connectWS();
}

pauseButton.onclick = () => {
  paused = !paused;
  pauseButton.textContent = paused ? 'Resume' : 'Pause';
};
reloadButton.onclick = () => location.reload();
graphDrawerButton.onclick = () => {
  graphDrawer.hidden = !graphDrawer.hidden;
  if (!graphDrawer.hidden) renderGraphDrawer();
};
graphDrawerClose.onclick = () => { graphDrawer.hidden = true; };
lightboxClose.onclick = () => closeGraphLightbox();
graphLightbox.addEventListener('click', event => {
  if (event.target === graphLightbox) closeGraphLightbox();
});
graphLightbox.addEventListener('close', () => {
  if (lightboxChart) {
    lightboxChart.destroy();
    lightboxChart = null;
  }
  lightboxGraph = null;
});
addGraphButton.onclick = () => openEditor();
metricSearch.oninput = () => renderMetricPicker();
graphYScale.onchange = () => {
  const logarithmic = graphYScale.value === 'logarithmic';
  if (logarithmic) graphStacked.checked = false;
  graphStacked.disabled = logarithmic;
};
graphForm.addEventListener('input', () => { dirtyEditor = true; });
graphForm.addEventListener('submit', event => {
  if (event.submitter?.id !== 'saveGraphButton') return;
  event.preventDefault();
  saveGraphFromDialog();
  graphDialog.close();
});

init().catch(err => {
  dashboard.innerHTML = `<div class="empty">Failed to load dashboard: ${escapeHTML(err.message)}</div>`;
});

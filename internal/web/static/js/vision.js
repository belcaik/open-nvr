"use strict";

const geom = globalThis.VisionGeom;
const tile = document.querySelector(".camera .tile[data-cam]");
const channel = tile?.dataset.cam;
const overlay = document.querySelector("#vision-overlay");
const stateLine = document.querySelector("#vision-state");
const form = document.querySelector("#vision-config");
const csrf = JSON.parse(document.body.getAttribute("hx-headers") || "{}")["X-CSRF-Token"] || "";

let config = null;
let motor = null;
let frame = { tracks: [], target: 0 };
let lastFrameAt = 0;

const stateLabels = {
  at_preset: "en preset", tracking: "siguiendo", searching: "buscando", returning: "volviendo", paused: "pausado",
};
const detectorLabels = {
  ok: "operativo", starting: "iniciando", reconnecting: "reconectando", down: "no disponible",
};

async function request(url, options = {}) {
  const headers = new Headers(options.headers || {});
  if (options.method && options.method !== "GET") headers.set("X-CSRF-Token", csrf);
  if (options.body) headers.set("Content-Type", "application/json");
  const response = await fetch(url, { ...options, headers });
  let data = null;
  try { data = await response.json(); } catch { /* El error puede venir como texto. */ }
  if (!response.ok) throw new Error(data?.error || `Error HTTP ${response.status}`);
  return data;
}

function videoElement() {
  const host = tile?.querySelector("opennvr-video");
  return tile?.querySelector("video") || host?.video || null;
}

function imageRect(width = tile.clientWidth, height = tile.clientHeight) {
  const video = videoElement();
  return geom.letterboxRect(width, height, video?.videoWidth || 16, video?.videoHeight || 9);
}

function fitOverlay() {
  if (!overlay || !tile) return;
  const ratio = window.devicePixelRatio || 1;
  const width = tile.clientWidth;
  const height = tile.clientHeight;
  if (overlay.width !== Math.round(width * ratio) || overlay.height !== Math.round(height * ratio)) {
    overlay.width = Math.round(width * ratio);
    overlay.height = Math.round(height * ratio);
  }
  drawOverlay();
}

function drawPolygon(ctx, zone, rect) {
  const points = zone.points || [];
  if (points.length < 2) return;
  ctx.beginPath();
  points.forEach((point, index) => {
    const p = geom.denormalizePoint(point, rect);
    if (index === 0) ctx.moveTo(p.x, p.y); else ctx.lineTo(p.x, p.y);
  });
  ctx.closePath();
  ctx.fillStyle = "rgba(79, 156, 240, .14)";
  ctx.strokeStyle = "#4f9cf0";
  ctx.lineWidth = 2;
  ctx.fill();
  ctx.stroke();
  const label = geom.denormalizePoint(points[0], rect);
  ctx.fillStyle = "#d9ebff";
  ctx.fillText(zone.name || zone.id, label.x + 5, label.y - 6);
}

function drawOverlay() {
  if (!overlay || !tile) return;
  const ctx = overlay.getContext("2d");
  const ratio = window.devicePixelRatio || 1;
  const width = tile.clientWidth;
  const height = tile.clientHeight;
  ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
  ctx.clearRect(0, 0, width, height);
  ctx.font = "600 13px system-ui";
  ctx.lineJoin = "round";
  const rect = imageRect(width, height);
  geom.activeZones(config?.zones, motor?.pose).forEach((zone) => drawPolygon(ctx, zone, rect));

  for (const track of frame.tracks || []) {
    if (!Array.isArray(track.box) || track.box.length !== 4) continue;
    const [x1, y1, x2, y2] = track.box;
    const a = geom.denormalizePoint({ x: x1, y: y1 }, rect);
    const b = geom.denormalizePoint({ x: x2, y: y2 }, rect);
    const target = Number(frame.target) !== 0 && Number(track.id) === Number(frame.target);
    ctx.strokeStyle = target ? "#ffb52e" : "#3fb67a";
    ctx.fillStyle = ctx.strokeStyle;
    ctx.lineWidth = target ? 4 : 2;
    ctx.strokeRect(a.x, a.y, b.x - a.x, b.y - a.y);
    ctx.fillText(`#${track.id}`, a.x + 4, Math.max(rect.y + 14, a.y - 5));
  }
}

function updateState() {
  if (!stateLine) return;
  if (!motor) {
    stateLine.textContent = "Motor: conectando · pose sin datos · detector: conectando";
    return;
  }
  // Una cámara caída no sigue ni recibe órdenes: la línea entera lo dice.
  if (motor.camera === "offline") {
    stateLine.textContent = "cámara desconectada";
    return;
  }
  const pose = motor.pose;
  let poseText = "pose cualquiera";
  if (pose && Number(pose.preset) > 0) {
    const verification = pose.displaced ? "desplazada" : (pose.verified ? "verificada" : "sin verificar");
    poseText = `preset ${pose.preset} · ${verification}`;
  }
  stateLine.textContent = `Motor: ${stateLabels[motor.state] || motor.state} · ${poseText} · detector: ${detectorLabels[motor.detector] || motor.detector}`;
}

function toast(text) {
  let box = document.querySelector("#toasts");
  if (!box) {
    box = document.createElement("div");
    box.id = "toasts";
    box.setAttribute("aria-live", "polite");
    document.body.append(box);
  }
  const notice = document.createElement("div");
  notice.className = "toast vision-toast";
  notice.textContent = text;
  box.append(notice);
  setTimeout(() => notice.remove(), 5000);
}

function connectStream() {
  const events = new EventSource(`/api/vision/${encodeURIComponent(channel)}/stream`);
  events.addEventListener("frame", (event) => {
    try {
      frame = JSON.parse(event.data);
      lastFrameAt = Date.now();
      drawOverlay();
    } catch { /* Se espera el siguiente frame válido. */ }
  });
  events.addEventListener("state", (event) => {
    try {
      motor = JSON.parse(event.data);
      updateState();
      drawOverlay();
    } catch { /* Se espera el siguiente estado válido. */ }
  });
  events.addEventListener("vision", (event) => {
    try {
      const visionEvent = JSON.parse(event.data);
      if (visionEvent.kind === "zone_enter") {
        toast(visionEvent.message || `Persona en zona${visionEvent.zone_id ? ` ${visionEvent.zone_id}` : ""}`);
      }
    } catch { /* Se espera el siguiente evento válido. */ }
  });
  events.addEventListener("error", () => {
    if (stateLine) stateLine.textContent = "Reconectando la información de visión…";
  });
}

function setFormValues() {
  if (!config || !form) return;
  const tracking = config.tracking || {};
  form.elements.enabled.checked = !!config.enabled;
  form.elements.tracking_enabled.checked = !!tracking.enabled;
  form.elements.speed.value = tracking.speed ?? 5;
  form.elements.zoom_target_height.value = Math.round((tracking.zoom_target_height ?? 0.5) * 100);
  form.elements.return_after_s.value = tracking.return_after_s ?? 20;
  form.elements.home_preset.value = config.home_preset ?? 1;
  form.elements.max_pan_pulses.value = tracking.max_pan_pulses ?? 20;
  form.elements.max_tilt_pulses.value = tracking.max_tilt_pulses ?? 10;
  form.elements.max_zoom_pulses.value = tracking.max_zoom_pulses ?? 10;
  applyProposedPreset();
}

function configFromForm() {
  const values = form.elements;
  return {
    enabled: values.enabled.checked,
    home_preset: Number(values.home_preset.value),
    tracking: {
      enabled: values.tracking_enabled.checked,
      speed: Number(values.speed.value),
      zoom_target_height: Number(values.zoom_target_height.value) / 100,
      return_after_s: Number(values.return_after_s.value),
      max_pan_pulses: Number(values.max_pan_pulses.value),
      max_tilt_pulses: Number(values.max_tilt_pulses.value),
      max_zoom_pulses: Number(values.max_zoom_pulses.value),
    },
    zones: config?.zones || [],
    references: config?.references || {},
  };
}

async function putConfig(next) {
  const saved = await request(`/api/vision/${encodeURIComponent(channel)}/config`, {
    method: "PUT", body: JSON.stringify(next),
  });
  const references = saved.references || config?.references || {};
  config = { ...saved, references };
  setFormValues();
  renderZones();
  drawOverlay();
  return config;
}

form?.addEventListener("submit", async (event) => {
  event.preventDefault();
  const error = document.querySelector("#vision-config-error");
  error.textContent = "";
  try {
    await putConfig(configFromForm());
    toast("Ajustes de visión guardados");
  } catch (err) {
    error.textContent = err.message;
  }
});

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

async function updateReference(preset, button) {
  const previous = button?.textContent;
  if (button) { button.disabled = true; button.textContent = "Actualizando…"; }
  try {
    const result = await request(`/api/vision/${encodeURIComponent(channel)}/reference`, {
      method: "POST", body: JSON.stringify({ preset: Number(preset) }),
    });
    const fresh = await request(`/api/vision/${encodeURIComponent(channel)}/config`);
    config = fresh;
    setFormValues();
    renderZones();
    toast(`Referencia ${result.mode === "night" ? "nocturna" : "diurna"} actualizada`);
  } finally {
    if (button?.isConnected) { button.disabled = false; button.textContent = previous; }
  }
}

function renderReferences() {
  const container = document.querySelector("#vision-references");
  container.replaceChildren();
  const presets = new Set();
  if (Number(config.home_preset) >= 1) presets.add(Number(config.home_preset));
  for (const zone of config.zones || []) {
    if (Number(zone.pose?.preset) >= 1) presets.add(Number(zone.pose.preset));
  }
  for (const preset of [...presets].sort((a, b) => a - b)) {
    const reference = config.references?.[String(preset)] || {};
    const row = el("div", "reference-row");
    row.append(el("span", "", `Preset ${preset}: día ${reference.day ? "sí" : "no"} · noche ${reference.night ? "sí" : "no"}`));
    const button = el("button", "", "Actualizar referencia");
    button.type = "button";
    button.addEventListener("click", async () => {
      try { await updateReference(preset, button); } catch (err) { toast(err.message); }
    });
    row.append(button);
    container.append(row);
  }
}

function renderZones() {
  if (!config) return;
  renderReferences();
  const container = document.querySelector("#vision-zones");
  container.replaceChildren();
  for (const zone of config.zones || []) {
    const card = el("article", "zone-card");
    const text = el("div");
    text.append(el("strong", "", zone.name || zone.id));
    text.append(el("span", "muted", `Preset ${zone.pose?.preset || 0} · alerta ${zone.alert ? "sí" : "no"} · zoom ${zone.zoom ? "sí" : "no"}`));
    const actions = el("div", "row");
    const edit = el("button", "", "Editar");
    edit.type = "button";
    edit.addEventListener("click", () => openEditor(zone));
    const remove = el("button", "danger", "Borrar");
    remove.type = "button";
    remove.addEventListener("click", async () => {
      if (!confirm(`¿Borrar la zona «${zone.name || zone.id}»?`)) return;
      try {
        await putConfig({ ...configFromForm(), zones: config.zones.filter((item) => item.id !== zone.id) });
      } catch (err) { toast(err.message); }
    });
    actions.append(edit, remove);
    card.append(text, actions);
    container.append(card);
  }
  if (!config.zones?.length) container.append(el("p", "muted", "No hay zonas configuradas."));
}

const newZonePreset = document.querySelector("#new-zone-preset");
let presetTouched = false;
newZonePreset?.addEventListener("input", () => { presetTouched = true; });

// El preset propuesto es el de inicio, salvo que ya se haya elegido otro.
function applyProposedPreset() {
  if (!newZonePreset || !config || presetTouched) return;
  newZonePreset.value = config.home_preset ?? 1;
}

const editor = {
  root: document.querySelector("#zone-editor"),
  stage: document.querySelector(".zone-editor-stage"),
  canvas: document.querySelector("#zone-canvas"),
  image: null,
  zoneID: null,
  preset: 0,
  points: [],
  baseline: "[]",
  closed: false,
  drag: -1,
  moved: false,
  suppressClick: false,
  hover: null,
  hoverIndex: -1,
};

function pointsKey(points) {
  return JSON.stringify((points || []).map((point) => [
    Math.round(point.x * 10000) / 10000,
    Math.round(point.y * 10000) / 10000,
  ]));
}

function isDirty() {
  return pointsKey(editor.points) !== editor.baseline;
}

function editorRect() {
  return geom.letterboxRect(editor.canvas.width, editor.canvas.height,
    editor.image?.naturalWidth || editor.image?.width || 1,
    editor.image?.naturalHeight || editor.image?.height || 1);
}

function editorPoint(event) {
  const bounds = editor.canvas.getBoundingClientRect();
  return {
    x: (event.clientX - bounds.left) * editor.canvas.width / (bounds.width || 1),
    y: (event.clientY - bounds.top) * editor.canvas.height / (bounds.height || 1),
  };
}

function pointRadius() {
  const display = editor.canvas.getBoundingClientRect().width;
  return geom.captureRadius(12, editor.canvas.width, display);
}

function placedPoints() {
  const rect = editorRect();
  return editor.points.map((point) => geom.denormalizePoint(point, rect));
}

function vertexAt(point) {
  return geom.nearestPoint(placedPoints(), point, pointRadius());
}

function drawEditor() {
  if (!editor.image) return;
  const ctx = editor.canvas.getContext("2d");
  const width = editor.canvas.width;
  const height = editor.canvas.height;
  const rect = editorRect();
  const unit = geom.captureRadius(1, width, editor.canvas.getBoundingClientRect().width || width);
  ctx.clearRect(0, 0, width, height);
  ctx.fillStyle = "#000";
  ctx.fillRect(0, 0, width, height);
  ctx.drawImage(editor.image, rect.x, rect.y, rect.width, rect.height);
  const points = placedPoints();
  if (!points.length) return;
  ctx.beginPath();
  ctx.lineJoin = "round";
  ctx.lineCap = "round";
  points.forEach((point, index) => index ? ctx.lineTo(point.x, point.y) : ctx.moveTo(point.x, point.y));
  if (editor.closed) ctx.closePath();
  const hoverNormalized = editor.hover && geom.normalizePoint(editor.hover, rect);
  const inside = editor.closed && hoverNormalized && geom.pointInPolygon(hoverNormalized, editor.points);
  ctx.fillStyle = inside ? "rgba(255, 181, 46, .24)" : "rgba(79, 156, 240, .18)";
  ctx.strokeStyle = "#4f9cf0";
  ctx.lineWidth = Math.max(2 * unit, 1);
  if (editor.closed) ctx.fill();
  ctx.stroke();
  const hot = editor.drag >= 0 ? editor.drag : editor.hoverIndex;
  const radius = 12 * unit;
  const drawVertex = (point, active) => {
    ctx.beginPath();
    ctx.arc(point.x, point.y, active ? radius * 1.35 : radius, 0, Math.PI * 2);
    ctx.fillStyle = active ? "#fff4d2" : "#ffb52e";
    ctx.fill();
    ctx.lineWidth = Math.max(2 * unit, 1);
    ctx.strokeStyle = active ? "#ffb52e" : "#111315";
    ctx.stroke();
  };
  points.forEach((point, index) => { if (index !== hot) drawVertex(point, false); });
  if (hot >= 0 && points[hot]) drawVertex(points[hot], true);
}

function fitEditor() {
  if (!editor.root?.open || !editor.canvas) return;
  const bounds = editor.canvas.getBoundingClientRect();
  if (bounds.width < 2 || bounds.height < 2) return;
  const ratio = window.devicePixelRatio || 1;
  const width = Math.max(1, Math.round(bounds.width * ratio));
  const height = Math.max(1, Math.round(bounds.height * ratio));
  if (editor.canvas.width !== width || editor.canvas.height !== height) {
    editor.canvas.width = width;
    editor.canvas.height = height;
  }
  if (editor.image) drawEditor();
}

function refreshHelp() {
  const help = document.querySelector("#zone-help");
  if (!help || !editor.image) return;
  const preset = editor.preset ? `preset ${editor.preset}` : "preset cualquiera";
  help.textContent = `${preset} · ${editor.points.length} puntos`;
}

function hideConfirm() {
  const box = document.querySelector("#zone-confirm");
  if (box) box.hidden = true;
}

function showConfirm() {
  const box = document.querySelector("#zone-confirm");
  if (!box) return;
  box.hidden = false;
  box.scrollIntoView({ block: "nearest" });
  document.querySelector("#zone-confirm-stay")?.focus();
}

function requestClose() {
  if (isDirty()) { showConfirm(); return; }
  editor.root?.close();
}

function setCursor(grabbing) {
  if (!editor.canvas) return;
  editor.canvas.style.cursor = grabbing ? "grabbing" : (editor.hoverIndex >= 0 ? "grab" : "crosshair");
}

async function loadSnapshot() {
  const response = await fetch(`/camera/${encodeURIComponent(channel)}/snapshot`, { cache: "no-store" });
  if (!response.ok) throw new Error(`No se pudo obtener la captura (${response.status})`);
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  try {
    const image = new Image();
    image.src = url;
    await image.decode();
    editor.image = image;
  } finally {
    URL.revokeObjectURL(url);
  }
}

async function openEditor(zone = null) {
  const error = document.querySelector("#zone-error");
  const preset = Number(zone?.pose?.preset ?? newZonePreset?.value ?? config?.home_preset ?? 0);
  if (!Number.isInteger(preset) || preset < 0 || preset > 256) {
    toast("El preset debe ser un número entre 0 y 256.");
    return;
  }
  error.textContent = "";
  hideConfirm();
  editor.zoneID = zone?.id || null;
  editor.preset = preset;
  editor.points = (zone?.points || []).map((point) => ({ x: point.x, y: point.y }));
  editor.baseline = pointsKey(editor.points);
  editor.closed = editor.points.length >= 3;
  editor.drag = -1;
  editor.hover = null;
  editor.hoverIndex = -1;
  editor.image = null;
  document.querySelector("#zone-editor-title").textContent = zone ? "Editar zona" : "Nueva zona";
  document.querySelector("#zone-name").value = zone?.name || "";
  document.querySelector("#zone-alert").checked = zone?.alert ?? true;
  document.querySelector("#zone-zoom").checked = zone?.zoom ?? true;
  document.querySelector("#zone-help").textContent = "Preparando la imagen…";
  if (editor.canvas) {
    const ctx = editor.canvas.getContext("2d");
    ctx.clearRect(0, 0, editor.canvas.width, editor.canvas.height);
  }
  if (editor.root && !editor.root.open) editor.root.showModal();
  try {
    if (editor.preset >= 1) await updateReference(editor.preset);
    await loadSnapshot();
    await new Promise((resolve) => requestAnimationFrame(resolve));
    fitEditor();
    refreshHelp();
  } catch (err) {
    error.textContent = err.message;
  }
}

function closePolygon() {
  const error = document.querySelector("#zone-error");
  if (editor.points.length < 3) {
    error.textContent = "La zona necesita al menos 3 puntos.";
    return;
  }
  error.textContent = "";
  editor.closed = true;
  drawEditor();
}

document.querySelector("#new-zone")?.addEventListener("click", () => openEditor());
document.querySelector("#zone-close")?.addEventListener("click", closePolygon);
document.querySelector("#zone-undo")?.addEventListener("click", () => {
  editor.points.pop();
  editor.closed = false;
  document.querySelector("#zone-error").textContent = "";
  refreshHelp();
  drawEditor();
});
document.querySelector("#zone-cancel")?.addEventListener("click", requestClose);
document.querySelector("#zone-confirm-stay")?.addEventListener("click", hideConfirm);
document.querySelector("#zone-confirm-close")?.addEventListener("click", () => editor.root?.close());
editor.root?.addEventListener("cancel", (event) => {
  if (!isDirty()) return;
  event.preventDefault();
  showConfirm();
});
editor.root?.addEventListener("close", () => {
  editor.drag = -1;
  editor.hover = null;
  editor.hoverIndex = -1;
  hideConfirm();
  setCursor(false);
});

editor.canvas?.addEventListener("pointerdown", (event) => {
  if (!editor.image) return;
  hideConfirm();
  const point = editorPoint(event);
  editor.drag = vertexAt(point);
  editor.hoverIndex = editor.drag;
  editor.moved = false;
  if (editor.drag >= 0) {
    editor.canvas.setPointerCapture(event.pointerId);
    setCursor(true);
  }
});

editor.canvas?.addEventListener("pointermove", (event) => {
  const point = editorPoint(event);
  editor.hover = point;
  if (editor.drag >= 0) {
    editor.points = geom.dragPoint(editor.points, editor.drag, point, editorRect());
    editor.moved = true;
    editor.hoverIndex = editor.drag;
    setCursor(true);
  } else {
    editor.hoverIndex = vertexAt(point);
    setCursor(false);
  }
  drawEditor();
});

editor.canvas?.addEventListener("pointerleave", () => {
  editor.hover = null;
  if (editor.drag < 0) {
    editor.hoverIndex = -1;
    setCursor(false);
    drawEditor();
  }
});

editor.canvas?.addEventListener("pointerup", (event) => {
  if (editor.drag >= 0 && editor.canvas.hasPointerCapture(event.pointerId)) editor.canvas.releasePointerCapture(event.pointerId);
  editor.suppressClick = editor.moved || editor.drag >= 0;
  editor.drag = -1;
  editor.hoverIndex = vertexAt(editorPoint(event));
  setCursor(false);
  drawEditor();
});

editor.canvas?.addEventListener("click", (event) => {
  if (!editor.image || editor.suppressClick) { editor.suppressClick = false; return; }
  if (editor.closed || event.detail > 1) return;
  if (editor.points.length >= 32) {
    document.querySelector("#zone-error").textContent = "La zona admite como máximo 32 puntos.";
    return;
  }
  editor.points.push(geom.normalizePoint(editorPoint(event), editorRect()));
  document.querySelector("#zone-error").textContent = "";
  refreshHelp();
  drawEditor();
});

editor.canvas?.addEventListener("dblclick", (event) => {
  event.preventDefault();
  closePolygon();
});

if (editor.stage) new ResizeObserver(() => fitEditor()).observe(editor.stage);

function zoneID(name) {
  const base = name.normalize("NFD").replace(/[\u0300-\u036f]/g, "").toLowerCase()
    .replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "zona";
  let id = base;
  let suffix = 2;
  while ((config.zones || []).some((zone) => zone.id === id)) id = `${base}-${suffix++}`;
  return id;
}

document.querySelector("#zone-save")?.addEventListener("click", async () => {
  const error = document.querySelector("#zone-error");
  const name = document.querySelector("#zone-name").value.trim();
  error.textContent = "";
  if (!editor.closed || editor.points.length < 3) {
    error.textContent = "Cierra un polígono de al menos 3 puntos antes de guardar.";
    return;
  }
  if (!name) { error.textContent = "Escribe un nombre para la zona."; return; }
  const zone = {
    id: editor.zoneID || zoneID(name), name,
    points: editor.points.map((point) => ({ x: point.x, y: point.y })),
    pose: { preset: editor.preset },
    alert: document.querySelector("#zone-alert").checked,
    zoom: document.querySelector("#zone-zoom").checked,
  };
  const zones = [...(config.zones || [])];
  const index = zones.findIndex((item) => item.id === editor.zoneID);
  if (index >= 0) zones[index] = zone; else zones.push(zone);
  try {
    await putConfig({ ...configFromForm(), zones });
    editor.root?.close();
    toast("Zona guardada");
  } catch (err) { error.textContent = err.message; }
});

async function init() {
  try {
    config = await request(`/api/vision/${encodeURIComponent(channel)}/config`);
    setFormValues();
    renderZones();
    drawOverlay();
  } catch (err) {
    document.querySelector("#vision-config-error").textContent = err.message;
  }
  connectStream();
  fitOverlay();
}

new ResizeObserver(fitOverlay).observe(tile);
window.addEventListener("resize", () => { fitOverlay(); fitEditor(); });
setInterval(() => {
  if (lastFrameAt && Date.now() - lastFrameAt > 2000 && frame.tracks?.length) {
    frame = { tracks: [], target: 0 };
    drawOverlay();
  }
}, 500);

init();

"use strict";

const labels = {
  zone_enter: "Entrada en zona",
  zone_leave: "Salida de zona",
  tracking_start: "Inicio de seguimiento",
  tracking_stop: "Fin de seguimiento",
  camera_displaced: "Cámara desplazada",
  camera_offline: "Cámara desconectada",
  camera_online: "Cámara conectada",
  detector_down: "Detector no disponible",
};

// Mismo punto de conexión que el directo: rojo caída, verde vuelta.
const icons = {
  camera_offline: "bad",
  camera_online: "ok",
};

const form = document.querySelector("#vision-event-filters");
const list = document.querySelector("#vision-events");
const error = document.querySelector("#vision-events-error");
const dialog = document.querySelector("#vision-snapshot-dialog");

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

// El historial guarda la ruta relativa a su directorio ("snapshots/<día>/<id>.jpg");
// la ruta HTTP ya incluye "snapshots".
function snapshotURL(path) {
  const rel = String(path).replace(/^snapshots\//, "");
  return `/vision/snapshots/${rel.split("/").map(encodeURIComponent).join("/")}`;
}

function localTime(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value || "—") : date.toLocaleString("es-CL");
}

function kindTitle(kind) {
  const strong = element("strong", "event-kind");
  if (icons[kind]) {
    const dot = element("span", `dot ${icons[kind]}`);
    dot.setAttribute("aria-hidden", "true");
    strong.append(dot);
  }
  strong.append(document.createTextNode(labels[kind] || kind || "Evento"));
  return strong;
}

function syncKindFilter() {
  const select = form?.elements?.kind;
  if (!select) return;
  const present = new Set(Array.from(select.options, (option) => option.value));
  for (const kind of ["camera_offline", "camera_online"]) {
    if (present.has(kind)) continue;
    const option = document.createElement("option");
    option.value = kind;
    option.textContent = labels[kind];
    select.append(option);
  }
}

function showSnapshot(path, description) {
  const image = dialog.querySelector("img");
  image.src = snapshotURL(path);
  image.alt = description;
  dialog.showModal();
}

function render(events) {
  list.replaceChildren();
  for (const event of events) {
    const card = element("article", "vision-event-card");
    if (event.snapshot) {
      const button = element("button", "snapshot-button");
      button.type = "button";
      const image = element("img");
      image.src = snapshotURL(event.snapshot);
      image.loading = "lazy";
      image.alt = `Instantánea de cámara ${event.cam}`;
      button.append(image);
      button.addEventListener("click", () => showSnapshot(event.snapshot, image.alt));
      card.append(button);
    } else {
      card.append(element("div", "snapshot-empty", "Sin imagen"));
    }
    const body = element("div", "vision-event-body");
    body.append(element("time", "", localTime(event.at)));
    body.append(kindTitle(event.kind));
    body.append(element("span", "muted", `Cámara ${event.cam}${event.zone_id ? ` · zona ${event.zone_id}` : ""}`));
    if (event.message) body.append(element("span", "", event.message));
    card.append(body);
    list.append(card);
  }
  if (!events.length) list.append(element("p", "muted", "No hay eventos con estos filtros."));
}

async function load() {
  error.textContent = "";
  list.replaceChildren(element("p", "muted", "Cargando…"));
  const query = new URLSearchParams();
  const data = new FormData(form);
  if (data.get("cam")) query.set("cam", data.get("cam"));
  if (data.get("kind")) query.append("kind", data.get("kind"));
  query.set("limit", "50");
  try {
    const response = await fetch(`/api/vision/events?${query}`);
    const payload = await response.json();
    if (!response.ok) throw new Error(payload.error || `Error HTTP ${response.status}`);
    render(payload.events || []);
  } catch (err) {
    list.replaceChildren();
    error.textContent = err.message;
  }
}

form?.addEventListener("submit", (event) => { event.preventDefault(); load(); });
dialog?.querySelector("[data-close]")?.addEventListener("click", () => dialog.close());
dialog?.addEventListener("click", (event) => { if (event.target === dialog) dialog.close(); });

syncKindFilter();
load();

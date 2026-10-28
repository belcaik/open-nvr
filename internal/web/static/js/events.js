// Avisos en vivo de los eventos del NVR (Server-Sent Events).
const kinds = { motion: "Movimiento", videolost: "Pérdida de vídeo", ai: "Detección" };

function toast(text) {
  let box = document.querySelector("#toasts");
  if (!box) {
    box = document.createElement("div");
    box.id = "toasts";
    box.setAttribute("aria-live", "polite");
    document.body.append(box);
  }
  const t = document.createElement("div");
  t.className = "toast";
  t.textContent = text;
  box.append(t);
  setTimeout(() => t.remove(), 6000);
}

const es = new EventSource("/api/events");
es.addEventListener("nvr", (e) => {
  const ev = JSON.parse(e.data);
  if (!ev.active) return;
  const what = kinds[ev.kind] || `Evento (${ev.kind})`;
  toast(`${ev.time} · ${what}${ev.channel ? ` en cámara ${ev.channel}` : ""}`);
  const tile = document.querySelector(`.tile[data-cam="${ev.channel}"]`);
  if (tile) {
    tile.classList.add("alert");
    setTimeout(() => tile.classList.remove("alert"), 10000);
  }
});

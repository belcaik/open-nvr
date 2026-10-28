// Movimiento continuo con stop de seguridad: start al pulsar, keepalive
// cada 500 ms mientras se mantiene, stop al soltar, cancelar, perder el
// foco u ocultar la página. Si dejan de llegar keepalives (red caída,
// pestaña cerrada), el servidor detiene la cámara en 1,5 s.
const csrf = JSON.parse(document.body.getAttribute("hx-headers") || "{}")["X-CSRF-Token"];

function post(url, data) {
  return fetch(url, {
    method: "POST",
    headers: { "X-CSRF-Token": csrf, "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(data || {}),
    keepalive: true,
  }).catch(() => null);
}

let active = null; // {ch, btn, timer}

function stop() {
  if (!active) return;
  clearInterval(active.timer);
  active.btn.classList.remove("active");
  const ch = active.ch;
  active = null;
  post(`/api/ptz/${ch}/stop`);
}

function start(btn) {
  stop();
  const ch = btn.dataset.ch;
  const speed = document.querySelector("#ptz-speed")?.value || "5";
  btn.classList.add("active");
  post(`/api/ptz/${ch}/start`, { cmd: btn.dataset.ptz, speed });
  active = {
    ch,
    btn,
    timer: setInterval(async () => {
      const r = await post(`/api/ptz/${ch}/keepalive`);
      if (r && r.status === 409) stop();
    }, 500),
  };
}

document.querySelectorAll("[data-ptz]").forEach((btn) => {
  btn.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    btn.setPointerCapture(e.pointerId);
    start(btn);
  });
  btn.addEventListener("pointerup", stop);
  btn.addEventListener("pointercancel", stop);
  btn.addEventListener("lostpointercapture", stop);
  btn.addEventListener("contextmenu", (e) => e.preventDefault());
});
window.addEventListener("blur", stop);
document.addEventListener("visibilitychange", () => { if (document.hidden) stop(); });
window.addEventListener("pagehide", stop);

// Presets: la URL lleva el número (1–256) del campo #preset.
document.querySelectorAll("[data-preset]").forEach((btn) => {
  btn.addEventListener("click", () => {
    const n = document.querySelector("#preset").value;
    const action = btn.dataset.preset;
    if (action === "remove" && !confirm(`¿Borrar el preset ${n}?`)) return;
    const ch = document.querySelector("[data-ptz]").dataset.ch;
    htmx.ajax("POST", `/api/ptz/${ch}/preset/${action}/${encodeURIComponent(n)}`,
      { source: btn, target: "#ptz-result", swap: "innerHTML" });
  });
});

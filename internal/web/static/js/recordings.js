import { VideoRTC } from "/static/vendor/video-rtc.js";

class RecordingVideo extends VideoRTC {
  oninit() {
    super.oninit();
    this.media = "video";
  }

  onclose() {
    this.dispatchEvent(new CustomEvent("stream-close"));
    return super.onclose();
  }
}
customElements.define("opennvr-recording", RecordingVideo);

const panel = document.querySelector("#recording-player");
const target = panel?.querySelector(".recording-video");
const state = document.querySelector("#recording-state");
const pause = document.querySelector("#recording-pause");
const seek = document.querySelector("#recording-seek");
const close = document.querySelector("#recording-close");
const csrf = document.body.dataset.csrf || "";
let active = null;

async function request(path, method = "POST", values = null, keepalive = false) {
  const response = await fetch(path, {
    method,
    headers: { "X-CSRF-Token": csrf, ...(values ? { "Content-Type": "application/x-www-form-urlencoded" } : {}) },
    body: values ? new URLSearchParams(values) : null,
    credentials: "same-origin",
    cache: "no-store",
    keepalive,
  });
  if (!response.ok) throw new Error(await response.text());
  return response.status === 204 ? null : response.json();
}

function showState(text) {
  state.textContent = text;
}

async function shutdown(keepalive = false) {
  if (!active) return;
  const current = active;
  active = null;
  target.replaceChildren();
  panel.hidden = true;
  try { await request(`/api/playbacks/${encodeURIComponent(current.id)}`, "DELETE", null, keepalive); } catch (_) {}
}

async function open(button) {
  await shutdown();
  panel.hidden = false;
  showState("Abriendo…");
  pause.disabled = true;
  seek.disabled = true;
  try {
    const view = await request("/api/playbacks", "POST", {
      channel: button.dataset.channel,
      start: button.dataset.start,
      end: button.dataset.end,
    });
    active = { ...view, paused: false, video: null };
    seek.max = Math.max(1, Math.floor((Date.parse(view.end) - Date.parse(view.start)) / 1000));
    seek.value = "0";
    const video = document.createElement("opennvr-recording");
    active.video = video;
    video.mode = "webrtc,mse";
    video.background = false;
    target.replaceChildren(video);
    video.src = `/api/stream/ws?src=${encodeURIComponent(view.stream)}`;
    video.video?.addEventListener("playing", () => showState("Reproduciendo"));
    video.addEventListener("stream-close", () => showState("Desconectada"));
    pause.disabled = false;
    seek.disabled = false;
    showState("Conectando…");
  } catch (_) {
    showState("No se pudo reproducir");
  }
}

document.querySelectorAll(".play-recording").forEach((button) => {
  button.addEventListener("click", () => open(button));
});

pause?.addEventListener("click", async () => {
  if (!active) return;
  pause.disabled = true;
  try {
    const operation = active.paused ? "resume" : "pause";
    await request(`/api/playbacks/${encodeURIComponent(active.id)}/${operation}`);
    active.paused = !active.paused;
    if (active.video?.video) {
      if (active.paused) active.video.video.pause();
      else active.video.video.play().catch(() => {});
    }
    pause.textContent = active.paused ? "Reanudar" : "Pausar";
    showState(active.paused ? "Pausada" : "Reproduciendo");
  } catch (_) { showState("Error de control"); }
  pause.disabled = false;
});

seek?.addEventListener("change", async () => {
  if (!active) return;
  const position = new Date(Date.parse(active.start) + Number(seek.value) * 1000).toISOString();
  seek.disabled = true;
  try {
    await request(`/api/playbacks/${encodeURIComponent(active.id)}/seek`, "POST", { position });
    showState(active.paused ? "Pausada" : "Reproduciendo");
  } catch (_) { showState("No se pudo mover"); }
  seek.disabled = false;
});

close?.addEventListener("click", () => shutdown());
window.addEventListener("pagehide", () => shutdown(true));

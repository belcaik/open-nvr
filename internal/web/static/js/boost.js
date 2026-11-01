// Amplifica el audio del directo por encima del 100 % del navegador: el
// micrófono de estas cámaras llega muy bajo. Ganancia + limitador en Web Audio.
// WebRTC: Chrome no saca audio remoto por createMediaElementSource, así que se
// usa el MediaStream y se silencia el <video>. MSE: el propio <video>.
const slider = document.getElementById("audio-boost");
const label = document.getElementById("audio-boost-value");
const KEY = "opennvr.audioBoost";

let context = null;
let gain = null;
let source = null;
let bound = null; // MediaStream o <video> conectado ahora

function stored() {
  try {
    const v = Number(localStorage.getItem(KEY));
    return v >= 1 && v <= Number(slider.max) ? v : 1;
  } catch {
    return 1;
  }
}

function remember(v) {
  try {
    localStorage.setItem(KEY, String(v));
  } catch {
    // Sin almacenamiento sólo se pierde la preferencia.
  }
}

function currentVideo() {
  return document.querySelector(".tile[data-cam] opennvr-video")?.video ?? null;
}

// connect enlaza la fuente actual del vídeo al grafo; se repite si el
// reproductor reconecta y cambia de MediaStream.
function connect() {
  const video = currentVideo();
  if (!video || !context) return;
  const target = video.srcObject instanceof MediaStream ? video.srcObject : video;
  if (target === bound) return;
  if (target instanceof MediaStream && target.getAudioTracks().length === 0) return;
  if (source) source.disconnect();
  if (target instanceof MediaStream) {
    source = context.createMediaStreamSource(target);
    video.muted = true;
  } else {
    source = context.createMediaElementSource(video);
  }
  source.connect(gain);
  bound = target;
}

function ensureGraph() {
  if (context) return;
  context = new AudioContext();
  gain = context.createGain();
  const limiter = context.createDynamicsCompressor();
  limiter.threshold.value = -6;
  limiter.knee.value = 0;
  limiter.ratio.value = 20;
  limiter.attack.value = 0.003;
  limiter.release.value = 0.1;
  gain.connect(limiter).connect(context.destination);
  const video = currentVideo();
  video?.addEventListener("playing", connect);
  video?.addEventListener("loadedmetadata", connect);
}

function apply(v) {
  label.textContent = `×${v}`;
  if (v <= 1 && !context) return; // sin amplificar no hace falta tocar el audio
  ensureGraph();
  gain.gain.value = v;
  if (context.state === "suspended") context.resume().catch(() => {});
  connect();
}

if (slider && label) {
  const initial = stored();
  slider.value = String(initial);
  label.textContent = `×${initial}`;
  slider.addEventListener("input", () => {
    const v = Number(slider.value);
    remember(v);
    apply(v);
  });
  // El AudioContext exige un gesto del usuario: la preferencia guardada se
  // aplica en la primera interacción con la página.
  if (initial > 1) {
    document.addEventListener("pointerdown", () => apply(Number(slider.value)), { once: true });
  }
}

// Monta el reproductor de go2rtc en cada .tile[data-cam]. Elige la variante
// del stream según lo que decodifica el navegador: HEVC tal cual si puede
// (Chrome/Edge con aceleración), o la copia H.264 transcodificada (Firefox).
import { VideoRTC } from "/static/vendor/video-rtc.js";

class OpenNVRVideo extends VideoRTC {
  oninit() {
    super.oninit();
    if (this.dataset.muted === "true") {
      this.video.muted = true;
      this.video.controls = false;
    }
  }
}
customElements.define("opennvr-video", OpenNVRVideo);

const HEVC = 'video/mp4; codecs="hvc1.1.6.L120.90"';

function supportsHEVC() {
  const mse = window.ManagedMediaSource || window.MediaSource;
  return !!(mse && mse.isTypeSupported(HEVC));
}

export function streamName(cam, quality, hevc) {
  const base = `cam${cam}` + (quality === "sub" ? "_sub" : "");
  return hevc ? base : base + "_h264";
}

function mount(tile) {
  const src = streamName(tile.dataset.cam, tile.dataset.quality, supportsHEVC());
  const v = document.createElement("opennvr-video");
  v.mode = "webrtc,mse";
  v.background = false;
  if (tile.dataset.quality === "sub") v.dataset.muted = "true";
  tile.querySelector(".video").replaceChildren(v);
  // El setter de src convierte la ruta relativa en ws(s):// y conecta.
  v.src = `/api/stream/ws?src=${encodeURIComponent(src)}`;
  tile.dataset.stream = src;
}

document.querySelectorAll(".tile[data-cam]").forEach(mount);

const button = document.querySelector("#talk-button");
const status = document.querySelector("#talk-status");

if (button && status) {
  let held = false;
  let generation = 0;
  let active = null;

  function state(name, message) {
    button.dataset.state = name;
    button.classList.toggle("active", name === "active");
    button.setAttribute("aria-pressed", name === "active" ? "true" : "false");
    status.textContent = message;
    status.classList.toggle("error", name === "error");
  }

  function cleanup(resources) {
    if (!resources || resources.stopping) return;
    resources.stopping = true;
    if (resources.socket && resources.socket.readyState < WebSocket.CLOSING) {
      resources.socket.close(1000, "Micrófono liberado");
    }
    if (resources.source) resources.source.disconnect();
    if (resources.worklet) resources.worklet.disconnect();
    if (resources.gain) resources.gain.disconnect();
    if (resources.stream) {
      for (const track of resources.stream.getTracks()) track.stop();
    }
    if (resources.context && resources.context.state !== "closed") {
      void resources.context.close();
    }
  }

  function release() {
    held = false;
    generation += 1;
    const resources = active;
    active = null;
    cleanup(resources);
    state("idle", "Listo");
  }

  function socketURL() {
    const scheme = location.protocol === "https:" ? "wss:" : "ws:";
    return `${scheme}//${location.host}/api/talk/${button.dataset.channel}`;
  }

  function waitForOpen(socket) {
    return new Promise((resolve, reject) => {
      socket.addEventListener("open", resolve, { once: true });
      socket.addEventListener("error", () => reject(new Error("WebSocket")), { once: true });
      socket.addEventListener("close", () => reject(new Error("WebSocket cerrado")), { once: true });
    });
  }

  async function begin() {
    if (!held || active) return;
    const current = ++generation;
    const resources = { stopping: false };
    active = resources;
    state("permission", "Solicitando permiso…");

    try {
      resources.stream = await navigator.mediaDevices.getUserMedia({
        video: false,
        audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true },
      });
      if (!held || current !== generation) {
        cleanup(resources);
        return;
      }

      state("connecting", "Conectando…");
      resources.context = new AudioContext({ latencyHint: "interactive" });
      await resources.context.audioWorklet.addModule(button.dataset.worklet);
      if (!held || current !== generation) {
        cleanup(resources);
        return;
      }

      resources.socket = new WebSocket(socketURL(), `opennvr-talk.${button.dataset.csrf}`);
      resources.socket.binaryType = "arraybuffer";
      await waitForOpen(resources.socket);
      if (!held || current !== generation) {
        cleanup(resources);
        return;
      }

      resources.source = resources.context.createMediaStreamSource(resources.stream);
      resources.worklet = new AudioWorkletNode(resources.context, "talk-pcm", {
        numberOfInputs: 1,
        numberOfOutputs: 1,
        outputChannelCount: [1],
      });
      resources.gain = resources.context.createGain();
      resources.gain.gain.value = 0;
      resources.worklet.port.onmessage = (event) => {
        if (active !== resources || resources.socket.readyState !== WebSocket.OPEN) return;
        if (resources.socket.bufferedAmount > 4 * 640) {
          active = null;
          cleanup(resources);
          state("error", "La conexión de audio está congestionada");
          return;
        }
        resources.socket.send(event.data);
      };
      resources.source.connect(resources.worklet).connect(resources.gain).connect(resources.context.destination);
      resources.socket.addEventListener("close", () => {
        if (active !== resources || resources.stopping) return;
        active = null;
        cleanup(resources);
        state("error", "Se perdió la conexión del micrófono");
      });
      state("active", "Hablando");
    } catch (error) {
      if (active === resources) active = null;
      cleanup(resources);
      if (held && current === generation) {
        state("error", "No se pudo iniciar el micrófono");
      }
    }
  }

  button.addEventListener("pointerdown", (event) => {
    event.preventDefault();
    held = true;
    button.setPointerCapture(event.pointerId);
    void begin();
  });
  for (const eventName of ["pointerup", "pointercancel", "lostpointercapture"]) {
    button.addEventListener(eventName, release);
  }
  button.addEventListener("keydown", (event) => {
    if (event.repeat || (event.key !== " " && event.key !== "Enter")) return;
    event.preventDefault();
    held = true;
    void begin();
  });
  button.addEventListener("keyup", (event) => {
    if (event.key === " " || event.key === "Enter") release();
  });
  button.addEventListener("contextmenu", (event) => event.preventDefault());
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") release();
  });
  window.addEventListener("pagehide", release);
  window.addEventListener("beforeunload", release);
}

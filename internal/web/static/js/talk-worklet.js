class TalkPCMProcessor extends AudioWorkletProcessor {
  constructor() {
    super();
    this.phase = 0;
    this.frame = new ArrayBuffer(640);
    this.view = new DataView(this.frame);
    this.samples = 0;
  }

  process(inputs) {
    const input = inputs[0] && inputs[0][0];
    if (!input) return true;

    const ratio = 8000 / sampleRate;
    for (const value of input) {
      this.phase += ratio;
      if (this.phase < 1) continue;
      this.phase -= 1;
      const clipped = Math.max(-1, Math.min(1, value));
      const pcm = clipped < 0 ? clipped * 32768 : clipped * 32767;
      this.view.setInt16(this.samples * 2, pcm, true);
      this.samples += 1;
      if (this.samples === 320) {
        this.port.postMessage(this.frame, [this.frame]);
        this.frame = new ArrayBuffer(640);
        this.view = new DataView(this.frame);
        this.samples = 0;
      }
    }
    return true;
  }
}

registerProcessor("talk-pcm", TalkPCMProcessor);

export default (MixinBase) =>
  class extends MixinBase {
    constructor() {
      super();
      this._is_first_frame_finished = false;
    }

    sendMessage(message) {
      if (this.channel == undefined) {
        return;
      }
      this.channel.postMessage(message);
    }

    // Send an ArrayBuffer as a binary message. The background process
    // forwards this directly to the WebSocket as a binary frame.
    // Uses base64 encoding with a prefix to avoid slow structured clone
    // of large typed arrays across the content/background boundary.
    sendBinaryMessage(buffer) {
      if (this.channel == undefined) {
        return;
      }
      const bytes = new Uint8Array(buffer);
      let binaryStr = "";
      for (let i = 0; i < bytes.length; i++) {
        binaryStr += String.fromCharCode(bytes[i]);
      }
      this.channel.postMessage("__BINARY__" + btoa(binaryStr));
    }

    log(...messages) {
      if (this.channel == undefined) {
        return;
      }
      messages.unshift(this.channel.name);
      this.sendMessage(`/log,${JSON.stringify(messages)}`);
    }

    logPerformance(work, reference) {
      let start = performance.now();
      work();
      let end = performance.now();
      let duration = end - start;
      if (duration > 10) {
        this.firstFrameLog(`${reference}: ${duration}ms`);
      }
    }

    logError(error) {
      this.log(`'${error.name}' ${error.message}`);
      this.log(`@${error.fileName}:${error.lineNumber}`);
      this.log(error.stack);
    }

    // If you're logging large objects and using a high-ish FPS (<1000ms) then you might
    // crash the browser. So use this function instead.
    firstFrameLog(...logs) {
      if (this._is_first_frame_finished) return;
      if (DEVELOPMENT) {
        this.log(logs);
      }
    }
  };

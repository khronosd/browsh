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
    sendBinaryMessage(buffer) {
      if (this.channel == undefined) {
        return;
      }
      // Convert to a plain array for postMessage compatibility across
      // the content-script/background boundary
      this.channel.postMessage({
        __binary: true,
        data: Array.from(new Uint8Array(buffer)),
      });
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

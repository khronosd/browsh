import utils from "utils";

import CommonMixin from "dom/common_mixin";

// Converts an instance of the visible DOM into an array of pixel values.
// Note that it does this both with and without the text visible in order
// to aid in a clean separation of the graphics and text in the final frame
// rendered in the terminal.
export default class extends utils.mixins(CommonMixin) {
  constructor(channel, dimensions, config) {
    super();
    this.channel = channel;
    this.dimensions = dimensions;
    this.config = config;
    this._html_image_compression = this.config["http-server"].jpeg_compression;
    this._screenshot_canvas = document.createElement("canvas");
    this._converter_canvas = document.createElement("canvas");
    this._screenshot_ctx = this._screenshot_canvas.getContext("2d");
    this._converter_ctx = this._converter_canvas.getContext("2d");
  }

  sendFrame() {
    this.__getScaledScreenshot();
    this._sendFrame();
  }

  // With full-block single-glyph font on
  getUnscaledFGPixelAt(x, y) {
    [x, y] = this._convertDOMCoordsToRelative(x, y);
    if (x === null || y === null) {
      return [null, null, null];
    }
    const width = this.dimensions.dom.sub.width;
    const pixel_data_start = parseInt(y * width * 4 + x * 4);
    let fg_rgb = this.pixels_with_text.slice(
      pixel_data_start,
      pixel_data_start + 3
    );
    return [fg_rgb[0], fg_rgb[1], fg_rgb[2]];
  }

  // Without any text showing at all
  getUnscaledBGPixelAt(x, y) {
    [x, y] = this._convertDOMCoordsToRelative(x, y);
    if (x === null || y === null) {
      return [null, null, null];
    }
    const width = this.dimensions.dom.sub.width;
    const pixel_data_start = parseInt(y * width * 4 + x * 4);
    let bg_rgb = this.pixels_without_text.slice(
      pixel_data_start,
      pixel_data_start + 3
    );
    return [bg_rgb[0], bg_rgb[1], bg_rgb[2]];
  }

  getScreenshotWithText(callback) {
    this.logPerformance(() => {
      this._getScreenshotWithText(callback);
    }, "get screenshot with text");
  }

  getScreenshotWithoutText() {
    this.logPerformance(() => {
      this._getScreenshotWithoutText();
    }, "get screenshot without text");
  }

  getOnOffScreenshots(callback) {
    this.getScreenshotWithoutText();
    this.getScreenshotWithText(callback);
  }

  _getScreenshotWithoutText() {
    this.pixels_without_text = this._getScreenshot().data;
    return this.pixels_without_text;
  }

  _getScreenshotWithText(callback) {
    this.showText();
    if (this.config["http-server-mode"]) {
      // It's a little odd that `config['http-server'].render_delay` is named as such
      // and placed here of all places. But the fact is that a delay is needed here
      // *anyway* and extending the delay kills 2 birds with one stone. Firstly solving
      // this tricky little need-to-wait-for-the-font-to-render issue *and* solving the
      // the fact that some pages just don't finish loading at `windows.onload()`.
      setTimeout(() => {
        this._getScreenshotWithTextDelayable(callback);
      }, this.config["http-server"].render_delay);
    } else {
      this._getScreenshotWithTextDelayable(callback);
    }
  }

  // I'm not entirely clear on the reason, but when a Browsh tab's only purpose is
  // to render a single frame (such as in the HTTP service), it needs a few milliseconds
  // to show the text for the first time. My only theory is that at page load some time
  // is needed to parse and render the font.
  // However in normal TTY mode, no such delay is needed, indeed even placing this
  // function inside `setTimeout()` causes oddities.
  _getScreenshotWithTextDelayable(callback) {
    this.pixels_with_text = this._getScreenshot().data;
    this.hideText();
    callback();
  }

  _getScaledScreenshot() {
    this._scaleCanvas();
    this.scaled_pixels_image_object = this._getScreenshot();
    this.scaled_pixels = this.scaled_pixels_image_object.data;
    this._unScaleCanvas();
    return this.scaled_pixels;
  }

  // It's either convert coords to relative in this class or TextBuilder. On balance it
  // seems better to retain TextBuilder's reference in absolute coords, thus somewhat
  // hiding the overhead of relative-to-the-frame coords in public methods.
  _convertDOMCoordsToRelative(x, y) {
    const top = this.dimensions.dom.sub.top;
    const bottom = this.dimensions.dom.sub.top + this.dimensions.dom.sub.height;
    const left = this.dimensions.dom.sub.left;
    const right = this.dimensions.dom.sub.left + this.dimensions.dom.sub.width;
    if (x >= left && x < right) {
      x -= this.dimensions.dom.sub.left;
    } else {
      x = null;
    }
    if (y >= top && y < bottom) {
      y -= this.dimensions.dom.sub.top;
    } else {
      y = null;
    }
    return [x, y];
  }

  // Scaled to the size where each pixel is the same size as a TTY cell
  _getScaledPixelAt(x, y) {
    const width = this.dimensions.frame.sub.width;
    const pixel_data_start = y * width * 4 + x * 4;
    const rgb = this.scaled_pixels.slice(
      pixel_data_start,
      pixel_data_start + 3
    );
    return [rgb[0], rgb[1], rgb[2]];
  }

  __getScaledScreenshot() {
    this.logPerformance(() => {
      this._getScaledScreenshot();
    }, "get scaled screenshot");
  }

  hideText() {
    document.body.classList.remove("browsh-show-text");
    document.body.classList.add("browsh-hide-text");
  }

  showText() {
    document.body.classList.remove("browsh-hide-text");
    document.body.classList.add("browsh-show-text");
  }

  _getScreenshot() {
    return this._getPixelData();
  }

  // Scale the screenshot so that 1 pixel approximates half a TTY cell.
  _scaleCanvas() {
    this._is_scaled = true;
    this._screenshot_ctx.save();
    this._screenshot_ctx.scale(
      this.dimensions.scale_factor.width,
      this.dimensions.scale_factor.height
    );
  }

  _unScaleCanvas() {
    this._screenshot_ctx.restore();
    this._is_scaled = false;
  }

  _updateCanvasSize() {
    if (this._is_scaled) return;
    this._screenshot_canvas.width = this.dimensions.dom.sub.width;
    this._screenshot_canvas.height = this.dimensions.dom.sub.height;
  }

  // Get an array of RGB values.
  // This is Firefox-only. Chrome has a nicer MediaStream for this.
  _getPixelData() {
    let width, height;
    const background_colour = "rgb(255,255,255)";
    if (this._is_scaled) {
      width = this.dimensions.frame.sub.width;
      height = this.dimensions.frame.sub.height;
    } else {
      width = this.dimensions.dom.sub.width;
      height = this.dimensions.dom.sub.height;
    }
    if (width <= 0 || height <= 0) {
      return [];
    }
    this._updateCanvasSize();
    this._screenshot_ctx.drawWindow(
      window,
      this.dimensions.dom.sub.left,
      this.dimensions.dom.sub.top,
      this.dimensions.dom.sub.width,
      this.dimensions.dom.sub.height,
      background_colour
    );
    return this._screenshot_ctx.getImageData(0, 0, width, height);
  }

  // Return the scaled screenshot as a data URI to display in HTML
  _getScaledDataURI() {
    this.__getScaledScreenshot();
    this._converter_canvas.width = this.dimensions.frame.sub.width;
    this._converter_canvas.height = this.dimensions.frame.sub.height;
    this._converter_ctx.putImageData(this.scaled_pixels_image_object, 0, 0);
    return this._converter_canvas.toDataURL(
      "image/jpeg",
      this._html_image_compression
    );
  }

  _sendFrame() {
    const meta = this.dimensions.getFrameMeta();
    meta.id = parseInt(this.channel.name);
    const width = this.dimensions.frame.sub.width;
    const height = this.dimensions.frame.sub.height;
    const pixelCount = width * height;
    if (pixelCount <= 0 || !this.scaled_pixels) {
      this.log("Not sending empty pixels frame");
      return;
    }

    // Build current RGB data (strip alpha from RGBA ImageData)
    const currentRGB = new Uint8Array(pixelCount * 3);
    for (let i = 0; i < pixelCount; i++) {
      currentRGB[i * 3] = this.scaled_pixels[i * 4];
      currentRGB[i * 3 + 1] = this.scaled_pixels[i * 4 + 1];
      currentRGB[i * 3 + 2] = this.scaled_pixels[i * 4 + 2];
    }

    // Try to send a diff if we have a previous frame with same dimensions
    if (
      this._prevPixelRGB &&
      this._prevPixelMeta &&
      this._prevPixelMeta.sub_left === meta.sub_left &&
      this._prevPixelMeta.sub_top === meta.sub_top &&
      this._prevPixelMeta.sub_width === meta.sub_width &&
      this._prevPixelMeta.sub_height === meta.sub_height
    ) {
      const diffFrame = this._buildPixelDiff(meta, currentRGB, pixelCount);
      if (diffFrame !== null) {
        this.sendBinaryMessage(diffFrame);
        this._prevPixelRGB = currentRGB;
        this._prevPixelMeta = meta;
        return;
      }
    }

    // Send full frame (first frame, dimension change, or >50% changed)
    const fullFrame = this._buildFullPixelFrame(meta, currentRGB, pixelCount);
    this.sendBinaryMessage(fullFrame);
    this._prevPixelRGB = currentRGB;
    this._prevPixelMeta = meta;
  }

  // Build a diff frame (type 0x03). Returns null if >50% of pixels changed
  // (in which case a full frame is more efficient).
  _buildPixelDiff(meta, currentRGB, pixelCount) {
    const prev = this._prevPixelRGB;
    const changedIndices = [];
    for (let i = 0; i < pixelCount; i++) {
      const off = i * 3;
      if (
        currentRGB[off] !== prev[off] ||
        currentRGB[off + 1] !== prev[off + 1] ||
        currentRGB[off + 2] !== prev[off + 2]
      ) {
        changedIndices.push(i);
      }
    }

    if (changedIndices.length === 0) {
      // Nothing changed — send an empty diff
      return this._buildEmptyDiff(meta);
    }

    // If >50% changed, a full frame is more compact
    if (changedIndices.length > pixelCount * 0.5) {
      return null;
    }

    // Diff format: [15B header][4B num_changed][per cell: 4B index + 3B RGB]
    const headerSize = 15;
    const numChanged = changedIndices.length;
    const buffer = new ArrayBuffer(headerSize + 4 + numChanged * 7);
    const view = new DataView(buffer);
    const bytes = new Uint8Array(buffer);

    this._writeHeader(view, 0x03, meta);
    view.setUint32(headerSize, numChanged);

    let offset = headerSize + 4;
    for (const idx of changedIndices) {
      view.setUint32(offset, idx);
      const rgbOff = idx * 3;
      bytes[offset + 4] = currentRGB[rgbOff];
      bytes[offset + 5] = currentRGB[rgbOff + 1];
      bytes[offset + 6] = currentRGB[rgbOff + 2];
      offset += 7;
    }

    return buffer;
  }

  _buildEmptyDiff(meta) {
    const buffer = new ArrayBuffer(15 + 4);
    const view = new DataView(buffer);
    this._writeHeader(view, 0x03, meta);
    view.setUint32(15, 0); // zero changed cells
    return buffer;
  }

  _buildFullPixelFrame(meta, rgbData, pixelCount) {
    const headerSize = 15;
    const buffer = new ArrayBuffer(headerSize + pixelCount * 3);
    const view = new DataView(buffer);
    const bytes = new Uint8Array(buffer);

    this._writeHeader(view, 0x01, meta);
    bytes.set(rgbData, headerSize);

    return buffer;
  }

  _writeHeader(view, type, meta) {
    view.setUint8(0, type);
    view.setUint16(1, meta.id);
    view.setUint16(3, meta.sub_left);
    view.setUint16(5, meta.sub_top);
    view.setUint16(7, meta.sub_width);
    view.setUint16(9, meta.sub_height);
    view.setUint16(11, meta.total_width);
    view.setUint16(13, meta.total_height);
  }

  // JSON serialisation kept for _getScaledDataURI path and tests
  _serialiseFrame() {
    this._setupFrameMeta();
    const width = this.dimensions.frame.sub.width;
    const height = this.dimensions.frame.sub.height;
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width; x++) {
        this._getScaledPixelAt(x, y).map((c) => this.frame.colours.push(c));
      }
    }
  }

  _setupFrameMeta() {
    this.frame = {
      meta: this.dimensions.getFrameMeta(),
      colours: [],
    };
    this.frame.meta.id = parseInt(this.channel.name);
  }
}

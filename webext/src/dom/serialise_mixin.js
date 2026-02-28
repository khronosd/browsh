import utils from "utils";

export default (MixinBase) =>
  class extends MixinBase {
    __serialiseFrame() {
      let cell, index;
      const top = this.dimensions.frame.sub.top / 2;
      const left = this.dimensions.frame.sub.left;
      const bottom = top + this.dimensions.frame.sub.height / 2;
      const right = left + this.dimensions.frame.sub.width;
      this._setupFrameMeta();
      this._serialiseInputBoxes();
      for (let y = top; y < bottom; y++) {
        for (let x = left; x < right; x++) {
          index = y * this.dimensions.frame.width + x;
          cell = this.tty_grid.cells[index];
          if (cell === undefined) {
            this.frame.colours.push(0);
            this.frame.colours.push(0);
            this.frame.colours.push(0);
            this.frame.text.push("");
          } else {
            cell.fg_colour.map((c) => this.frame.colours.push(c));
            this.frame.text.push(cell.rune);
          }
        }
      }
    }

    _serialiseRawText() {
      let raw_text = "";
      this._previous_cell_href = "";
      this._is_inside_anchor = false;
      const top = this.dimensions.frame.sub.top / 2;
      const left = this.dimensions.frame.sub.left;
      const bottom = top + this.dimensions.frame.sub.height / 2;
      const right = left + this.dimensions.frame.sub.width;
      for (let y = top; y < bottom; y++) {
        for (let x = left; x < right; x++) {
          raw_text += this._addCell(x, y, right);
        }
        raw_text += "\n";
      }
      return this._wrap(raw_text);
    }

    _wrap(raw_text) {
      let head;
      head =
        this._raw_mode_type === "raw_text_html"
          ? this._getHTMLHead()
          : this._getUserHeader();
      return head + raw_text + this._getFooter();
    }

    // Whether a use has shown support. This controls certain Browsh branding and
    // nags to donate.
    userHasShownSupport() {
      return (
        this.config.browsh_supporter === "I have shown my support for Browsh"
      );
    }

    _byBrowsh() {
      let by;
      if (this.userHasShownSupport()) {
        return "";
      }
      by =
        this._raw_mode_type === "raw_text_html"
          ? 'by <a href="https://www.brow.sh">Browsh</a> v'
          : "by Browsh v";
      return by + this.config.browsh_version + " ";
    }

    _getUserFooter() {
      return "\n" + this.config["http-server"].footer;
    }

    _getUserHeader() {
      return this.config["http-server"].header + "\n";
    }

    _getMetaData() {
      let metadata = "";
      this._markParsingDuration();
      const date_time = this._getCurrentDataTime();
      const elapsed = `${this._parsing_duration}ms`;
      metadata +=
        "\n\n" + `Built ` + this._byBrowsh() + `on ${date_time} in ${elapsed}.`;
      if (this.dimensions.is_page_truncated) {
        metadata +=
          "\nBrowsh parser: the page was too large, some text may have been truncated.";
      }
      return metadata;
    }

    _getDonateCall() {
      let donating;
      if (this.userHasShownSupport()) {
        return "";
      }
      donating =
        this._raw_mode_type === "raw_text_html"
          ? '<a href="https://www.brow.sh/donate">donating</a>'
          : "https://brow.sh/donate";
      return (
        "\nPlease consider " +
        donating +
        " to help all those with slow and/or expensive internet."
      );
    }

    _getFooter() {
      let start, end;
      if (this._raw_mode_type === "raw_text_html") {
        start = '<span class="browsh-footer">';
        end = "</span></pre></body></html>";
      } else {
        start = "";
        end = "";
      }
      return (
        start +
        this._getMetaData() +
        this._getDonateCall() +
        this._getUserFooter() +
        end
      );
    }

    _getHTMLHead() {
      const img_src = this.graphics_builder._getScaledDataURI();
      const width = this.dimensions.dom.sub.width;
      const height = this.dimensions.dom.sub.height;
      return `<html>
     <head>
       ${this._getFavicon()}
       <title>${document.title}</title>
       <style>
        html * {
         font-family: 'Courier New', monospace;
        }
        body {
          font-size: 15px;
        }
        pre {
          background-image: url(${img_src});
          background-repeat: no-repeat;
          background-size: ${width}px ${height}px;
          /* Pixelate the background image */
          image-rendering: -moz-crisp-edges;          /* Firefox                        */
          image-rendering: -o-crisp-edges;            /* Opera                          */
          image-rendering: -webkit-optimize-contrast; /* Chrome (and eventually Safari) */
          image-rendering: pixelated;                 /* Chrome                         */
          -ms-interpolation-mode: nearest-neighbor;   /* IE8+                           */
          width: ${width}px;
          height: ${height}px;
          /* These styles need to exactly follow Browsh's rendering styles */
          font-size: 15px !important;
          line-height: 20px !important;
          letter-spacing: 0px !important;
          font-style: normal !important;
          font-weight: normal !important;
        }
        .browsh-footer {
          opacity: 0.7;
        }
       </style>
     </head>
     <body>
     ${this._getUserHeader()}
     <pre>`;
    }

    _getFavicon() {
      let el = document.querySelector("link[rel*='icon']");
      if (el) {
        return `<link rel="shortcut icon" type = "image/x-icon" href="${el.href}">`;
      } else {
        return "";
      }
    }

    _markParsingDuration() {
      this._parsing_duration = performance.now() - this._parse_start_time;
    }

    _getCurrentDataTime() {
      let current_date = new Date();
      const offset = -(new Date().getTimezoneOffset() / 60);
      const sign = offset > 0 ? "+" : "-";
      let date_time =
        current_date.getDate() +
        "/" +
        (current_date.getMonth() + 1) +
        "/" +
        current_date.getFullYear() +
        "@" +
        current_date.getHours() +
        ":" +
        current_date.getMinutes() +
        ":" +
        current_date.getSeconds() +
        " " +
        "UTC" +
        sign +
        offset +
        " (" +
        Intl.DateTimeFormat().resolvedOptions().timeZone +
        ")";
      return date_time;
    }

    // TODO: Ultimately we're going to need to know exactly which parts of the input
    //       box are obscured. This is partly possible using the element's computed
    //       styles, however this isn't comprehensive - think partially obscuring.
    //       So the best solution is to use the same trick as we do for normal text,
    //       except that we can't fill the input box with text, however we can
    //       temporarily change the background to a contrasting colour.
    _getAllInputBoxes() {
      let dom_rect, styles, font_rgb;
      let parsed_input_boxes = {};
      let raw_input_boxes = document.querySelectorAll(
        "input, " + "textarea, " + '[role="textbox"]'
      );
      raw_input_boxes.forEach((i) => {
        let type;
        this._ensureBrowshID(i);
        dom_rect = this._convertDOMRectToAbsoluteCoords(
          i.getBoundingClientRect()
        );
        const width = utils.snap(
          dom_rect.width * this.dimensions.scale_factor.width
        );
        const height = utils.snap(
          dom_rect.height * this.dimensions.scale_factor.height
        );
        if (width == 0 || height == 0) {
          return;
        }
        type =
          i.getAttribute("role") == "textbox"
            ? "textbox"
            : i.getAttribute("type");
        styles = window.getComputedStyle(i);
        font_rgb = styles["color"]
          .replace(/[^\d,]/g, "")
          .split(",")
          .map((i) => parseInt(i));
        const padding_top = parseInt(styles["padding-top"].replace("px", ""));
        const padding_left = parseInt(styles["padding-left"].replace("px", ""));
        if (this._isUnwantedInboxBox(i, styles)) {
          return;
        }
        parsed_input_boxes[i.getAttribute("data-browsh-id")] = {
          id: i.getAttribute("data-browsh-id"),
          x: utils.snap(
            (dom_rect.left + padding_left) * this.dimensions.scale_factor.width
          ),
          y: utils.snap(
            (dom_rect.top + padding_top) * this.dimensions.scale_factor.height
          ),
          width: width,
          height: height,
          tag_name: i.nodeName,
          type: type,
          colour: [font_rgb[0], font_rgb[1], font_rgb[2]],
        };
      });
      return parsed_input_boxes;
    }

    _ensureBrowshID(element) {
      if (element.getAttribute("data-browsh-id") === null) {
        element.setAttribute("data-browsh-id", utils.uuidv4());
      }
    }

    _isUnwantedInboxBox(input_box, styles) {
      return (
        styles.display === "none" ||
        styles.visibility === "hidden" ||
        input_box.getAttribute("aria-hidden") == "true"
      );
    }

    _sendRawText() {
      let body;
      if (this._raw_mode_type == "raw_text_dom") {
        body =
          "<html>" +
          document.getElementsByTagName("html")[0].innerHTML +
          "</html>";
      } else {
        body = this._serialiseRawText();
      }
      let payload = {
        body: body,
        page_load_duration: this.config.page_load_duration,
        parsing_duration: this._parsing_duration,
      };
      this.sendMessage(`/raw_text,${JSON.stringify(payload)}`);
    }

    _sendFrame() {
      const binaryFrame = this._serialiseBinaryTextFrame();
      if (binaryFrame) {
        this.sendBinaryMessage(binaryFrame);
      } else {
        this.log("Not sending empty text frame");
      }
    }

    _serialiseBinaryTextFrame() {
      const meta = this.dimensions.getFrameMeta();
      meta.id = parseInt(this.channel.name);
      const top = this.dimensions.frame.sub.top / 2;
      const left = this.dimensions.frame.sub.left;
      const bottom = top + this.dimensions.frame.sub.height / 2;
      const right = left + this.dimensions.frame.sub.width;
      const cellCount = (bottom - top) * (right - left);
      if (cellCount <= 0) return null;

      // Build colour and text arrays
      const colourBytes = new Uint8Array(cellCount * 3);
      const textParts = [];
      let ci = 0;
      for (let y = top; y < bottom; y++) {
        for (let x = left; x < right; x++) {
          const index = y * this.dimensions.frame.width + x;
          const cell = this.tty_grid.cells[index];
          if (cell === undefined || !cell.fg_colour) {
            colourBytes[ci] = 0;
            colourBytes[ci + 1] = 0;
            colourBytes[ci + 2] = 0;
            textParts.push("");
          } else {
            colourBytes[ci] = cell.fg_colour[0];
            colourBytes[ci + 1] = cell.fg_colour[1];
            colourBytes[ci + 2] = cell.fg_colour[2];
            textParts.push(cell.rune);
          }
          ci += 3;
        }
      }

      // Encode text as null-separated UTF-8
      const textEncoded = new TextEncoder().encode(textParts.join("\0"));

      // Build binary frame
      const headerSize = 15;
      const colourLenSize = 4;
      const colourLen = colourBytes.length;
      const totalSize = headerSize + colourLenSize + colourLen + textEncoded.length;
      const buffer = new ArrayBuffer(totalSize);
      const view = new DataView(buffer);
      const bytes = new Uint8Array(buffer);

      // Header
      view.setUint8(0, 0x02); // type: text
      view.setUint16(1, meta.id);
      view.setUint16(3, meta.sub_left);
      view.setUint16(5, meta.sub_top);
      view.setUint16(7, meta.sub_width);
      view.setUint16(9, meta.sub_height);
      view.setUint16(11, meta.total_width);
      view.setUint16(13, meta.total_height);

      // Colour data length
      view.setUint32(headerSize, colourLen);

      // Colour data
      bytes.set(colourBytes, headerSize + colourLenSize);

      // Text data
      bytes.set(textEncoded, headerSize + colourLenSize + colourLen);

      // Also send input boxes via JSON (they're small and infrequent)
      const input_boxes = this._getAllInputBoxes();
      if (input_boxes && Object.keys(input_boxes).length > 0) {
        const inputBoxMsg = `/input_boxes,${JSON.stringify({
          meta: meta,
          input_boxes: input_boxes,
        })}`;
        this.sendMessage(inputBoxMsg);
      }

      return buffer;
    }

    _addCell(x, y, right) {
      let text = "";
      const index = y * this.dimensions.frame.width + x;
      this._cell_for_raw_text = this.tty_grid.cells[index];
      if (this._raw_mode_type === "raw_text_html") {
        this._is_line_end = x === right - 1;
        text += this._addCellAsHTML();
      } else {
        text += this._addCellAsPlainText();
      }
      return text;
    }

    _addCellAsHTML() {
      this._HTML = "";
      if (!this._cell_for_raw_text) {
        this._addHTMLForNonExistentCell();
      } else {
        this._current_cell_href = this._cell_for_raw_text.parent_element.href;
        this._is_HREF_changed =
          this._current_cell_href !== this._previous_cell_href;
        this._handleCellOutsideAnchor();
        this._handleCellInsideAnchor();
        this._HTML += this._cell_for_raw_text.rune;
        this._previous_cell_href = this._current_cell_href;
      }
      if (this._will_be_inside_anchor !== undefined) {
        this._is_inside_anchor = this._will_be_inside_anchor;
      }
      return this._HTML;
    }

    _addHTMLForNonExistentCell() {
      if (this._is_inside_anchor) {
        this._previous_cell_href = undefined;
        this._closeAnchorTag();
      }
      this._HTML += " ";
    }

    _handleCellOutsideAnchor() {
      if (this._is_inside_anchor) {
        return;
      }
      if (this._current_cell_href || this._is_HREF_changed) {
        this._openAnchorTag();
      }
    }

    _handleCellInsideAnchor() {
      if (!this._is_inside_anchor) {
        return;
      }
      if (
        this._is_HREF_changed ||
        !this._current_cell_href ||
        this._is_line_end
      ) {
        this._closeAnchorTag();
        if (this._current_cell_href) {
          this._openAnchorTag();
        }
      }
    }

    _openAnchorTag() {
      this._will_be_inside_anchor = true;
      this._HTML += `<a href="/${this._current_cell_href}">`;
    }

    _closeAnchorTag() {
      this._will_be_inside_anchor = false;
      this._HTML += `</a>`;
    }

    _addCellAsPlainText() {
      if (this._cell_for_raw_text === undefined) {
        return " ";
      }
      return this._cell_for_raw_text.rune;
    }

    _setupFrameMeta() {
      this.frame = {
        meta: this.dimensions.getFrameMeta(),
        text: [],
        colours: [],
      };
      this.frame.meta.id = parseInt(this.channel.name);
    }

    _serialiseInputBoxes() {
      this.frame.input_boxes = this._getAllInputBoxes();
    }
  };

import utils from "utils";

// The TTY grid
export default class {
  constructor(dimensions, graphics_builder, config) {
    this.dimensions = dimensions;
    this.graphics_builder = graphics_builder;
    this.config = config;
    this._setMiddleOfEm();
  }

  getCell(index) {
    return this.cells[index];
  }

  getCellAt(x, y) {
    return this.cells[y * this.dimensions.frame.width + x];
  }

  addCell(new_cell) {
    new_cell.index = this._calculateIndex(new_cell);
    const is_cell_possibly_obscured = !this._handleCellVisibility(new_cell);
    const is_cell_at_highest_layer = this._isNewCellAtHighestLayer(new_cell);
    if (is_cell_at_highest_layer && !is_cell_possibly_obscured) {
      this.cells[new_cell.index] = new_cell;
    }
  }

  _isNewCellAtHighestLayer(new_cell) {
    let existing_cell = this.cells[new_cell.index];
    if (existing_cell === undefined) return true;
    // When a character clobbers another in the grid, use elementFromPoint
    // to determine which is on top. This is expensive but only triggers
    // on cell collisions, not every cell.
    const found_element = document.elementFromPoint(
      new_cell.dom_coords.x,
      new_cell.dom_coords.y
    );
    // If elementFromPoint returns null (element not in viewport), assume visible
    if (found_element === null) return true;
    return new_cell.parent_element === found_element;
  }

  _handleCellVisibility(new_cell) {
    const colours = this._getColours(new_cell);
    if (!colours) return false;
    if (this._isCharObscured(colours)) return false;
    new_cell.fg_colour = colours[0];
    new_cell.bg_colour = colours[1];
    return true;
  }

  _calculateIndex(cell) {
    return cell.tty_coords.y * this.dimensions.frame.width + cell.tty_coords.x;
  }

  // Get the colours for a cell. When experimental text visibility is enabled,
  // samples pixel data from with/without-text screenshots. Otherwise uses
  // getComputedStyle which avoids 2 expensive drawWindow() calls per frame.
  _getColours(cell) {
    if (this.config.browsh.use_experimental_text_visibility) {
      return this._getColoursFromScreenshots(cell);
    }
    return this._getColoursFromComputedStyle(cell);
  }

  // Original pixel-sampling approach: requires two full-viewport screenshots
  _getColoursFromScreenshots(cell) {
    const offset_x = utils.snap(
      cell.dom_coords.x + this.dimensions.char.width * this._middle_of_em
    );
    const offset_y = utils.snap(
      cell.dom_coords.y + this.dimensions.char.height * this._middle_of_em
    );
    const fg_colour = this.graphics_builder.getUnscaledFGPixelAt(
      offset_x,
      offset_y
    );
    const bg_colour = this.graphics_builder.getUnscaledBGPixelAt(
      offset_x,
      offset_y
    );
    return [fg_colour, bg_colour];
  }

  // Fast approach: use CSS computed styles instead of pixel sampling.
  // Eliminates 2 of 3 drawWindow() calls per frame cycle.
  _getColoursFromComputedStyle(cell) {
    const element = cell.parent_element;
    if (!element) return null;
    const styles = window.getComputedStyle(element);
    const fg = this._parseRGB(styles.color);
    const bg = this._findBackgroundColor(element);
    return [fg, bg];
  }

  _parseRGB(colorString) {
    const match = colorString.match(/\d+/g);
    if (!match || match.length < 3) return [0, 0, 0];
    return [parseInt(match[0]), parseInt(match[1]), parseInt(match[2])];
  }

  // Walk up the DOM to find the first non-transparent background color
  _findBackgroundColor(element) {
    let el = element;
    while (el && el !== document.documentElement) {
      const bg = window.getComputedStyle(el).backgroundColor;
      if (bg && bg !== "transparent" && bg !== "rgba(0, 0, 0, 0)") {
        return this._parseRGB(bg);
      }
      el = el.parentElement;
    }
    // Default to white background
    return [255, 255, 255];
  }

  // This is the value to reach the middle of a uni-glyph font character in order to
  // sample its colour. Obviosuly it is better to reach for the middle in case there are
  // vagaries of rendering, it increases our chances of actually getting the characters
  // own colour and not some other colour nearby.
  //
  // However during testing, we use very small self-generated pixel arrays which makes
  // the snapped values rather unintuitive. So we just encourage the snaped values to
  // snap lower which just lends itself to more readable test values.
  _setMiddleOfEm() {
    this._middle_of_em = TEST ? 0.49 : 0.5;
  }

  // This is somewhat of a, hopefully elegant, hack. So, imagine that situation where you're
  // browsing a web page and a popup appears; perhaps just a select box, or menu, or worst
  // of all a dreaded full-page overlay. Now, DOM rectangles don't take into account whether
  // they are the uppermost visible element, so we're left in a bit of a pickle. The only JS
  // way to know if an element is visible is to use `Document.elementFromPoint(x, y)`, where
  // you compare the returned element with the element whose visibility you're checking.
  // This is has a number of problems. Firstly, it only checks one coordinate in the element
  // for visibility, which of course isn't going to 100% reliably speak for all the
  // characters in the element. Secondly, even ignoring the first caveat, running
  // `elementFromPoint()` for every character is very expensive, around 25ms for an average
  // DOM. So it's basically a no-go. So instead we take advantage of the fact that we're
  // working with a snapshot of the the webpage's pixels. It's pretty good assumption that if
  // you make the text transparent and a pixel's colour doesn't change then that character
  // must be obscured by something.
  //
  // There are of course some potential edge cases with this. What if we get a false
  // positive, where a character is obscured _by another character_? Hopefully in such a
  // case we can work with `z-index` so that characters justifiably overwrite each other in
  // the TTY grid.
  _isCharObscured(colours) {
    if (!this.config.browsh.use_experimental_text_visibility) {
      return false;
    }
    return (
      colours[0][0] === colours[1][0] &&
      colours[0][1] === colours[1][1] &&
      colours[0][2] === colours[1][2]
    );
  }
}

package browsh

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"unicode"

	"github.com/gdamore/tcell"
)

// A frame is a single snapshot of the DOM. The TTY is merely a window onto a
// region of this frame.
type frame struct {
	// Dimensions of the frame's real data. Can be less than the DOM dimensions because
	// we cannot sync frames of unlimited size from the browser.
	subWidth  int
	subHeight int
	// If the frame is smaller than the DOM, then this is the frame's position
	// within the overall DOM.
	subLeft int
	subTop  int
	// The total DOM dimensions. These are measured in the same units of the frame
	totalWidth  int
	totalHeight int
	// The current position of the scroll in the TTY. Should be synced with the real
	// browser.
	xScroll int
	yScroll int
	// Usually we want to just overlay new data. But if the DOM changes then all bets are off
	// and we need to start from scratch again. It's just too unpredictable how data for a DOM
	// of a different size and shape will interact with data from another DOM.
	isDOMSizeChanged bool
	// Raw data used to build a single, usable frame. Flat slices indexed by
	// (row * totalWidth) + col for O(1) access and cache-friendly iteration.
	pixels      [][2]tcell.Color
	pixelsValid []bool
	text        [][]rune
	textColours []tcell.Color
	textValid   []bool
	// The actual built frame using double-buffered lock-free cell storage
	cells *doubleBufferedCells
	// Input boxes, like for entering passwords, sending emails etc
	inputBoxes map[string]*inputBox
}

type jsonFrameBase struct {
	TabID       int `json:"id"`
	SubWidth    int `json:"sub_width"`
	SubHeight   int `json:"sub_height"`
	SubLeft     int `json:"sub_left"`
	SubTop      int `json:"sub_top"`
	TotalWidth  int `json:"total_width"`
	TotalHeight int `json:"total_height"`
}

type incomingFrameText struct {
	Meta       jsonFrameBase       `json:"meta"`
	Text       []string            `json:"text"`
	Colours    []int32             `json:"colours"`
	InputBoxes map[string]inputBox `json:"input_boxes"`
}

type incomingFramePixels struct {
	Meta    jsonFrameBase `json:"meta"`
	Colours []int32       `json:"colours"`
}

// Binary frame header layout (15 bytes):
//   [0]     message type (0x01=pixels, 0x02=text)
//   [1:3]   tab ID (uint16 big-endian)
//   [3:5]   sub_left
//   [5:7]   sub_top
//   [7:9]   sub_width
//   [9:11]  sub_height
//   [11:13] total_width
//   [13:15] total_height
func parseBinaryHeader(data []byte) jsonFrameBase {
	return jsonFrameBase{
		TabID:       int(binary.BigEndian.Uint16(data[1:3])),
		SubLeft:     int(binary.BigEndian.Uint16(data[3:5])),
		SubTop:      int(binary.BigEndian.Uint16(data[5:7])),
		SubWidth:    int(binary.BigEndian.Uint16(data[7:9])),
		SubHeight:   int(binary.BigEndian.Uint16(data[9:11])),
		TotalWidth:  int(binary.BigEndian.Uint16(data[11:13])),
		TotalHeight: int(binary.BigEndian.Uint16(data[13:15])),
	}
}

// parseBinaryFramePixels parses a binary pixel frame.
// After the 15-byte header, the payload is raw RGB triplets (3 bytes per pixel).
func parseBinaryFramePixels(data []byte) {
	meta := parseBinaryHeader(data)
	tabsMu.RLock()
	defer tabsMu.RUnlock()
	if !isTabPresentLocked(meta.TabID) {
		slog.Warn("Not building binary pixel frame for non-existent tab", "TabID", meta.TabID)
		return
	}
	if Tabs[meta.TabID].frame.text == nil {
		return
	}
	f := &Tabs[meta.TabID].frame
	f.setup(meta)

	pixelData := data[binaryHeaderLen:]
	expectedPixels := meta.SubWidth * meta.SubHeight
	if len(pixelData) < expectedPixels*3 {
		slog.Warn("Binary pixel frame too short", "expected", expectedPixels*3, "got", len(pixelData))
		return
	}

	sliceSize := f.domRowCount() * f.totalWidth
	if f.isDOMSizeChanged || f.pixels == nil {
		f.pixels = make([][2]tcell.Color, sliceSize)
		f.pixelsValid = make([]bool, sliceSize)
	}

	f.cells.copyFrontToBack()
	var cellIndex int
	pixelDataLen := len(pixelData)
	pixelsLen := len(f.pixels)
	for y := 0; y < meta.SubHeight; y += 2 {
		for x := 0; x < meta.SubWidth; x++ {
			cellIndex = f.getCellIndexFromSubCoords(x, y)
			if cellIndex < 0 || cellIndex >= pixelsLen {
				continue
			}
			bgOffset := ((y * meta.SubWidth) + x) * 3
			fgOffset := (((y + 1) * meta.SubWidth) + x) * 3
			// Bounds check: ensure both bg and fg pixel data are available
			if bgOffset+2 >= pixelDataLen || fgOffset+2 >= pixelDataLen {
				continue
			}
			f.pixels[cellIndex] = [2]tcell.Color{
				tcell.NewRGBColor(
					int32(pixelData[bgOffset]),
					int32(pixelData[bgOffset+1]),
					int32(pixelData[bgOffset+2]),
				),
				tcell.NewRGBColor(
					int32(pixelData[fgOffset]),
					int32(pixelData[fgOffset+1]),
					int32(pixelData[fgOffset+2]),
				),
			}
			if cellIndex < len(f.pixelsValid) {
				f.pixelsValid[cellIndex] = true
			}
			f.buildCell(f.subLeft+x, (f.subTop+y)/2)
		}
	}
	f.cells.swap()
}

// applyBinaryPixelDiff applies a sparse pixel diff (type 0x03).
// Layout after 15-byte header:
//   [15:19]  number of changed cells (uint32 big-endian)
//   For each changed cell: [4B cell_index (uint32)][3B RGB]
func applyBinaryPixelDiff(data []byte) {
	if len(data) < binaryHeaderLen+4 {
		slog.Warn("Binary pixel diff too short")
		return
	}
	meta := parseBinaryHeader(data)
	tabsMu.RLock()
	defer tabsMu.RUnlock()
	if !isTabPresentLocked(meta.TabID) {
		return
	}
	f := &Tabs[meta.TabID].frame
	if f.pixels == nil {
		// No base frame to diff against — skip
		return
	}

	numChanged := int(binary.BigEndian.Uint32(data[binaryHeaderLen : binaryHeaderLen+4]))
	if numChanged == 0 {
		return
	}

	payloadStart := binaryHeaderLen + 4
	expectedLen := payloadStart + numChanged*7
	if len(data) < expectedLen {
		slog.Warn("Binary pixel diff truncated", "expected", expectedLen, "got", len(data))
		return
	}

	sliceSize := len(f.pixels)
	f.cells.copyFrontToBack()

	offset := payloadStart
	for i := 0; i < numChanged; i++ {
		pixelIdx := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		r := int32(data[offset+4])
		g := int32(data[offset+5])
		b := int32(data[offset+6])
		offset += 7

		// The pixelIdx is a flat index into the scaled pixel grid.
		// Map it to a cell index using the sub-frame dimensions.
		cellX := pixelIdx % meta.SubWidth
		cellY := (pixelIdx / meta.SubWidth)
		// Pixel rows come in pairs (bg=even, fg=odd) for the half-block trick
		subY := (cellY / 2) * 2
		cellIndex := f.getCellIndexFromSubCoords(cellX, subY)
		if cellIndex < 0 || cellIndex >= sliceSize {
			continue
		}

		// Update the appropriate pixel colour (bg for even rows, fg for odd)
		if cellY%2 == 0 {
			f.pixels[cellIndex][0] = tcell.NewRGBColor(r, g, b)
		} else {
			f.pixels[cellIndex][1] = tcell.NewRGBColor(r, g, b)
		}
		f.pixelsValid[cellIndex] = true
		f.buildCell(f.subLeft+cellX, (f.subTop+subY)/2)
	}

	f.cells.swap()
}

// parseBinaryFrameText parses a binary text frame.
// Layout after 15-byte header:
//   [15:19]  colour data length in bytes (uint32 big-endian)
//   [19:19+colourLen]  RGB colour data (3 bytes per cell)
//   [19+colourLen:]    UTF-8 text data, null-separated per cell
func parseBinaryFrameText(data []byte) {
	if len(data) < binaryHeaderLen+4 {
		slog.Warn("Binary text frame too short for colour length")
		return
	}
	meta := parseBinaryHeader(data)
	tabsMu.RLock()
	defer tabsMu.RUnlock()
	if !isTabPresentLocked(meta.TabID) {
		slog.Info(fmt.Sprintf("Not building binary text frame for non-existent tab ID: %d", meta.TabID))
		return
	}
	f := &Tabs[meta.TabID].frame
	f.setup(meta)

	colourLen := int(binary.BigEndian.Uint32(data[binaryHeaderLen : binaryHeaderLen+4]))
	colourStart := binaryHeaderLen + 4
	colourEnd := colourStart + colourLen
	if len(data) < colourEnd {
		slog.Warn("Binary text frame truncated in colour data")
		return
	}
	colourData := data[colourStart:colourEnd]
	textData := data[colourEnd:]

	cellCount := meta.SubWidth * (meta.SubHeight / 2)
	sliceSize := f.domRowCount() * f.totalWidth
	if f.isDOMSizeChanged || f.text == nil {
		f.text = make([][]rune, sliceSize)
		f.textColours = make([]tcell.Color, sliceSize)
		f.textValid = make([]bool, sliceSize)
	}

	// Parse null-separated text into a slice of strings
	textStrings := make([]string, 0, cellCount)
	start := 0
	for i := 0; i <= len(textData); i++ {
		if i == len(textData) || textData[i] == 0 {
			textStrings = append(textStrings, string(textData[start:i]))
			start = i + 1
		}
	}
	f.cells.copyFrontToBack()
	for y := 0; y < f.subRowCount(); y++ {
		for x := 0; x < f.subWidth; x++ {
			cellIndex := f.getCellIndexFromSubCoords(x, y*2)
			if cellIndex < 0 || cellIndex >= sliceSize {
				continue
			}
			frameIndex := (y * f.subWidth) + x
			colourOffset := frameIndex * 3
			if colourOffset+2 < len(colourData) {
				f.textColours[cellIndex] = tcell.NewRGBColor(
					int32(colourData[colourOffset]),
					int32(colourData[colourOffset+1]),
					int32(colourData[colourOffset+2]),
				)
			}
			if frameIndex < len(textStrings) {
				f.text[cellIndex] = []rune(textStrings[frameIndex])
			}
			f.textValid[cellIndex] = true
			f.buildCell(f.subLeft+x, (f.subTop/2)+y)
		}
	}
	f.cells.swap()
}

func (f *frame) domRowCount() int {
	return f.totalHeight / 2
}

func (f *frame) subRowCount() int {
	return f.subHeight / 2
}

func parseJSONFrameText(jsonString string) {
	var incoming incomingFrameText
	jsonBytes := []byte(jsonString)
	if err := json.Unmarshal(jsonBytes, &incoming); err != nil {
		Shutdown(err)
	}
	tabsMu.RLock()
	defer tabsMu.RUnlock()
	if !isTabPresentLocked(incoming.Meta.TabID) {
		slog.Info(
			fmt.Sprintf("Not building frame for non-existent tab ID: %d", incoming.Meta.TabID),
		)
		return
	}
	Tabs[incoming.Meta.TabID].frame.buildFrameText(incoming)
}

func (f *frame) buildFrameText(incoming incomingFrameText) {
	f.setup(incoming.Meta)
	if !f.isIncomingFrameTextValid(incoming) {
		return
	}
	f.updateInputBoxes(incoming)
	f.populateFrameText(incoming)
}

func parseJSONFramePixels(jsonString string) {
	var incoming incomingFramePixels
	jsonBytes := []byte(jsonString)
	if err := json.Unmarshal(jsonBytes, &incoming); err != nil {
		Shutdown(err)
	}
	tabsMu.RLock()
	defer tabsMu.RUnlock()
	if !isTabPresentLocked(incoming.Meta.TabID) {
		slog.Warn("Not building frame for non-existent tab ID", "TabID", incoming.Meta.TabID)
		return
	}
	if Tabs[incoming.Meta.TabID].frame.text == nil {
		return
	}
	Tabs[incoming.Meta.TabID].frame.buildFramePixels(incoming)
}

func (f *frame) buildFramePixels(incoming incomingFramePixels) {
	f.setup(incoming.Meta)
	if !f.isIncomingFramePixelsValid(incoming) {
		return
	}
	f.populateFramePixels(incoming)
}

func (f *frame) setup(meta jsonFrameBase) {
	f.isDOMSizeChanged = meta.TotalWidth != f.totalWidth || meta.TotalHeight != f.totalHeight
	// Update dimensions before resetCells so size calculation is correct
	f.subWidth = meta.SubWidth
	f.subHeight = meta.SubHeight
	f.totalWidth = meta.TotalWidth
	f.totalHeight = meta.TotalHeight
	f.subLeft = meta.SubLeft
	f.subTop = meta.SubTop
	if f.isDOMSizeChanged || f.cells == nil {
		f.resetCells()
	}
	if f.inputBoxes == nil {
		f.inputBoxes = make(map[string]*inputBox)
	}
}

func (f *frame) resetCells() {
	size := f.domRowCount() * f.totalWidth
	if size <= 0 {
		size = 1
	}
	f.cells = newDoubleBufferedCells(size)
}

func (f *frame) isIncomingFrameTextValid(incoming incomingFrameText) bool {
	if len(incoming.Text) == 0 {
		slog.Warn("Not parsing zero-size text frame")
		return false
	}
	return true
}

// parseInputBoxes handles the separate /input_boxes JSON message sent
// alongside binary text frames.
func parseInputBoxes(jsonString string) {
	var incoming struct {
		Meta       jsonFrameBase       `json:"meta"`
		InputBoxes map[string]inputBox `json:"input_boxes"`
	}
	if err := json.Unmarshal([]byte(jsonString), &incoming); err != nil {
		slog.Warn("Failed to parse input_boxes", "error", err)
		return
	}
	tabsMu.RLock()
	defer tabsMu.RUnlock()
	if !isTabPresentLocked(incoming.Meta.TabID) {
		return
	}
	f := &Tabs[incoming.Meta.TabID].frame
	if f.inputBoxes == nil {
		f.inputBoxes = make(map[string]*inputBox)
	}
	// Reuse the existing updateInputBoxes logic via a shim
	textShim := incomingFrameText{InputBoxes: incoming.InputBoxes}
	f.updateInputBoxes(textShim)
}

// TODO: There must be a more idiomatic way of doing this?
func (f *frame) updateInputBoxes(incoming incomingFrameText) {
	for _, existingInputBox := range f.inputBoxes {
		if _, ok := incoming.InputBoxes[existingInputBox.ID]; !ok {
			// TODO: Does this also delete the memory pointed to by the reference?
			delete(f.inputBoxes, existingInputBox.ID)
		}
	}
	for _, incomingInputBox := range incoming.InputBoxes {
		if _, ok := f.inputBoxes[incomingInputBox.ID]; !ok {
			f.inputBoxes[incomingInputBox.ID] = newInputBox(incomingInputBox.ID)
		}
		inputBox := f.inputBoxes[incomingInputBox.ID]
		inputBox.X = incomingInputBox.X
		// TODO: Why do we have to add the 1 to the y coord??
		inputBox.Y = (incomingInputBox.Y + 1) / 2
		inputBox.Width = incomingInputBox.Width
		inputBox.Height = incomingInputBox.Height / 2
		inputBox.FgColour = incomingInputBox.FgColour
		inputBox.TagName = incomingInputBox.TagName
		inputBox.Type = incomingInputBox.Type
	}
}

func (f *frame) populateFrameText(incoming incomingFrameText) {
	var cellIndex, frameIndex, colourIndex int
	sliceSize := f.domRowCount() * f.totalWidth
	if f.isDOMSizeChanged || f.text == nil {
		f.text = make([][]rune, sliceSize)
		f.textColours = make([]tcell.Color, sliceSize)
		f.textValid = make([]bool, sliceSize)
	}
	// Copy front buffer to back so incremental sub-frames preserve existing cells
	f.cells.copyFrontToBack()
	for y := 0; y < f.subRowCount(); y++ {
		for x := 0; x < f.subWidth; x++ {
			cellIndex = f.getCellIndexFromSubCoords(x, y*2)
			if cellIndex < 0 || cellIndex >= sliceSize {
				continue
			}
			frameIndex = (y * f.subWidth) + x
			colourIndex = frameIndex * 3
			f.textColours[cellIndex] = tcell.NewRGBColor(
				incoming.Colours[colourIndex+0],
				incoming.Colours[colourIndex+1],
				incoming.Colours[colourIndex+2],
			)
			f.text[cellIndex] = []rune(incoming.Text[frameIndex])
			f.textValid[cellIndex] = true
			f.buildCell(f.subLeft+x, (f.subTop/2)+y)
		}
	}
	f.cells.swap()
}

func (f *frame) populateFramePixels(incoming incomingFramePixels) {
	var cellIndex, frameIndexFg, frameIndexBg, pixelIndexFg, pixelIndexBg int
	sliceSize := f.domRowCount() * f.totalWidth
	if f.isDOMSizeChanged || f.pixels == nil {
		f.pixels = make([][2]tcell.Color, sliceSize)
		f.pixelsValid = make([]bool, sliceSize)
	}
	data := incoming.Colours
	// Copy front buffer to back so incremental sub-frames preserve existing cells
	f.cells.copyFrontToBack()
	for y := 0; y < f.subHeight; y += 2 {
		for x := 0; x < f.subWidth; x++ {
			cellIndex = f.getCellIndexFromSubCoords(x, y)
			if cellIndex < 0 || cellIndex >= sliceSize {
				continue
			}
			frameIndexBg = (y * f.subWidth) + x
			frameIndexFg = ((y + 1) * f.subWidth) + x
			pixelIndexBg = frameIndexBg * 3
			pixelIndexFg = frameIndexFg * 3
			pixels := [2]tcell.Color{
				tcell.NewRGBColor(
					data[pixelIndexBg+0],
					data[pixelIndexBg+1],
					data[pixelIndexBg+2],
				),
				tcell.NewRGBColor(
					data[pixelIndexFg+0],
					data[pixelIndexFg+1],
					data[pixelIndexFg+2],
				),
			}
			f.pixels[cellIndex] = pixels
			f.pixelsValid[cellIndex] = true
			f.buildCell(f.subLeft+x, (f.subTop+y)/2)
		}
	}
	f.cells.swap()
}

func (f *frame) isIncomingFramePixelsValid(incoming incomingFramePixels) bool {
	if len(incoming.Colours) == 0 {
		slog.Warn("Not parsing zero-size text frame")
		return false
	}
	return true
}

// This is where we implement the UTF8 half-block trick.
// This a half-block: "▄", notice how it takes up precisely half a text cell. This
// means that we can get 2 pixel colours from it, the top pixel comes from setting
// the background colour and the bottom pixel comes from setting the foreground
// colour, namely the colour of the text.
func (f *frame) buildCell(x int, y int) {
	index := (y * f.totalWidth) + x
	character, fgColour := f.getCharacterAt(index)
	pixelFg, bgColour := f.getPixelColoursAt(index)
	if isCharacterTransparent(character) {
		character = []rune("▄")
		fgColour = pixelFg
	}
	f.addCell(index, fgColour, bgColour, character)
}

func (f *frame) getCharacterAt(index int) ([]rune, tcell.Color) {
	if index >= 0 && index < len(f.textValid) && f.textValid[index] {
		return f.text[index], f.textColours[index]
	}
	return []rune(" "), tcell.ColorBlack
}

func (f *frame) getPixelColoursAt(index int) (tcell.Color, tcell.Color) {
	if index >= 0 && index < len(f.pixelsValid) && f.pixelsValid[index] {
		return f.pixels[index][1], f.pixels[index][0]
	}
	x := index % f.totalWidth
	return getHatchedCellColours(x)
}

func isCharacterTransparent(character []rune) bool {
	return string(character) == "" || unicode.IsSpace(character[0])
}

func (f *frame) addCell(index int, fgColour, bgColour tcell.Color, character []rune) {
	newCell := cell{
		fgColour:  fgColour,
		bgColour:  bgColour,
		character: character,
	}
	f.cells.store(index, newCell)
}

// When iterating over a sub frame we still need to place the resulting data into the
// overall frame grid. So here we're essentially mapping relative coordinates to
// absolute ones. Also note that the y coord is converted from the frame pixels value
// to the TTY row value.
func (f *frame) getCellIndexFromSubCoords(x, y int) int {
	yInAbsoluteFrameTTY := (y + f.subTop) / 2
	return (yInAbsoluteFrameTTY * f.totalWidth) + (x + f.subLeft)
}

func (f *frame) limitScroll(height int) {
	maxYScroll := f.domRowCount() - height
	if f.yScroll > maxYScroll {
		f.yScroll = maxYScroll
	}
	if f.yScroll < 0 {
		f.yScroll = 0
	}
}

func (f *frame) maybeFocusInputBox(x, y int) {
	activeInputBox = nil
	for _, inputBox := range f.inputBoxes {
		inputBox.isActive = false
		top := inputBox.Y
		bottom := inputBox.Y + inputBox.Height
		left := inputBox.X
		right := inputBox.X + inputBox.Width
		if x >= left && x < right && y >= top && y < bottom {
			urlBarFocus(false)
			inputBox.isActive = true
			activeInputBox = inputBox
		}
	}
}

func (f *frame) overlayInputBoxContent() {
	for _, inputBox := range f.inputBoxes {
		inputBox.setCells()
	}
}

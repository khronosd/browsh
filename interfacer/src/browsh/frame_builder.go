package browsh

import (
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

// TODO: Can these be sent as binary blobs?
type incomingFramePixels struct {
	Meta    jsonFrameBase `json:"meta"`
	Colours []int32       `json:"colours"`
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

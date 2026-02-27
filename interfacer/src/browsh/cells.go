package browsh

import (
	"sync/atomic"

	"github.com/gdamore/tcell"
)

// A cell represents an individual TTY cell. An entire representation of the browser
// DOM is stored in a local in-memory "frame". The TTY can then quickly render a region
// of this frame for fast scrolling.
type cell struct {
	character []rune
	fgColour  tcell.Color
	bgColour  tcell.Color
}

// cellBuffer holds a flat slice of cells and a parallel validity tracker.
type cellBuffer struct {
	cells []cell
	valid []bool
	size  int
}

func newCellBuffer(size int) *cellBuffer {
	return &cellBuffer{
		cells: make([]cell, size),
		valid: make([]bool, size),
		size:  size,
	}
}

// doubleBufferedCells uses two cell buffers and an atomic index to allow
// lock-free reads from the active (front) buffer while the writer builds
// into the inactive (back) buffer. After a full frame is built, the writer
// calls swap() to atomically make the back buffer the new front buffer.
type doubleBufferedCells struct {
	buffers [2]*cellBuffer
	active  atomic.Int32 // 0 or 1
}

func newDoubleBufferedCells(size int) *doubleBufferedCells {
	db := &doubleBufferedCells{}
	db.buffers[0] = newCellBuffer(size)
	db.buffers[1] = newCellBuffer(size)
	db.active.Store(0)
	return db
}

// load reads a cell from the active (front) buffer. Lock-free.
func (db *doubleBufferedCells) load(key int) (value cell, ok bool) {
	front := db.buffers[db.active.Load()]
	if key < 0 || key >= front.size {
		return cell{}, false
	}
	if !front.valid[key] {
		return cell{}, false
	}
	return front.cells[key], true
}

// store writes a cell into the back buffer.
func (db *doubleBufferedCells) store(key int, value cell) {
	backIdx := 1 - db.active.Load()
	back := db.buffers[backIdx]
	if key < 0 || key >= back.size {
		return
	}
	back.cells[key] = value
	back.valid[key] = true
}

// copyFrontToBack copies the current front buffer contents into the back buffer
// so that incremental updates (sub-frames) preserve existing cell data.
func (db *doubleBufferedCells) copyFrontToBack() {
	frontIdx := db.active.Load()
	backIdx := 1 - frontIdx
	front := db.buffers[frontIdx]
	back := db.buffers[backIdx]
	copy(back.cells, front.cells)
	copy(back.valid, front.valid)
}

// swap atomically makes the back buffer the new front buffer.
func (db *doubleBufferedCells) swap() {
	db.active.Store(1 - db.active.Load())
}

// resize creates new buffers of the given size, discarding old data.
func (db *doubleBufferedCells) resize(size int) {
	db.buffers[0] = newCellBuffer(size)
	db.buffers[1] = newCellBuffer(size)
	db.active.Store(0)
}

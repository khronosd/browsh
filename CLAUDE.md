# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Browsh is a text-based browser that renders modern web pages in a terminal. It works by running a headless Firefox instance with a custom WebExtension that analyzes the DOM, then communicates over WebSocket to a Go CLI that renders the output via tcell.

## Architecture

Two main components connected via WebSocket (localhost:3334):

- **Go backend** (`interfacer/`): CLI application, terminal rendering (tcell), HTTP server mode, Firefox lifecycle management via Marionette protocol
- **JavaScript WebExtension** (`webext/`): Runs inside Firefox, traverses the DOM, converts pages to text/graphics cells, sends data to Go backend

Entry point: `interfacer/cmd/browsh/main.go` → `browsh.MainEntry()`

Key Go packages are all in `interfacer/src/browsh/` (flat structure, single package):
- `browsh.go` — core init, TTY startup, logging
- `firefox.go` — headless Firefox management, Marionette protocol
- `comms.go` — WebSocket server
- `raw_text_server.go` — HTTP server mode
- `frame_builder.go` — converts browser DOM data to text cells
- `tty.go` / `ui.go` — terminal rendering
- `input_*.go` — input handling

Key WebExtension modules:
- `src/background/manager.js` — main orchestrator, WebSocket client
- `src/background/tab.js` — per-tab rendering management
- `src/dom/text_builder.js` / `graphics_builder.js` — DOM-to-cell conversion

The WebExtension is compiled to an XPI file (`interfacer/src/browsh/browsh.xpi`) and embedded in the Go binary.

## Build & Development Commands

### Go backend (run from `interfacer/`)
```bash
go run ./cmd/browsh --debug        # Run with debug logging (logs to ./debug.log)
go run ./cmd/browsh --firefox.with-gui  # Run with visible Firefox window
```

### WebExtension (run from `webext/`)
```bash
npm install                        # Install JS dependencies
npm run build:dev                  # One-time webpack build
npm run build:watch                # Webpack watch mode for development
```

### Full build via ctl.sh (from repo root)
```bash
./ctl.sh build_browsh_binary       # Compile Go binary
./ctl.sh build_webextension_production  # Production webext build + signing
```

## Testing

### WebExtension tests (Mocha/Chai, from `webext/`)
```bash
npm test
```

### Go unit tests (Ginkgo/Gomega, from `interfacer/`)
```bash
go test -v $(find src/browsh -name '*.go' | grep -v windows)
```

### E2E TTY tests (from `interfacer/`)
```bash
go test test/tty/*.go -v -ginkgo.slowSpecThreshold=30 -ginkgo.flakeAttempts=3
```

### HTTP server tests (from `interfacer/`)
```bash
go test test/http-server/*.go -v -ginkgo.slowSpecThreshold=30 -ginkgo.flakeAttempts=3
```

Test HTML fixtures are in `interfacer/test/sites/smorgasbord/`.

## Linting

```bash
# Go formatting check (from interfacer/)
gofmt -l src/

# JS formatting (from webext/)
npx prettier --list-different '{src,test}/**/*.js'
npx prettier --write '{src,test}/**/*.js'   # auto-fix
```

## Key Conventions

- Version is defined in `interfacer/src/browsh/version.go` and injected into `webext/manifest.json` via webpack
- Platform-specific code uses `_unix.go` / `_windows.go` suffixes
- Go tests use Ginkgo/Gomega BDD-style framework
- Config is TOML-based; defaults in `interfacer/src/browsh/config_sample.go`
- Critical errors call `Shutdown(err)` which logs and exits
- Thread-safe map types (`threadSafe*Map`) are used for concurrent access in Go
- E2E tests require Firefox to be installed; CI uses Firefox 140.0

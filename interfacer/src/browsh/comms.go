package browsh

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-errors/errors"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
)

// Binary frame message types
const (
	msgTypePixels   byte = 0x01
	msgTypeText     byte = 0x02
	binaryHeaderLen      = 15
)

var (
	upgrader = websocket.Upgrader{
		CheckOrigin:     func(r *http.Request) bool { return true },
		ReadBufferSize:  128 * 1024,
		WriteBufferSize: 128 * 1024,
	}
	stdinChannel              = make(chan string, 32)
	IsConnectedToWebExtension = false
)

type incomingRawText struct {
	RequestID string `json:"request_id"`
	RawJSON   string `json:"json"`
}

func startWebSocketServer() {
	serverMux := http.NewServeMux()
	serverMux.HandleFunc("/", webSocketServer)
	port := viper.GetString("browsh.websocket-port")
	slog.Info("Starting websocket server...")
	if netErr := http.ListenAndServe(":"+port, serverMux); netErr != nil {
		Shutdown(fmt.Errorf("Error starting websocket server: %w", netErr))
	}
}

func sendMessageToWebExtension(message string) {
	if !IsConnectedToWebExtension {
		slog.Info("Webextension not connected. Message not sent", "message", message)
		return
	}
	stdinChannel <- message
}

// Listen to all messages coming from the webextension
// TODO: It seems this *also* receives sent to the webextention!?
func webSocketReader(ws *websocket.Conn) {
	defer ws.Close()
	for {
		messageType, message, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseGoingAway) {
				slog.Info("Socket reader detected that the browser closed the websocket")
				triggerSocketWriterClose()
				return
			}
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				slog.Error("Socket reader detected that the connection unexpectedly dissapeared")
				triggerSocketWriterClose()
				return
			}
			Shutdown(err)
		}
		if messageType == websocket.BinaryMessage {
			handleBinaryFrame(message)
		} else {
			handleWebextensionCommand(message)
		}
	}
}

func handleBinaryFrame(data []byte) {
	if len(data) < binaryHeaderLen {
		slog.Warn("Binary frame too short", "len", len(data))
		return
	}
	switch data[0] {
	case msgTypePixels:
		parseBinaryFramePixels(data)
		renderCurrentTabWindow()
	case msgTypeText:
		parseBinaryFrameText(data)
	default:
		slog.Warn("Unknown binary frame type", "type", data[0])
	}
}

// splitCommand splits a message at the first comma, returning (command, payload).
// This avoids splitting the entire multi-megabyte JSON payload at every comma
// then re-joining it, which caused 3+ full copies of each frame in memory.
func splitCommand(msg string) (command, payload string) {
	idx := strings.IndexByte(msg, ',')
	if idx == -1 {
		return msg, ""
	}
	return msg[:idx], msg[idx+1:]
}

func handleWebextensionCommand(message []byte) {
	msg := string(message)
	command, payload := splitCommand(msg)
	if viper.GetBool("http-server-mode") {
		handleRawFrameTextCommands(command, payload)
		return
	}
	switch command {
	case "/frame_text":
		parseJSONFrameText(payload)
	case "/frame_pixels":
		parseJSONFramePixels(payload)
		renderCurrentTabWindow()
	case "/tab_state":
		parseJSONTabState(payload)
		tabsMu.RLock()
		ct := CurrentTab
		tabsMu.RUnlock()
		if ct != nil {
			renderUI()
		}
	case "/input_boxes":
		parseInputBoxes(strings.Join(parts[1:], ","))
	case "/screenshot":
		saveScreenshot(payload)
	default:
		slog.Info("WEBEXT", "message", msg)
	}
}

func handleRawFrameTextCommands(command, payload string) {
	var incoming incomingRawText
	if command == "/raw_text" {
		if err := json.Unmarshal([]byte(payload), &incoming); err != nil {
			Shutdown(err)
		}
		if incoming.RequestID != "" {
			slog.Info("Raw text for", "RequestID", incoming.RequestID)
			pendingRequests.resolve(incoming.RequestID, incoming.RawJSON)
		} else {
			slog.Info("Raw text but no associated request ID")
		}
	} else {
		slog.Info("WEBEXT", "command", command+","+payload)
	}
}

// When the socket reader attempts to read from a closed websocket it quickly and
// simply closes its associated Go routine. However the socket writer won't
// automatically notice until it actually needs to send something. So we force that
// by sending this NOOP text.
// TODO: There's a potential race condition because new connections share the same
//
//	Go channel. So we need to setup a new channel for every connection.
func triggerSocketWriterClose() {
	stdinChannel <- "BROWSH CLIENT FORCING CLOSE OF WEBSOCKET WRITER"
}

// Send a message to the webextension
func webSocketWriter(ws *websocket.Conn) {
	var message string
	defer ws.Close()
	for {
		message = <-stdinChannel
		slog.Info("TTY sending", "message", message)
		if err := ws.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
			if errors.Is(err, websocket.ErrCloseSent) {
				slog.Info("Socket writer detected that the browser closed the websocket")
			} else {
				slog.Error("Socket writer detected unexpected closure of websocket", "error", err)
			}
			return
		}
	}
}

func webSocketServer(w http.ResponseWriter, r *http.Request) {
	slog.Info("Incoming web request from browser")
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		Shutdown(err)
	}
	IsConnectedToWebExtension = true
	go webSocketWriter(ws)
	go webSocketReader(ws)
	sendConfigToWebExtension()
	setDefaultFirefoxPreferences()
	if !viper.GetBool("http-server-mode") {
		sendTtySize()
	}
	// For some reason, using Firefox's CLI arg `--url https://google.com` doesn't consistently
	// work. So we do it here instead.
	validURL := viper.GetStringSlice("validURL")
	if len(validURL) == 0 {
		if !IsHTTPServerMode {
			sendMessageToWebExtension("/new_tab," + viper.GetString("startup-url"))
		}
	} else {
		for i := 0; i < len(validURL); i++ {
			sendMessageToWebExtension("/new_tab," + validURL[i])
		}
	}
}

func sendConfigToWebExtension() {
	configJSON, _ := json.Marshal(viper.AllSettings())
	sendMessageToWebExtension("/config," + string(configJSON))
}

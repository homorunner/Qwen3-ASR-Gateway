package ws

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"gateway/internal/engine"
	"gateway/internal/session"
	pb "gateway/proto/qwen_asr"

	"github.com/gorilla/websocket"
)

// --- WebSocket Message Types ---
type ClientStartMessage struct {
	Type   string       `json:"type"`
	Config ClientConfig `json:"config"`
}

type ClientConfig struct {
	Language        string  `json:"language"`
	Context         string  `json:"context"`
	SampleRate      int     `json:"sample_rate"`
	Encoding        string  `json:"encoding"`
	ChunkSizeSec    float32 `json:"chunk_size_sec"`
	UnfixedChunkNum int     `json:"unfixed_chunk_num"`
	UnfixedTokenNum int     `json:"unfixed_token_num"`
}

type ClientControlMessage struct {
	Type string `json:"type"`
}

type ServerMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Language  string `json:"language,omitempty"`
	Text      string `json:"text,omitempty"`
	IsFinal   bool   `json:"is_final,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

// --- Handler ---

var upgrader = websocket.Upgrader{
	ReadBufferSize:  64 * 1024,
	WriteBufferSize: 64 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Handler struct {
	pool        *engine.Pool
	sessionMgr  *session.Manager
	activeConns sync.WaitGroup
}

func NewHandler(pool *engine.Pool, sessionMgr *session.Manager) *Handler {
	return &Handler{
		pool:       pool,
		sessionMgr: sessionMgr,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ERROR] WebSocket upgrade failed: %v", err)
		return
	}

	h.activeConns.Add(1)
	go h.handleConnection(conn)
}

func (h *Handler) handleConnection(conn *websocket.Conn) {
	defer h.activeConns.Done()
	defer conn.Close()

	var (
		currentSessionID string
		currentEngineEp  *engine.Endpoint
		sessionStarted   bool
		sessionFinished  bool
		writeMu          sync.Mutex
	)

	defer func() {
		if sessionStarted && !sessionFinished && currentSessionID != "" {
			h.destroySession(currentSessionID, currentEngineEp)
		}
	}()

	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[WARN] WebSocket read error: %v", err)
			}
			return
		}

		switch msgType {
		case websocket.TextMessage:
			var ctrl ClientControlMessage
			if err := json.Unmarshal(data, &ctrl); err != nil {
				sendError(&writeMu, conn, "INVALID_MESSAGE", "Failed to parse JSON message")
				continue
			}

			switch ctrl.Type {
			case "start":
				if sessionStarted {
					sendError(&writeMu, conn, "SESSION_ALREADY_STARTED", "Session already started")
					continue
				}

				var startMsg ClientStartMessage
				if err := json.Unmarshal(data, &startMsg); err != nil {
					sendError(&writeMu, conn, "INVALID_MESSAGE", "Failed to parse start message")
					continue
				}

				sid, ep, err := h.startSession(startMsg.Config)
				if err != nil {
					sendError(&writeMu, conn, "ENGINE_UNAVAILABLE", err.Error())
					continue
				}

				currentSessionID = sid
				currentEngineEp = ep
				sessionStarted = true

				sendJSON(&writeMu, conn, ServerMessage{
					Type:      "started",
					SessionID: sid,
				})

				log.Printf("[INFO] Session started: %s -> engine %d", sid, ep.ID)

			case "finish":
				if !sessionStarted || sessionFinished {
					sendError(&writeMu, conn, "NO_SESSION", "No active session to finish")
					continue
				}

				sessionFinished = true
				resp, err := h.finishSession(currentSessionID, currentEngineEp)
				if err != nil {
					sendError(&writeMu, conn, "FINISH_ERROR", err.Error())
					return
				}

				sendJSON(&writeMu, conn, ServerMessage{
					Type:      "result",
					SessionID: currentSessionID,
					Language:  resp.Language,
					Text:      resp.Text,
					IsFinal:   true,
				})

				log.Printf("[INFO] Session finished: %s", currentSessionID)
				return

			default:
				sendError(&writeMu, conn, "UNKNOWN_TYPE", "Unknown message type: "+ctrl.Type)
			}

		case websocket.BinaryMessage:
			if !sessionStarted || sessionFinished {
				sendError(&writeMu, conn, "NO_SESSION", "No active session; send 'start' first")
				continue
			}

			resp, err := h.pushAudio(currentSessionID, currentEngineEp, data)
			if err != nil {
				log.Printf("[ERROR] PushAudio error for session %s: %v", currentSessionID, err)
				sendError(&writeMu, conn, "PUSH_ERROR", err.Error())
				continue
			}

			sendJSON(&writeMu, conn, ServerMessage{
				Type:      "result",
				SessionID: currentSessionID,
				Language:  resp.Language,
				Text:      resp.Text,
				IsFinal:   false,
			})
		}
	}
}

func (h *Handler) startSession(cfg ClientConfig) (string, *engine.Endpoint, error) {
	ep, err := h.pool.PickEngine()
	if err != nil {
		return "", nil, err
	}

	sessionID := session.GenerateID()

	sampleRate := int32(cfg.SampleRate)
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	encoding := cfg.Encoding
	if encoding == "" {
		encoding = "pcm_f32le"
	}
	chunkSizeSec := cfg.ChunkSizeSec
	if chunkSizeSec <= 0 {
		chunkSizeSec = 2.0
	}
	unfixedChunkNum := int32(cfg.UnfixedChunkNum)
	if unfixedChunkNum <= 0 {
		unfixedChunkNum = 2
	}
	unfixedTokenNum := int32(cfg.UnfixedTokenNum)
	if unfixedTokenNum <= 0 {
		unfixedTokenNum = 5
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := ep.Client().CreateSession(ctx, &pb.CreateSessionRequest{
		SessionId: sessionID,
		Config: &pb.SessionConfig{
			Language:        cfg.Language,
			Context:         cfg.Context,
			SampleRate:      sampleRate,
			Encoding:        encoding,
			ChunkSizeSec:    chunkSizeSec,
			UnfixedChunkNum: unfixedChunkNum,
			UnfixedTokenNum: unfixedTokenNum,
		},
	})
	if err != nil {
		return "", nil, err
	}
	if !resp.Success {
		return "", nil, &SessionError{Message: resp.ErrorMessage}
	}

	// Register session locally
	h.sessionMgr.Create(sessionID, ep.ID)
	ep.IncrSessions()

	return sessionID, ep, nil
}

func (h *Handler) pushAudio(sessionID string, ep *engine.Endpoint, audioData []byte) (*pb.PushAudioResponse, error) {
	h.sessionMgr.Get(sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return ep.Client().PushAudio(ctx, &pb.PushAudioRequest{
		SessionId: sessionID,
		AudioData: audioData,
	})
}

func (h *Handler) finishSession(sessionID string, ep *engine.Endpoint) (*pb.FinishSessionResponse, error) {
	defer func() {
		h.sessionMgr.Remove(sessionID)
		ep.DecrSessions()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return ep.Client().FinishSession(ctx, &pb.FinishSessionRequest{
		SessionId: sessionID,
	})
}

func (h *Handler) destroySession(sessionID string, ep *engine.Endpoint) {
	if ep == nil {
		return
	}

	defer func() {
		h.sessionMgr.Remove(sessionID)
		ep.DecrSessions()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ep.Client().DestroySession(ctx, &pb.DestroySessionRequest{
		SessionId: sessionID,
	})
	if err != nil {
		log.Printf("[WARN] DestroySession error for %s: %v", sessionID, err)
	} else {
		log.Printf("[INFO] Session destroyed (disconnect): %s", sessionID)
	}
}

func sendJSON(mu *sync.Mutex, conn *websocket.Conn, msg ServerMessage) {
	mu.Lock()
	defer mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[ERROR] JSON marshal error: %v", err)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("[ERROR] WebSocket write error: %v", err)
	}
}

func sendError(mu *sync.Mutex, conn *websocket.Conn, code, message string) {
	sendJSON(mu, conn, ServerMessage{
		Type:    "error",
		Code:    code,
		Message: message,
	})
}

type SessionError struct {
	Message string
}

func (e *SessionError) Error() string {
	return e.Message
}

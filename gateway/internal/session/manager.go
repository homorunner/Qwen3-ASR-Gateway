package session

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"sync"
	"time"
)

type Session struct {
	ID           string
	EngineID     int
	CreatedAt    time.Time
	LastActiveAt time.Time
	Finished     bool
}

// OnExpireFunc is called for each session removed by the cleanup loop.
// It receives the removed Session so the caller can release engine-side
// resources (e.g. DestroySession gRPC call).
type OnExpireFunc func(s *Session)

type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex

	timeoutDuration    time.Duration
	maxDurationTimeout time.Duration

	onExpire OnExpireFunc

	stopCh chan struct{}
	wg     sync.WaitGroup
}

func NewManager(timeoutSec, maxDurationSec int, onExpire OnExpireFunc) *Manager {
	return &Manager{
		sessions:           make(map[string]*Session),
		timeoutDuration:    time.Duration(timeoutSec) * time.Second,
		maxDurationTimeout: time.Duration(maxDurationSec) * time.Second,
		onExpire:           onExpire,
		stopCh:             make(chan struct{}),
	}
}

func (m *Manager) Start() {
	m.wg.Add(1)
	go m.cleanupLoop()
	log.Printf("[INFO] Session manager started (timeout=%v, max_duration=%v)",
		m.timeoutDuration, m.maxDurationTimeout)
}

func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
	log.Println("[INFO] Session manager stopped")
}

func GenerateID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) Create(sessionID string, engineID int) *Session {
	now := time.Now()
	s := &Session{
		ID:           sessionID,
		EngineID:     engineID,
		CreatedAt:    now,
		LastActiveAt: now,
	}

	m.mu.Lock()
	m.sessions[sessionID] = s
	m.mu.Unlock()

	return s
}

func (m *Manager) Get(sessionID string) *Session {
	m.mu.RLock()
	s, ok := m.sessions[sessionID]
	m.mu.RUnlock()

	if !ok {
		return nil
	}

	m.mu.Lock()
	s.LastActiveAt = time.Now()
	m.mu.Unlock()

	return s
}

func (m *Manager) Remove(sessionID string) *Session {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if ok {
		delete(m.sessions, sessionID)
	}
	m.mu.Unlock()

	if ok {
		return s
	}
	return nil
}

func (m *Manager) ExpiredSessions() []string {
	now := time.Now()
	var expired []string

	m.mu.RLock()
	for id, s := range m.sessions {
		idleTimeout := now.Sub(s.LastActiveAt) > m.timeoutDuration
		maxTimeout := now.Sub(s.CreatedAt) > m.maxDurationTimeout
		if idleTimeout || maxTimeout {
			expired = append(expired, id)
		}
	}
	m.mu.RUnlock()

	return expired
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

func (m *Manager) cleanupLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			expired := m.ExpiredSessions()
			if len(expired) == 0 {
				continue
			}

			log.Printf("[INFO] Cleaning up %d expired sessions", len(expired))
			for _, id := range expired {
				s := m.Remove(id)
				if s != nil && m.onExpire != nil {
					m.onExpire(s)
				}
			}
		}
	}
}

package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gateway/internal/config"
	"gateway/internal/engine"
	"gateway/internal/session"
	"gateway/internal/ws"
)

func main() {
	configPath := flag.String("config", "", "Path to config.yaml (optional; uses defaults if not set)")
	flag.Parse()

	var cfg *config.Config
	if *configPath != "" {
		var err error
		cfg, err = config.LoadFromFile(*configPath)
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
		log.Printf("[INFO] Loaded config from %s", *configPath)
	} else {
		cfg = config.DefaultConfig()
		log.Println("[INFO] Using default config (8 engines on localhost:50050-50057)")
	}

	engineConfigs := make([]struct {
		ID   int
		Addr string
	}, len(cfg.Engines))
	for i, e := range cfg.Engines {
		engineConfigs[i] = struct {
			ID   int
			Addr string
		}{ID: e.ID, Addr: e.Addr}
	}

	pool, err := engine.NewPool(
		engineConfigs,
		time.Duration(cfg.HealthCheck.IntervalSec)*time.Second,
		cfg.HealthCheck.FailureThreshold,
	)
	if err != nil {
		log.Fatalf("Failed to create engine pool: %v", err)
	}
	pool.Start()
	defer pool.Stop()

	sessionMgr := session.NewManager(cfg.Session.TimeoutSec, cfg.Session.MaxDurationSec)
	sessionMgr.Start()
	defer sessionMgr.Stop()

	wsHandler := ws.NewHandler(pool, sessionMgr)

	mux := http.NewServeMux()
	mux.Handle(cfg.Server.WebSocketPath, wsHandler)

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","active_sessions":%d}`, sessionMgr.Count())
	})

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  0,
		WriteTimeout: 0,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("[INFO] Received signal %v, shutting down...", sig)
		server.Close()
	}()

	log.Printf("[INFO] Gateway starting on %s (WebSocket: %s)", addr, cfg.Server.WebSocketPath)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}

	log.Println("[INFO] Gateway shut down")
}

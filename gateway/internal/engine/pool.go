package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	pb "gateway/proto/qwen_asr"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	ErrNoHealthyEngine = errors.New("no healthy engine available")
)

type Endpoint struct {
	ID   int
	Addr string

	conn   *grpc.ClientConn
	client pb.ASREngineClient

	activeSessions int32
	healthy        int32
	failCount      int32

	mu sync.Mutex
}

type Pool struct {
	endpoints        []*Endpoint
	failureThreshold int
	healthInterval   time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewPool(configs []struct {
	ID   int
	Addr string
}, healthInterval time.Duration, failureThreshold int) (*Pool, error) {
	ctx, cancel := context.WithCancel(context.Background())

	pool := &Pool{
		endpoints:        make([]*Endpoint, 0, len(configs)),
		failureThreshold: failureThreshold,
		healthInterval:   healthInterval,
		ctx:              ctx,
		cancel:           cancel,
	}

	for _, cfg := range configs {
		ep, err := newEndpoint(cfg.ID, cfg.Addr)
		if err != nil {
			log.Printf("[WARN] Failed to connect to engine %d at %s: %v", cfg.ID, cfg.Addr, err)
			ep = &Endpoint{
				ID:   cfg.ID,
				Addr: cfg.Addr,
			}
			atomic.StoreInt32(&ep.healthy, 0)
		}
		pool.endpoints = append(pool.endpoints, ep)
	}

	return pool, nil
}

func newEndpoint(id int, addr string) (*Endpoint, error) {
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(64*1024*1024),
			grpc.MaxCallSendMsgSize(64*1024*1024),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", addr, err)
	}

	client := pb.NewASREngineClient(conn)

	ep := &Endpoint{
		ID:     id,
		Addr:   addr,
		conn:   conn,
		client: client,
	}
	atomic.StoreInt32(&ep.healthy, 1)

	return ep, nil
}

func (p *Pool) Start() {
	p.wg.Add(1)
	go p.healthCheckLoop()
	log.Printf("[INFO] Engine pool started with %d endpoints, health check every %v",
		len(p.endpoints), p.healthInterval)
}

func (p *Pool) Stop() {
	p.cancel()
	p.wg.Wait()

	for _, ep := range p.endpoints {
		if ep.conn != nil {
			ep.conn.Close()
		}
	}
	log.Println("[INFO] Engine pool stopped")
}

func (p *Pool) PickEngine() (*Endpoint, error) {
	var best *Endpoint
	minSessions := int32(math.MaxInt32)

	for _, ep := range p.endpoints {
		if atomic.LoadInt32(&ep.healthy) == 0 {
			continue
		}
		n := atomic.LoadInt32(&ep.activeSessions)
		if n < minSessions {
			minSessions = n
			best = ep
		}
	}

	if best == nil {
		return nil, ErrNoHealthyEngine
	}
	return best, nil
}

func (p *Pool) GetEndpoint(id int) *Endpoint {
	for _, ep := range p.endpoints {
		if ep.ID == id {
			return ep
		}
	}
	return nil
}

func (ep *Endpoint) Client() pb.ASREngineClient {
	ep.mu.Lock()
	defer ep.mu.Unlock()
	return ep.client
}

func (ep *Endpoint) IncrSessions() {
	atomic.AddInt32(&ep.activeSessions, 1)
}

func (ep *Endpoint) DecrSessions() {
	atomic.AddInt32(&ep.activeSessions, -1)
}

func (ep *Endpoint) ActiveSessionCount() int32 {
	return atomic.LoadInt32(&ep.activeSessions)
}

func (ep *Endpoint) IsHealthy() bool {
	return atomic.LoadInt32(&ep.healthy) == 1
}

func (p *Pool) healthCheckLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.healthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.doHealthCheck()
		}
	}
}

func (p *Pool) doHealthCheck() {
	for _, ep := range p.endpoints {
		ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)

		client := ep.Client()
		if client == nil {
			// Try to reconnect
			newEp, err := newEndpoint(ep.ID, ep.Addr)
			if err != nil {
				atomic.AddInt32(&ep.failCount, 1)
				if atomic.LoadInt32(&ep.failCount) >= int32(p.failureThreshold) {
					if atomic.CompareAndSwapInt32(&ep.healthy, 1, 0) {
						log.Printf("[WARN] Engine %d (%s) marked unhealthy", ep.ID, ep.Addr)
					}
				}
				cancel()
				continue
			}
			ep.mu.Lock()
			ep.conn = newEp.conn
			ep.client = newEp.client
			ep.mu.Unlock()
			client = newEp.client
		}

		resp, err := client.HealthCheck(ctx, &pb.HealthCheckRequest{})
		cancel()

		if err != nil {
			count := atomic.AddInt32(&ep.failCount, 1)
			if count >= int32(p.failureThreshold) {
				if atomic.CompareAndSwapInt32(&ep.healthy, 1, 0) {
					log.Printf("[WARN] Engine %d (%s) marked unhealthy after %d failures",
						ep.ID, ep.Addr, count)
				}
			}
			continue
		}

		// Health check succeeded
		atomic.StoreInt32(&ep.failCount, 0)
		if atomic.CompareAndSwapInt32(&ep.healthy, 0, 1) {
			log.Printf("[INFO] Engine %d (%s) recovered, marked healthy", ep.ID, ep.Addr)
		}

		// Sync session count from engine (if needed)
		if resp.ModelLoaded {
			// Update local counter if it drifted
			_ = resp.ActiveSessions
		}
	}
}

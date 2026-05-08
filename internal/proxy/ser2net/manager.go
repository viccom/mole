package ser2net

import (
	"context"
	"fmt"
	"log"
	"sync"
)

type TunnelConfig struct {
	Type   string
	Config Ser2NetConfig
}

type Manager struct {
	mu      sync.RWMutex
	tunnels map[string]*Handler
	errors  map[string]Stats
	ctx     context.Context
	cancel  context.CancelFunc
}

func NewManager(ctx context.Context) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	return &Manager{
		tunnels: make(map[string]*Handler),
		errors:  make(map[string]Stats),
		ctx:     ctx,
		cancel:  cancel,
	}
}

func (m *Manager) Close() {
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.tunnels {
		h.Stop()
	}
}

func (m *Manager) Status(name string) (Stats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if h, ok := m.tunnels[name]; ok {
		return h.Stats(), nil
	}
	if s, ok := m.errors[name]; ok {
		return s, nil
	}
	return Stats{}, fmt.Errorf("ser2net tunnel %q not found", name)
}

func (m *Manager) List() []Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Stats, 0, len(m.tunnels)+len(m.errors))
	for _, h := range m.tunnels {
		result = append(result, h.Stats())
	}
	for _, s := range m.errors {
		result = append(result, s)
	}
	return result
}

func (m *Manager) OnTunnelUpdate(configs map[string]TunnelConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop removed tunnels
	for name, h := range m.tunnels {
		if _, ok := configs[name]; !ok {
			log.Printf("ser2net: stopping removed tunnel %s", name)
			h.Stop()
			delete(m.tunnels, name)
			delete(m.errors, name)
		}
	}

	// Start or restart tunnels
	for name, tc := range configs {
		cfg := tc.Config
		if !cfg.Enable {
			if h, ok := m.tunnels[name]; ok {
				h.Stop()
				delete(m.tunnels, name)
			}
			delete(m.errors, name)
			continue
		}

		if existing, ok := m.tunnels[name]; ok {
			if existing.cfg == cfg && existing.typ == tc.Type && existing.IsRunning() {
				continue
			}
			log.Printf("ser2net: restarting tunnel %s", name)
			existing.Stop()
			delete(m.tunnels, name)
			delete(m.errors, name)
		}

		handler, err := NewHandler(name, tc.Type, cfg)
		if err != nil {
			log.Printf("ser2net: create handler %s error: %v", name, err)
			m.errors[name] = Stats{Name: name, Type: tc.Type, Mode: cfg.Mode, Address: cfg.Address, Error: err.Error()}
			continue
		}

		if err := handler.Start(m.ctx); err != nil {
			log.Printf("ser2net: start tunnel %s error: %v", name, err)
			m.errors[name] = Stats{Name: name, Type: tc.Type, Mode: cfg.Mode, Address: cfg.Address, Error: err.Error()}
			continue
		}

		delete(m.errors, name)
		m.tunnels[name] = handler
		log.Printf("ser2net: started tunnel %s (%s/%s) on %s", name, tc.Type, cfg.Mode, cfg.Address)
	}
}

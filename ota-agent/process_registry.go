package main

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

type processRegistry struct {
	mu       sync.Mutex
	byID     map[string]*ProcessManager
	lastSpec map[string]ManagedProcessConfig
	logger   *Logger
}

func newProcessRegistry(logger *Logger) *processRegistry {
	return &processRegistry{
		byID:     make(map[string]*ProcessManager),
		lastSpec: make(map[string]ManagedProcessConfig),
		logger:   logger,
	}
}

func procSpecEqual(a, b ManagedProcessConfig) bool {
	return a.Executable == b.Executable &&
		a.Enabled == b.Enabled &&
		a.WorkDir == b.WorkDir &&
		reflect.DeepEqual(a.Args, b.Args)
}

// Sync applies cfg.Processes: stops removed/disabled, starts or restarts when spec changes.
func (r *processRegistry) Sync(cfg *AgentConfig) error {
	if r == nil || cfg == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	wantIDs := map[string]struct{}{}
	for _, p := range cfg.Processes {
		wantIDs[p.ID] = struct{}{}
	}

	for id, pm := range r.byID {
		if _, ok := wantIDs[id]; !ok {
			_ = pm.Stop()
			delete(r.byID, id)
			delete(r.lastSpec, id)
		}
	}

	for _, p := range cfg.Processes {
		id := p.ID
		if !p.Enabled {
			if pm, ok := r.byID[id]; ok {
				_ = pm.Stop()
				delete(r.byID, id)
				delete(r.lastSpec, id)
			}
			continue
		}

		exe := p.Executable
		if exe == "" {
			continue
		}

		last, had := r.lastSpec[id]
		pm := r.byID[id]
		if had && procSpecEqual(last, p) && pm != nil && pm.IsRunning() {
			continue
		}

		if pm != nil {
			_ = pm.Stop()
			delete(r.byID, id)
		}

		npm := NewProcessManagerFromSpec(exe, p.Args, p.WorkDir, r.logger)
		if err := npm.Start(); err != nil {
			return fmt.Errorf("start process %s: %w", id, err)
		}
		r.byID[id] = npm
		cp := p
		cp.Args = append([]string(nil), p.Args...)
		r.lastSpec[id] = cp
	}
	return nil
}

// Restart stops the managed child for id and starts a fresh instance from last known spec (same YAML entry must be enabled and running under registry).
func (r *processRegistry) Restart(id string) error {
	if r == nil {
		return fmt.Errorf("no process registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pm, ok := r.byID[id]
	spec, had := r.lastSpec[id]
	if !ok || !had || pm == nil {
		return fmt.Errorf("process %q is not under management (save config with enabled process first)", id)
	}
	if !spec.Enabled || strings.TrimSpace(spec.Executable) == "" {
		return fmt.Errorf("process %q is disabled or has no executable", id)
	}
	_ = pm.Stop()
	delete(r.byID, id)
	delete(r.lastSpec, id)

	npm := NewProcessManagerFromSpec(spec.Executable, spec.Args, spec.WorkDir, r.logger)
	if err := npm.Start(); err != nil {
		return fmt.Errorf("start process %q: %w", id, err)
	}
	r.byID[id] = npm
	cp := spec
	cp.Args = append([]string(nil), spec.Args...)
	r.lastSpec[id] = cp
	return nil
}

func (r *processRegistry) StopAll() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, pm := range r.byID {
		_ = pm.Stop()
		delete(r.byID, id)
		delete(r.lastSpec, id)
		_ = id
	}
}

func (r *processRegistry) Status() map[string]processStatusDTO {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]processStatusDTO)
	for id, pm := range r.byID {
		out[id] = processStatusDTO{
			Running: pm.IsRunning(),
			PID:     pm.GetPID(),
		}
	}
	return out
}

type processStatusDTO struct {
	Running bool `json:"running"`
	PID     int  `json:"pid"`
}

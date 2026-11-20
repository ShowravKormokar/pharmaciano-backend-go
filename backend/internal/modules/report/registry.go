// Package registry holds versioned report definitions. Definitions are
// code-owned, immutable contracts; changing semantics requires a new version.
// See REPORT_CONTRACT §3.
package report

import (
	"errors"
	"fmt"
	"sync"
)

// ReportDefinition is a versioned, code-owned report contract.
type ReportDefinition struct {
	Code              string
	Version           int
	Title             string
	Description       string
	RequiredPerms     []string            // report-level permissions (e.g. reports:export)
	SourcePerms       map[string][]string // source domain -> required source permissions
	Formats           []string
	MaxRows           int
	MaxBytes          int64
	MaxRuntimeSeconds int
	RetentionDays     int
	ConsistencyMode   string
	DateRange         bool // true when the definition accepts from/to filters
	BranchFilter      bool // true when branch/warehouse selectors are supported
	PII               bool
	Financial         bool
	Cost              bool
	Audit             bool
	StaffActivity     bool
	RendererVersion   string
}

// Registry is the in-memory catalogue of registered report definitions.
type Registry struct {
	mu       sync.RWMutex
	latest   map[string]*ReportDefinition         // code -> highest version
	versions map[string]map[int]*ReportDefinition // code -> version -> def
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		latest:   make(map[string]*ReportDefinition),
		versions: make(map[string]map[int]*ReportDefinition),
	}
}

// Register adds a definition. The same (code, version) may not be registered
// twice; released definition semantics are immutable.
func (r *Registry) Register(def ReportDefinition) error {
	if def.Code == "" {
		return errors.New("report: definition code is required")
	}
	if def.Version < 1 {
		return errors.New("report: definition version must be >= 1")
	}
	if len(def.Formats) == 0 {
		return errors.New("report: definition must support at least one format")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.versions[def.Code] == nil {
		r.versions[def.Code] = make(map[int]*ReportDefinition)
	}
	if _, exists := r.versions[def.Code][def.Version]; exists {
		return fmt.Errorf("report: definition %q version %d already registered", def.Code, def.Version)
	}
	cp := def
	r.versions[def.Code][def.Version] = &cp
	if cur, ok := r.latest[def.Code]; !ok || def.Version > cur.Version {
		r.latest[def.Code] = &cp
	}
	return nil
}

// Definition returns the definition for code/version, or false if absent.
func (r *Registry) Definition(code string, version int) (*ReportDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	vers, ok := r.versions[code]
	if !ok {
		return nil, false
	}
	def, ok := vers[version]
	return def, ok
}

// Latest returns the highest registered version for code, or false if absent.
func (r *Registry) Latest(code string) (*ReportDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.latest[code]
	return def, ok
}

// Codes returns all registered report codes.
func (r *Registry) Codes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.latest))
	for code := range r.latest {
		out = append(out, code)
	}
	return out
}

// MustRegister panics on error; handy for package-level registration.
func (r *Registry) MustRegister(def ReportDefinition) {
	if err := r.Register(def); err != nil {
		panic(err)
	}
}

package box

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Adapter is a pluggable data source for box.
// Each adapter handles its own monitor cycle and query types.
type Adapter interface {
	// ID returns the unique adapter identifier (e.g. "example_tool").
	ID() string

	// QueryTypes returns the list of supported query type names.
	QueryTypes() []string

	// Query executes an on-demand query and returns the result.
	// params is a JSON-encoded query string (may be empty).
	Query(queryType string, params json.RawMessage) (any, error)
}

type AdapterInfo struct {
	ID          string   `json:"id"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	QueryTypes  []string `json:"query_types"`
	Kind        string   `json:"kind,omitempty"`
}

// Provider supplies adapters dynamically. It is used for hot-updated box
// definitions loaded from configuration files.
type Provider interface {
	List() []AdapterInfo
	Get(id string) (Adapter, bool)
}

// Registry holds all registered adapters.
type Registry struct {
	mu        sync.RWMutex
	adapters  map[string]Adapter
	providers []Provider
}

func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]Adapter)}
}

func (r *Registry) Register(a Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[a.ID()] = a
}

func (r *Registry) RegisterProvider(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
}

func (r *Registry) Get(id string) (Adapter, bool) {
	r.mu.RLock()
	a, ok := r.adapters[id]
	providers := append([]Provider(nil), r.providers...)
	r.mu.RUnlock()
	if ok {
		return a, true
	}
	for _, provider := range providers {
		if adapter, ok := provider.Get(id); ok {
			return adapter, true
		}
	}
	return nil, false
}

func (r *Registry) List() []AdapterInfo {
	r.mu.RLock()
	providers := append([]Provider(nil), r.providers...)
	out := make([]AdapterInfo, 0, len(r.adapters))
	for _, a := range r.adapters {
		out = append(out, AdapterInfo{
			ID:         a.ID(),
			QueryTypes: a.QueryTypes(),
		})
	}
	r.mu.RUnlock()
	for _, provider := range providers {
		out = append(out, provider.List()...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Query dispatches a query to the named adapter.
func (r *Registry) Query(adapterID, queryType string, params json.RawMessage) (any, error) {
	a, ok := r.Get(adapterID)
	if !ok {
		return nil, fmt.Errorf("adapter %q not found", adapterID)
	}
	return a.Query(queryType, params)
}

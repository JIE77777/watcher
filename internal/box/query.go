package box

import (
	"encoding/json"
	"io"
	"net/http"
)

// HandleQuery returns an http.Handler that routes queries to registered adapters.
// Route pattern: GET /api/v2/box/query/{adapter_id}/{query_type}
func HandleQuery(registry *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		adapterID := r.PathValue("adapter_id")
		queryType := r.PathValue("query_type")

		if adapterID == "" || queryType == "" {
			http.Error(w, "adapter_id and query_type required", http.StatusBadRequest)
			return
		}

		var params json.RawMessage
		if r.Method == http.MethodPost && r.Body != nil {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
				return
			}
			params = body
		} else if q := r.URL.Query().Get("params"); q != "" {
			params = json.RawMessage(q)
		}

		result, err := registry.Query(adapterID, queryType, params)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"adapter": adapterID,
			"type":    queryType,
			"result":  result,
		})
	})
}

// HandleList returns an http.Handler that lists all registered adapters.
func HandleList(registry *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		adapters := registry.List()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"adapters": adapters,
		})
	})
}

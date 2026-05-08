package main

import (
	"net/http"
)

func (a *App) handlePushStatusV2(w http.ResponseWriter, _ *http.Request) {
	if a.pushManager == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"channels": []any{},
		})
		return
	}
	writeJSON(w, http.StatusOK, a.pushManager.Status())
}

func (a *App) handlePushWebSocketV2(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error": "service websocket push endpoint is not implemented; use watcher-relay /api/v2/push/ws",
	})
}

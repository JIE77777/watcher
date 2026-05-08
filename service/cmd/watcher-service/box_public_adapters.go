package main

import (
	"path/filepath"

	"watcher/internal/box"
)

func (a *App) registerPrivateBoxAdapters(cfg Config) {
	boxRoot := filepath.Join(cfg.Shell.ComponentsRoot, "box")
	a.boxRegistry.RegisterProvider(box.NewCatalogProvider([]string{
		filepath.Join(boxRoot, "examples"),
	}, nil))
}

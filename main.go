package main

import (
	"log/slog"
	"os"

	"crusoe-registry-token-issuer/internal/issuer"
	"crusoe-registry-token-issuer/internal/logging"
)

func main() {
	logging.InitializeDefault()
	if err := issuer.Serve(); err != nil {
		slog.Error("could not serve", "error", err)
		os.Exit(1)
	}
}

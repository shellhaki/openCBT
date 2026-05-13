package main

import (
	"context"
	"log"

	"github.com/shellhaki/openCBT/config"
	"github.com/shellhaki/openCBT/internal/api"
)

func main() {
	cfg := config.Load()

	config.ConnDB()
	defer config.DB.Close()

	server := api.NewServer(config.DB, cfg)
	server.StartExpiryWatcher(context.Background())

	if err := server.Routes().Run(":" + cfg.Port); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

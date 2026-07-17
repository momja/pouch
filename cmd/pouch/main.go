package main

import (
	"log"

	"github.com/momja/pouch/internal/server"
)

func main() {
	cfg, err := server.ConfigFromEnv()
	if err != nil {
		log.Fatalf("pouch: %v", err)
	}
	srv := server.New(cfg)
	log.Printf("pouch listening on %s (wallabag: %s)", cfg.Addr, cfg.WallabagURL)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

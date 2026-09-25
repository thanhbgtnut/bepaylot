// Command server runs bepilot: the HTTP API, the task workers, or both
// (-role / workers.role / BEPILOT_ROLE = api | worker | all).
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/thanhenti/bepaylot/docs" // generated OpenAPI docs (make swag)
	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/container"
	"github.com/thanhenti/bepaylot/internal/logging"
)

// @title                       bepilot API
// @version                     1.0.0
// @description                 A streaming AI agent backend with document processing. `/v1/messages` mirrors the Anthropic Messages API (with additive `provider` and `metadata.session_id` / `metadata.kb_ids` fields); `/v1/sessions` manages conversations; `/v1/kbs`, `/v1/documents` and `/v1/search` parse files (OCR + PDF text layer) and search them without embeddings; `/v1/graph` and `/v1/kbs/{id}/wiki` expose the extracted knowledge graph.
// @BasePath                    /
// @schemes                     http https
// @securityDefinitions.apikey  ApiKeyAuth
// @in                          header
// @name                        x-api-key
// @description                 An API key issued by `cmd/seed`. `Authorization: Bearer <key>` is also accepted.

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "path to config file")
	migrateOnly := flag.Bool("migrate-only", false, "run migrations then exit")
	role := flag.String("role", "", "api | worker | all (overrides workers.role)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		panic(err)
	}
	if *role != "" {
		cfg.Workers.Role = *role
	}
	log := logging.New(cfg.Log)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *migrateOnly {
		if err := postgres.Migrate(cfg.DB.DSN); err != nil {
			log.Error("migration failed", "err", err)
			os.Exit(1)
		}
		log.Info("migrations applied")
		return
	}

	app, err := container.Build(ctx, cfg, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	if err := app.Run(ctx); err != nil {
		log.Error("run failed", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

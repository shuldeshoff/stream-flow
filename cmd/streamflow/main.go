package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sul/streamflow/internal/config"
	"github.com/sul/streamflow/internal/ingestion"
	"github.com/sul/streamflow/internal/metrics"
	"github.com/sul/streamflow/internal/processor"
	"github.com/sul/streamflow/internal/storage"
)

func main() {
	// Настройка логирования
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	log.Info().Msg("Starting StreamFlow...")

	// Загрузка конфигурации
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	log.Info().
		Int("http_port", cfg.Server.Port).
		Int("worker_count", cfg.Processor.WorkerCount).
		Str("clickhouse_addr", cfg.ClickHouse.Address).
		Msg("Configuration loaded")

	// Инициализация метрик
	metricsServer := metrics.NewServer(cfg.Metrics.Port)
	go func() {
		if err := metricsServer.Start(); err != nil {
			log.Error().Err(err).Msg("Metrics server failed")
		}
	}()

	// Инициализация хранилища
	store, err := storage.NewClickHouseStorage(cfg.ClickHouse)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize storage")
	}
	defer store.Close()

	log.Info().Msg("Storage initialized")

	// Инициализация процессора событий
	proc := processor.NewEventProcessor(cfg.Processor, store)
	proc.Start()
	defer proc.Stop()

	log.Info().Int("workers", cfg.Processor.WorkerCount).Msg("Event processor started")

	// Инициализация HTTP сервера для приема событий
	server := ingestion.NewHTTPServer(cfg.Server, proc)
	go func() {
		if err := server.Start(); err != nil {
			log.Fatal().Err(err).Msg("HTTP server failed")
		}
	}()

	log.Info().Int("port", cfg.Server.Port).Msg("HTTP ingestion server started")

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Info().Msg("Shutting down gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("HTTP server shutdown failed")
	}

	log.Info().Msg("StreamFlow stopped")
	fmt.Println("\n✨ Thanks for using StreamFlow!")
}


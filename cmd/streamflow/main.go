package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sul/streamflow/internal/cache"
	"github.com/sul/streamflow/internal/config"
	"github.com/sul/streamflow/internal/grpcserver"
	"github.com/sul/streamflow/internal/ingestion"
	"github.com/sul/streamflow/internal/banking"
	"github.com/sul/streamflow/internal/metrics"
	"github.com/sul/streamflow/internal/processor"
	"github.com/sul/streamflow/internal/query"
	"github.com/sul/streamflow/internal/ratelimit"
	"github.com/sul/streamflow/internal/storage"
	"github.com/sul/streamflow/internal/websocket"
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
		Int("grpc_port", cfg.GRPC.Port).
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

	// Инициализация Redis кэша
	redisCache, err := cache.NewRedisCache(cfg.Redis.Address, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to initialize Redis cache, continuing without cache")
		redisCache = nil
	}
	if redisCache != nil {
		defer redisCache.Close()
		log.Info().Msg("Redis cache initialized")
	}

	// Инициализация процессора событий
	proc := processor.NewEventProcessor(cfg.Processor, store, redisCache)
	proc.Start()
	defer proc.Stop()

	log.Info().Int("workers", cfg.Processor.WorkerCount).Msg("Event processor started")

	// Инициализация rate limiter
	var rateLimiter *ratelimit.RateLimiter
	if cfg.RateLimit.Enabled {
		rateLimiter = ratelimit.NewRateLimiter(redisCache, cfg.RateLimit.RPS, cfg.RateLimit.Burst)
		defer rateLimiter.Stop()
		log.Info().
			Int("rps", cfg.RateLimit.RPS).
			Int("burst", cfg.RateLimit.Burst).
			Msg("Rate limiter enabled")
	}

	// Инициализация HTTP сервера для приема событий
	httpServer := ingestion.NewHTTPServer(cfg.Server, proc, rateLimiter)
	go func() {
		if err := httpServer.Start(); err != nil {
			log.Fatal().Err(err).Msg("HTTP server failed")
		}
	}()

	log.Info().Int("port", cfg.Server.Port).Msg("HTTP ingestion server started")

	// Инициализация gRPC сервера
	grpcSrv := grpcserver.NewGRPCServer(cfg.GRPC.Port, proc)
	go func() {
		if err := grpcSrv.Start(); err != nil {
			log.Fatal().Err(err).Msg("gRPC server failed")
		}
	}()

	log.Info().Int("port", cfg.GRPC.Port).Msg("gRPC server started")

	// Инициализация WebSocket сервера
	wsServer := websocket.NewWebSocketServer(redisCache)
	defer wsServer.Stop()
	
	// Добавляем WebSocket endpoint к HTTP серверу
	http.HandleFunc("/ws", wsServer.HandleWebSocket)
	go func() {
		addr := fmt.Sprintf(":%d", cfg.Server.Port+2) // WebSocket на порту +2
		log.Info().Str("address", addr).Msg("WebSocket server started")
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Error().Err(err).Msg("WebSocket server failed")
		}
	}()

	// Инициализация Query API сервера
	if redisCache != nil {
		queryServer := query.NewQueryServer(cfg.Server, store, redisCache)
		go func() {
			if err := queryServer.Start(); err != nil {
				log.Error().Err(err).Msg("Query API server failed")
			}
		}()

		log.Info().Int("port", cfg.Server.Port+1).Msg("Query API server started")
	}

	// Инициализация Banking API (порт 8083)
	bankingAPI := banking.NewBankingAPI(8083, proc, redisCache)
	go func() {
		if err := bankingAPI.Start(); err != nil {
			log.Error().Err(err).Msg("Banking API server failed")
		}
	}()

	log.Info().Int("port", 8083).Msg("Banking API server started")

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Info().Msg("Shutting down gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("HTTP server shutdown failed")
	}

	grpcSrv.Stop()

	log.Info().Msg("StreamFlow stopped")
	fmt.Println("\n✨ Thanks for using StreamFlow!")
}


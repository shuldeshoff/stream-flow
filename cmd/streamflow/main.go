package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/shuldeshoff/stream-flow/internal/banking"
	"github.com/shuldeshoff/stream-flow/internal/cache"
	"github.com/shuldeshoff/stream-flow/internal/config"
	"github.com/shuldeshoff/stream-flow/internal/grpcserver"
	"github.com/shuldeshoff/stream-flow/internal/ingestion"
	"github.com/shuldeshoff/stream-flow/internal/metrics"
	"github.com/shuldeshoff/stream-flow/internal/processor"
	"github.com/shuldeshoff/stream-flow/internal/query"
	"github.com/shuldeshoff/stream-flow/internal/ratelimit"
	"github.com/shuldeshoff/stream-flow/internal/security"
	"github.com/shuldeshoff/stream-flow/internal/storage"
	"github.com/shuldeshoff/stream-flow/internal/websocket"
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
		Bool("tls_enabled", cfg.TLS.Enabled).
		Bool("jwt_enabled", cfg.JWT.Enabled).
		Msg("Configuration loaded")

	// Инициализация TLS
	var tlsConfig *tls.Config
	if cfg.TLS.Enabled {
		var err error
		tlsConfig, err = security.LoadTLSConfig(cfg.TLS.CertFile, cfg.TLS.KeyFile, cfg.TLS.CAFile)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to load TLS configuration")
		}
		log.Info().Msg("TLS configuration loaded")
	} else {
		log.Warn().Msg("TLS is DISABLED - not recommended for production!")
	}

	// Инициализация JWT Manager
	var jwtManager *security.JWTManager
	if cfg.JWT.Enabled {
		if cfg.JWT.Secret == "" {
			log.Fatal().Msg("JWT_SECRET is required when JWT is enabled")
		}
		var err error
		jwtManager, err = security.NewJWTManager(cfg.JWT.Secret, cfg.JWT.Expiration, cfg.JWT.Issuer)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to initialize JWT manager")
		}
		log.Info().Msg("JWT authentication enabled")
	}

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
	httpServer := ingestion.NewHTTPServer(cfg.Server, tlsConfig, jwtManager, proc, rateLimiter)
	go func() {
		if err := httpServer.Start(); err != nil {
			log.Fatal().Err(err).Msg("HTTP server failed")
		}
	}()

	protocol := "HTTP"
	if tlsConfig != nil {
		protocol = "HTTPS"
	}
	log.Info().Str("protocol", protocol).Int("port", cfg.Server.Port).Msg("Ingestion server started")

	// Инициализация gRPC сервера
	grpcSrv := grpcserver.NewGRPCServer(cfg.GRPC.Port, proc)
	go func() {
		if err := grpcSrv.Start(); err != nil {
			log.Fatal().Err(err).Msg("gRPC server failed")
		}
	}()

	log.Info().Int("port", cfg.GRPC.Port).Msg("gRPC server started")

	// Инициализация Auth API если JWT включен
	if jwtManager != nil {
		authAPI := security.NewAuthAPI(jwtManager)
		authMux := http.NewServeMux()
		authAPI.RegisterRoutes(authMux)

		go func() {
			authPort := cfg.Server.Port + 3 // Auth API на порту +3
			log.Info().Int("port", authPort).Msg("Auth API server started")
			if err := http.ListenAndServe(fmt.Sprintf(":%d", authPort), authMux); err != nil {
				log.Error().Err(err).Msg("Auth API server failed")
			}
		}()
	}

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

	// Инициализация Banking API (порт +4 от основного, по умолчанию 8084)
	bankingPort := cfg.Server.Port + 4
	bankingAPI := banking.NewBankingAPI(bankingPort, proc, redisCache)
	go func() {
		if err := bankingAPI.Start(); err != nil {
			log.Error().Err(err).Msg("Banking API server failed")
		}
	}()

	log.Info().Int("port", bankingPort).Msg("Banking API server started")

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


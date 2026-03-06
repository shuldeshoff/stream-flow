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
	"github.com/shuldeshoff/stream-flow/internal/features"
	"github.com/shuldeshoff/stream-flow/internal/fraud"
	"github.com/shuldeshoff/stream-flow/internal/grpcserver"
	kfk "github.com/shuldeshoff/stream-flow/internal/kafka"
	"github.com/shuldeshoff/stream-flow/internal/ingestion"
	"github.com/shuldeshoff/stream-flow/internal/metrics"
	"github.com/shuldeshoff/stream-flow/internal/models"
	"github.com/shuldeshoff/stream-flow/internal/processor"
	"github.com/shuldeshoff/stream-flow/internal/query"
	"github.com/shuldeshoff/stream-flow/internal/ratelimit"
	"github.com/shuldeshoff/stream-flow/internal/scoring"
	"github.com/shuldeshoff/stream-flow/internal/security"
	"github.com/shuldeshoff/stream-flow/internal/storage"
	"github.com/shuldeshoff/stream-flow/internal/websocket"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	log.Info().Msg("Starting StreamFlow...")

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
		Bool("kafka_enabled", cfg.Kafka.Enabled).
		Bool("fraud_enabled", cfg.Fraud.Enabled).
		Msg("Configuration loaded")

	// ── TLS ────────────────────────────────────────────────────────────────────
	var tlsConfig *tls.Config
	if cfg.TLS.Enabled {
		tlsConfig, err = security.LoadTLSConfig(cfg.TLS.CertFile, cfg.TLS.KeyFile, cfg.TLS.CAFile)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to load TLS configuration")
		}
		log.Info().Msg("TLS configuration loaded")
	} else {
		log.Warn().Msg("TLS is DISABLED — not recommended for production!")
	}

	// ── JWT ────────────────────────────────────────────────────────────────────
	var jwtManager *security.JWTManager
	if cfg.JWT.Enabled {
		if cfg.JWT.Secret == "" {
			log.Fatal().Msg("JWT_SECRET is required when JWT is enabled")
		}
		jwtManager, err = security.NewJWTManager(cfg.JWT.Secret, cfg.JWT.Expiration, cfg.JWT.Issuer)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to initialize JWT manager")
		}
		log.Info().Msg("JWT authentication enabled")
	}

	// ── Metrics ────────────────────────────────────────────────────────────────
	metricsServer := metrics.NewServer(cfg.Metrics.Port)
	go func() {
		if err := metricsServer.Start(); err != nil {
			log.Error().Err(err).Msg("Metrics server failed")
		}
	}()

	// ── Storage ────────────────────────────────────────────────────────────────
	store, err := storage.NewClickHouseStorage(cfg.ClickHouse)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize storage")
	}
	defer store.Close()
	log.Info().Msg("ClickHouse storage initialized")

	// ── Redis cache ────────────────────────────────────────────────────────────
	redisCache, err := cache.NewRedisCache(cfg.Redis.Address, cfg.Redis.Password, cfg.Redis.DB)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to initialize Redis cache, continuing without cache")
		redisCache = nil
	}
	if redisCache != nil {
		defer redisCache.Close()
		log.Info().Msg("Redis cache initialized")
	}

	// ── Feature store ──────────────────────────────────────────────────────────
	var onlineStore features.OnlineStore
	if redisCache != nil {
		onlineStore = features.NewRedisOnlineStore(redisCache.Client())
		log.Info().Msg("Online feature store (Redis) initialized")
	}

	// ── Fraud engine ───────────────────────────────────────────────────────────
	var fraudEngine *fraud.Engine
	if cfg.Fraud.Enabled && onlineStore != nil {
		blocker := fraud.NewBlocker(redisCache, time.Duration(cfg.Fraud.BlockTTLHours)*time.Hour)
		scorer := scoring.NewScorer(scoring.Thresholds{
			Alert:     cfg.Fraud.ScoreAlertThreshold,
			Review:    cfg.Fraud.ScoreReviewThreshold,
			Challenge: cfg.Fraud.ScoreChallengeThreshold,
			Decline:   cfg.Fraud.ScoreDeclineThreshold,
			Block:     1000,
		})
		fraudEngine = fraud.NewEngine(fraud.EngineConfig{
			Blocker: blocker,
			Online:  onlineStore,
			Scorer:  scorer,
		})
		log.Info().Int("rules", fraudEngine.ActiveRuleCount()).Msg("Fraud engine initialized")
	}

	// ── Event processor ────────────────────────────────────────────────────────
	proc := processor.NewEventProcessor(cfg.Processor, store, redisCache)
	proc.Start()
	defer proc.Stop()
	log.Info().Int("workers", cfg.Processor.WorkerCount).Msg("Event processor started")

	// ── Rate limiter ───────────────────────────────────────────────────────────
	var rateLimiter *ratelimit.RateLimiter
	if cfg.RateLimit.Enabled {
		rateLimiter = ratelimit.NewRateLimiter(redisCache, cfg.RateLimit.RPS, cfg.RateLimit.Burst)
		defer rateLimiter.Stop()
		log.Info().Int("rps", cfg.RateLimit.RPS).Int("burst", cfg.RateLimit.Burst).Msg("Rate limiter enabled")
	}

	// ── Kafka producer ─────────────────────────────────────────────────────────
	var kafkaProducer *kfk.Producer
	var kafkaConsumers []*kfk.Consumer

	if cfg.Kafka.Enabled {
		kafkaProducer, err = kfk.NewProducer(kfk.ProducerConfig{
			Brokers:  cfg.Kafka.Brokers,
			ClientID: cfg.Kafka.ClientID + "-producer",
		})
		if err != nil {
			log.Warn().Err(err).Msg("Kafka producer unavailable, falling back to in-process pipeline")
		} else {
			defer kafkaProducer.Close()
			log.Info().Strs("brokers", cfg.Kafka.Brokers).Msg("Kafka producer connected")

			// ── events.raw consumer → in-process pipeline ─────────────────────────
			eventsConsumer, err := kfk.NewConsumer(kfk.ConsumerConfig{
				Brokers:  cfg.Kafka.Brokers,
				GroupID:  cfg.Kafka.ClientID + "-events-processor",
				Topics:   []string{kfk.TopicEventsRaw},
				ClientID: cfg.Kafka.ClientID + "-events-consumer",
			}, func(ctx context.Context, topic string, key, value []byte) error {
				var event models.Event
				if err := kfk.Unmarshal(value, &event); err != nil {
					return err
				}
				return proc.Submit(event)
			})
			if err != nil {
				log.Warn().Err(err).Msg("Failed to create events consumer")
			} else {
				kafkaConsumers = append(kafkaConsumers, eventsConsumer)
				log.Info().Str("group", cfg.Kafka.ClientID+"-events-processor").
					Str("topic", kfk.TopicEventsRaw).Msg("Kafka consumer registered")
			}

			// ── transactions.raw consumer → fraud engine ──────────────────────────
			if fraudEngine != nil {
				txConsumer, err := kfk.NewConsumer(kfk.ConsumerConfig{
					Brokers:  cfg.Kafka.Brokers,
					GroupID:  cfg.Kafka.ClientID + "-fraud-engine",
					Topics:   []string{kfk.TopicTransactionsRaw},
					ClientID: cfg.Kafka.ClientID + "-fraud-consumer",
				}, buildFraudHandler(kafkaProducer, fraudEngine))
				if err != nil {
					log.Warn().Err(err).Msg("Failed to create fraud consumer")
				} else {
					kafkaConsumers = append(kafkaConsumers, txConsumer)
					log.Info().Str("group", cfg.Kafka.ClientID+"-fraud-engine").
						Str("topic", kfk.TopicTransactionsRaw).Msg("Kafka fraud consumer registered")
				}
			}
		}
	}

	// Start all Kafka consumers in background goroutines.
	consumerCtx, consumerCancel := context.WithCancel(context.Background())
	defer consumerCancel()

	for _, c := range kafkaConsumers {
		consumer := c
		go consumer.Run(consumerCtx)
	}

	// ── HTTP ingestion server ──────────────────────────────────────────────────
	httpServer := ingestion.NewHTTPServer(cfg.Server, tlsConfig, jwtManager, proc, rateLimiter)
	if kafkaProducer != nil {
		httpServer.SetKafkaProducer(kafkaProducer)
	}
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

	// ── gRPC server ────────────────────────────────────────────────────────────
	grpcSrv := grpcserver.NewGRPCServer(cfg.GRPC.Port, proc)
	go func() {
		if err := grpcSrv.Start(); err != nil {
			log.Fatal().Err(err).Msg("gRPC server failed")
		}
	}()
	log.Info().Int("port", cfg.GRPC.Port).Msg("gRPC server started")

	// ── Auth API ───────────────────────────────────────────────────────────────
	if jwtManager != nil {
		authAPI := security.NewAuthAPI(jwtManager)
		authMux := http.NewServeMux()
		authAPI.RegisterRoutes(authMux)
		go func() {
			authPort := cfg.Server.Port + 3
			log.Info().Int("port", authPort).Msg("Auth API server started")
			if err := http.ListenAndServe(fmt.Sprintf(":%d", authPort), authMux); err != nil {
				log.Error().Err(err).Msg("Auth API server failed")
			}
		}()
	}

	// ── WebSocket server ───────────────────────────────────────────────────────
	wsServer := websocket.NewWebSocketServer(redisCache)
	defer wsServer.Stop()
	wsMux := http.NewServeMux()
	wsMux.HandleFunc("/ws", wsServer.HandleWebSocket)
	go func() {
		addr := fmt.Sprintf(":%d", cfg.Server.Port+2)
		log.Info().Str("address", addr).Msg("WebSocket server started")
		if err := http.ListenAndServe(addr, wsMux); err != nil {
			log.Error().Err(err).Msg("WebSocket server failed")
		}
	}()

	// ── Query API ──────────────────────────────────────────────────────────────
	if redisCache != nil {
		queryServer := query.NewQueryServer(cfg.Server, store, redisCache)
		go func() {
			if err := queryServer.Start(); err != nil {
				log.Error().Err(err).Msg("Query API server failed")
			}
		}()
		log.Info().Int("port", cfg.Server.Port+1).Msg("Query API server started")
	}

	// ── Banking API ────────────────────────────────────────────────────────────
	bankingPort := cfg.Banking.Port
	if bankingPort == 0 {
		bankingPort = cfg.Server.Port + 4
	}
	bankingAPI := banking.NewBankingAPI(bankingPort, proc, redisCache)
	go func() {
		if err := bankingAPI.Start(); err != nil {
			log.Error().Err(err).Msg("Banking API server failed")
		}
	}()
	log.Info().Int("port", bankingPort).Msg("Banking API server started")

	// ── Graceful shutdown ──────────────────────────────────────────────────────
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Info().Msg("Shutting down gracefully...")
	consumerCancel()
	for _, c := range kafkaConsumers {
		c.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Error().Err(err).Msg("HTTP server shutdown failed")
	}
	grpcSrv.Stop()

	log.Info().Msg("StreamFlow stopped")
	fmt.Println("\n✨ Thanks for using StreamFlow!")
}

// buildFraudHandler returns a Kafka handler that evaluates each raw transaction
// through the fraud engine and publishes the decision to transactions.decisions.
func buildFraudHandler(producer *kfk.Producer, engine *fraud.Engine) kfk.HandlerFunc {
	return func(ctx context.Context, topic string, key, value []byte) error {
		var tx fraud.BankTransaction
		if err := kfk.Unmarshal(value, &tx); err != nil {
			// Unparse-able payload → send to DLQ.
			_ = producer.Publish(ctx, kfk.TopicTransactionsDLQ, key, map[string]string{
				"error":   err.Error(),
				"payload": string(value),
			})
			return nil // don't block offset commit for bad payloads
		}

		decision, err := engine.Evaluate(ctx, &tx)
		if err != nil {
			log.Error().Err(err).Str("tx_id", tx.TransactionID).Msg("Fraud engine error")
			return err
		}

		partKey := kfk.PartitionKey(tx.CardNumber, tx.AccountID)
		return producer.Publish(ctx, kfk.TopicTransactionsDecisions, partKey, decision)
	}
}

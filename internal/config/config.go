package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Server     ServerConfig
	GRPC       GRPCConfig
	Processor  ProcessorConfig
	ClickHouse ClickHouseConfig
	Redis      RedisConfig
	RateLimit  RateLimitConfig
	Metrics    MetricsConfig
	TLS        TLSConfig
	JWT        JWTConfig
	Kafka      KafkaConfig
	Fraud      FraudConfig
	Banking    BankingConfig
}

// KafkaConfig holds all Kafka connection and topic settings.
type KafkaConfig struct {
	// Enabled controls whether events are published to Kafka.
	// When false, the system falls back to the direct in-process pipeline.
	Enabled  bool
	Brokers  []string // comma-separated in env: KAFKA_BROKERS
	ClientID string
}

// FraudConfig tunes the multi-layer fraud engine.
type FraudConfig struct {
	// Enabled toggles the fraud engine.
	Enabled bool
	// BlockTTLHours is how long a card stays blocked (default 24h).
	BlockTTLHours int
	// ScoreAlertThreshold overrides the default 200 alert boundary.
	ScoreAlertThreshold int
	// ScoreReviewThreshold overrides the default 400 review boundary.
	ScoreReviewThreshold int
	// ScoreChallengeThreshold overrides the default 600 challenge boundary.
	ScoreChallengeThreshold int
	// ScoreDeclineThreshold overrides the default 800 decline boundary.
	ScoreDeclineThreshold int
}

// BankingConfig holds Banking API settings.
type BankingConfig struct {
	// Port is the HTTP port for the Banking API.
	// Defaults to SERVER_PORT+4 (8084 with default SERVER_PORT=8080).
	Port int
}

type ServerConfig struct {
	Port         int
	ReadTimeout  int // seconds
	WriteTimeout int // seconds
}

type GRPCConfig struct {
	Port int
}

type TLSConfig struct {
	Enabled  bool
	CertFile string
	KeyFile  string
	CAFile   string
}

type JWTConfig struct {
	Enabled    bool
	Secret     string
	Expiration int // hours
	Issuer     string
}

type ProcessorConfig struct {
	WorkerCount  int
	BufferSize   int
	BatchSize    int
	FlushTimeout int // seconds
}

type ClickHouseConfig struct {
	Address  string
	Database string
	Username string
	Password string
	Table    string
}

type RedisConfig struct {
	Address  string
	Password string
	DB       int
}

type RateLimitConfig struct {
	Enabled bool
	RPS     int // requests per second per client
	Burst   int // burst size
}

type MetricsConfig struct {
	Port int
}

func Load() (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Port:         getEnvInt("SERVER_PORT", 8080),
			ReadTimeout:  getEnvInt("SERVER_READ_TIMEOUT", 30),
			WriteTimeout: getEnvInt("SERVER_WRITE_TIMEOUT", 30),
		},
		GRPC: GRPCConfig{
			Port: getEnvInt("GRPC_PORT", 9000),
		},
		Processor: ProcessorConfig{
			WorkerCount:  getEnvInt("WORKER_COUNT", 10),
			BufferSize:   getEnvInt("BUFFER_SIZE", 10000),
			BatchSize:    getEnvInt("BATCH_SIZE", 1000),
			FlushTimeout: getEnvInt("FLUSH_TIMEOUT", 5),
		},
		ClickHouse: ClickHouseConfig{
			Address:  getEnv("CLICKHOUSE_ADDRESS", "localhost:9000"),
			Database: getEnv("CLICKHOUSE_DATABASE", "streamflow"),
			Username: getEnv("CLICKHOUSE_USERNAME", "default"),
			Password: getEnv("CLICKHOUSE_PASSWORD", ""),
			Table:    getEnv("CLICKHOUSE_TABLE", "events"),
		},
		Redis: RedisConfig{
			Address:  getEnv("REDIS_ADDRESS", "localhost:6379"),
			Password: getEnv("REDIS_PASSWORD", ""),
			DB:       getEnvInt("REDIS_DB", 0),
		},
		RateLimit: RateLimitConfig{
			Enabled: getEnvBool("RATE_LIMIT_ENABLED", true),
			RPS:     getEnvInt("RATE_LIMIT_RPS", 1000),
			Burst:   getEnvInt("RATE_LIMIT_BURST", 2000),
		},
		Metrics: MetricsConfig{
			Port: getEnvInt("METRICS_PORT", 9090),
		},
		TLS: TLSConfig{
			Enabled:  getEnvBool("TLS_ENABLED", false),
			CertFile: getEnv("TLS_CERT_FILE", "./certs/server-cert.pem"),
			KeyFile:  getEnv("TLS_KEY_FILE", "./certs/server-key.pem"),
			CAFile:   getEnv("TLS_CA_FILE", ""),
		},
		JWT: JWTConfig{
			Enabled:    getEnvBool("JWT_ENABLED", false),
			Secret:     getEnv("JWT_SECRET", ""),
			Expiration: getEnvInt("JWT_EXPIRATION_HOURS", 24),
			Issuer:     getEnv("JWT_ISSUER", "streamflow"),
		},
		Kafka: KafkaConfig{
			Enabled:  getEnvBool("KAFKA_ENABLED", false),
			Brokers:  getEnvStringSlice("KAFKA_BROKERS", []string{"localhost:9092"}),
			ClientID: getEnv("KAFKA_CLIENT_ID", "streamflow"),
		},
		Fraud: FraudConfig{
			Enabled:                 getEnvBool("FRAUD_ENABLED", true),
			BlockTTLHours:           getEnvInt("FRAUD_BLOCK_TTL_HOURS", 24),
			ScoreAlertThreshold:     getEnvInt("FRAUD_SCORE_ALERT", 200),
			ScoreReviewThreshold:    getEnvInt("FRAUD_SCORE_REVIEW", 400),
			ScoreChallengeThreshold: getEnvInt("FRAUD_SCORE_CHALLENGE", 600),
			ScoreDeclineThreshold:   getEnvInt("FRAUD_SCORE_DECLINE", 800),
		},
		Banking: BankingConfig{
			Port: getEnvInt("BANKING_PORT", 0), // 0 = use SERVER_PORT+4
		},
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	strValue := os.Getenv(key)
	if strValue == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(strValue)
	if err != nil {
		fmt.Printf("Warning: invalid value for %s, using default %d\n", key, defaultValue)
		return defaultValue
	}
	return value
}

func getEnvBool(key string, defaultValue bool) bool {
	strValue := os.Getenv(key)
	if strValue == "" {
		return defaultValue
	}
	value, err := strconv.ParseBool(strValue)
	if err != nil {
		fmt.Printf("Warning: invalid value for %s, using default %v\n", key, defaultValue)
		return defaultValue
	}
	return value
}

// getEnvStringSlice parses a comma-separated environment variable into a string slice.
func getEnvStringSlice(key string, defaultValue []string) []string {
	strValue := os.Getenv(key)
	if strValue == "" {
		return defaultValue
	}
	var result []string
	for _, s := range splitComma(strValue) {
		if s != "" {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return defaultValue
	}
	return result
}

func splitComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}


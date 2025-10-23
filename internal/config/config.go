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
	Metrics    MetricsConfig
}

type ServerConfig struct {
	Port         int
	ReadTimeout  int // seconds
	WriteTimeout int // seconds
}

type GRPCConfig struct {
	Port int
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
		Metrics: MetricsConfig{
			Port: getEnvInt("METRICS_PORT", 9090),
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


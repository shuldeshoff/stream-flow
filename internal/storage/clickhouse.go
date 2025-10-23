package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/rs/zerolog/log"
	"github.com/sul/streamflow/internal/config"
	"github.com/sul/streamflow/internal/models"
)

type Storage interface {
	WriteBatch(ctx context.Context, events []models.ProcessedEvent) error
	Close() error
}

type ClickHouseStorage struct {
	conn   driver.Conn
	config config.ClickHouseConfig
}

func NewClickHouseStorage(cfg config.ClickHouseConfig) (*ClickHouseStorage, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.Address},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
		DialTimeout: 5 * time.Second,
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ClickHouse: %w", err)
	}

	// Проверяем соединение
	if err := conn.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping ClickHouse: %w", err)
	}

	storage := &ClickHouseStorage{
		conn:   conn,
		config: cfg,
	}

	// Создаем базу данных если не существует
	if err := storage.initDatabase(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	log.Info().Str("address", cfg.Address).Str("database", cfg.Database).Msg("Connected to ClickHouse")

	return storage, nil
}

func (s *ClickHouseStorage) initDatabase() error {
	ctx := context.Background()

	// Создаем базу данных
	createDBQuery := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", s.config.Database)
	if err := s.conn.Exec(ctx, createDBQuery); err != nil {
		return fmt.Errorf("failed to create database: %w", err)
	}

	// Создаем таблицу для событий
	createTableQuery := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s.%s (
			id String,
			type String,
			source String,
			timestamp DateTime64(3),
			processed_at DateTime64(3),
			data String,
			metadata String,
			date Date DEFAULT toDate(timestamp)
		) ENGINE = MergeTree()
		PARTITION BY toYYYYMM(date)
		ORDER BY (type, source, timestamp)
		TTL date + INTERVAL 90 DAY
	`, s.config.Database, s.config.Table)

	if err := s.conn.Exec(ctx, createTableQuery); err != nil {
		return fmt.Errorf("failed to create table: %w", err)
	}

	log.Info().Str("table", s.config.Table).Msg("Database schema initialized")

	return nil
}

func (s *ClickHouseStorage) WriteBatch(ctx context.Context, events []models.ProcessedEvent) error {
	if len(events) == 0 {
		return nil
	}

	batch, err := s.conn.PrepareBatch(ctx, fmt.Sprintf("INSERT INTO %s.%s", s.config.Database, s.config.Table))
	if err != nil {
		return fmt.Errorf("failed to prepare batch: %w", err)
	}

	for _, event := range events {
		err := batch.Append(
			event.ID,
			event.Type,
			event.Source,
			event.Timestamp,
			event.ProcessedAt,
			event.Data,
			event.Metadata,
		)
		if err != nil {
			return fmt.Errorf("failed to append to batch: %w", err)
		}
	}

	if err := batch.Send(); err != nil {
		return fmt.Errorf("failed to send batch: %w", err)
	}

	log.Debug().Int("count", len(events)).Msg("Batch written to ClickHouse")

	return nil
}

func (s *ClickHouseStorage) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}


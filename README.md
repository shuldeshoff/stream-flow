# StreamFlow

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)
[![Go Report Card](https://goreportcard.com/badge/github.com/shuldeshoff/stream-flow)](https://goreportcard.com/report/github.com/shuldeshoff/stream-flow)
[![Build Status](https://github.com/shuldeshoff/stream-flow/workflows/CI/badge.svg)](https://github.com/shuldeshoff/stream-flow/actions)
[![Docker](https://img.shields.io/badge/docker-ready-blue.svg?logo=docker)](docker-compose.yml)
[![Powered by ClickHouse](https://img.shields.io/badge/Powered%20by-ClickHouse-yellow.svg?logo=clickhouse)](https://clickhouse.com/)
[![Kafka](https://img.shields.io/badge/Kafka-3.9-231F20?style=flat&logo=apachekafka)](https://kafka.apache.org/)

Distributed real-time event processing platform with a **Kafka-based streaming backbone** and a **4-layer antifraud engine** backed by a two-tier feature store.

## Описание

StreamFlow — высоконагруженная платформа для сбора, потоковой обработки и анализа событий в реальном времени на Go. Включает **Banking Edition**: antifraud-движок с feature store, конфигурируемым rule engine и числовым risk scoring.

**Версия:** 0.6.0  
**Модуль:** `github.com/shuldeshoff/stream-flow`

## Архитектура

```
HTTP/gRPC ingest
       │
       ▼
 Kafka (KRaft)          ← центральный event backbone
  ├── events.raw            consumer group: in-process pipeline
  ├── transactions.raw      consumer group: fraud-engine
  ├── transactions.decisions
  └── transactions.dlq
       │
       ▼
┌──────────────────────────────────────────┐
│  4-Layer Fraud Engine                    │
│  1. Pre-checks  (card blocklist, fields) │
│  2. Feature snapshot                     │
│     ├── Online store  (Redis ZSET)       │
│     └── Offline store (ClickHouse)       │
│  3. Rule Engine  (configurable, hot-reload) │
│  4. Scoring → Decision + reason codes   │
└──────────────────────────────────────────┘
       │
       ▼
  ClickHouse (storage) + Redis (cache/features)
       │
  Query API · WebSocket · Grafana · Prometheus
```

### Слои

| Слой | Пакет | Описание |
|---|---|---|
| Ingestion | `internal/ingestion` | HTTP/gRPC, rate limiting, JWT, Kafka publish |
| Kafka | `internal/kafka` | Producer (idempotent), Consumer group (at-least-once) |
| Feature Store | `internal/features` | Online (Redis ZSET sliding windows) + Offline (ClickHouse baselines) |
| Rule Engine | `internal/rules` | Конфигурируемые правила, hot-reload, `ValidateRules` |
| Scoring | `internal/scoring` | Risk score [0–1000], reason codes, explain lines |
| Fraud Engine | `internal/fraud` | 4-layer `Engine`; `Blocker` (Redis + in-memory) |
| Processing | `internal/processor` | Worker pool, enrichment, DLQ, ClickHouse batch writer |
| Storage | `internal/storage` | ClickHouse |
| Cache | `internal/cache` | Redis |
| Banking API | `internal/banking` | Transaction endpoint → полный antifraud pipeline |
| Query API | `internal/query` | REST aggregates из Redis/ClickHouse |
| WebSocket | `internal/websocket` | Real-time events для dashboard |
| Metrics | `internal/metrics` | Prometheus |
| Security | `internal/security` | TLS, JWT |

## Kafka topic model

| Топик | Производитель | Потребитель |
|---|---|---|
| `events.raw` | Ingestion HTTP/gRPC | `events-processor` group → worker pool |
| `transactions.raw` | Banking simulator / external | `fraud-engine` group → 4-layer engine |
| `transactions.decisions` | Fraud engine | storage-writer, websocket-broadcaster |
| `transactions.dlq` | Fraud engine (bad payload) | DLQ handler |
| `transactions.retry.1m/5m` | Retry handler | (future) |

Partition key — `card_id`, что гарантирует упорядоченную обработку событий одной карты внутри раздела — необходимо для velocity rules и stateful fraud.

## Feature Store

### Online (Redis)

Sliding-window счётчики через Redis ZSET:

| Ключ | TTL | Описание |
|---|---|---|
| `feat:card:{id}:tx_ts` | 25h | Метки времени транзакций (все окна) |
| `feat:card:{id}:amount_ts` | 25h | Суммы транзакций |
| `feat:card:{id}:merchants_1h` | 1h | Уникальные мерчанты |
| `feat:card:{id}:countries_24h` | 24h | Уникальные страны |
| `feat:customer:{id}:devices` | 30d | Устройства клиента |
| `feat:device:{id}:cards` | 30d | Карты на устройстве |

Признаки, передаваемые в scoring: `card:tx_count_1m/5m/1h/24h`, `card:amount_sum_1h/24h`, `card:unique_merchants_1h`, `card:unique_countries_24h`, `customer:device_count`, `device:card_count`.

### Offline (ClickHouse)

Долгосрочные базовые показатели (запрашиваются in-line, future: кеш): `card:tx_count_7d/30d`, `card:amount_avg_30d`, `card:unique_merchants_30d`, `merchant:fraud_rate_30d`.

## Rule Engine

Правила описаны структурами (загружаются из YAML/JSON, не зашиты в код):

```yaml
- id: velocity_1m
  name: "High velocity — 1 minute"
  priority: 100
  conditions:
    - feature: card:tx_count_1m
      operator: ">"
      threshold: 5
  risk_points: 350
  action: block
  reason_code: HIGH_VELOCITY_1M
  cooldown_minutes: 30
  enabled: true
```

**Встроенный набор**: `velocity_1m`, `velocity_5m`, `amount_spike`, `geo_spread`, `merchant_spread`, `device_proliferation`, `customer_device_spread`, `merchant_high_fraud_rate`.

Hot-reload без рестарта: `engine.ReloadRules(newRules)`.

## Risk Scoring

Итоговое решение по транзакции:

```json
{
  "transaction_id": "tx-123",
  "risk_score": 650,
  "action": "challenge",
  "reason_codes": ["HIGH_VELOCITY_1M", "GEO_SPREAD_24H"],
  "triggered_rules": ["velocity_1m", "geo_spread"],
  "contributing_features": { "card:tx_count_1m": 7, "card:unique_countries_24h": 3 },
  "explain_lines": [
    "[velocity_1m] High velocity — 1 minute → block (+350 pts)",
    "[geo_spread] Geographic spread — 24h → challenge (+300 pts)"
  ],
  "decided_at": "2026-03-06T12:00:00Z"
}
```

Пороги (настраиваются через env):

| Score | Action |
|---|---|
| 0–199 | allow |
| 200–399 | alert |
| 400–599 | review |
| 600–799 | challenge |
| 800–999 | decline |
| 1000 | block |

## Streaming Pipeline

Каждая стадия — отдельная consumer group. Это обеспечивает независимое масштабирование и изоляцию ошибок.

```
Stage 1 · Ingestion
  HTTP / gRPC → validate schema → publish to events.raw / transactions.raw
  (Kafka-first; in-process fallback при недоступности брокера)

Stage 2 · Validation          consumer group: events-processor
  events.raw → field checks, deduplication → forward to processor

Stage 3 · Enrichment          (in-process, inside worker pool)
  timestamp, geo-ip, user-agent, session, counters

Stage 4 · Feature update      (inside fraud-engine consumer)
  transactions.raw → RecordTransaction() → Redis ZSET sliding windows
  offline baselines from ClickHouse

Stage 5 · Fraud scoring       consumer group: fraud-engine
  feature snapshot → rule engine → risk scorer → Decision

Stage 6 · Decision dispatch
  Decision → transactions.decisions (approved/declined)
           → transactions.dlq       (unparse-able payload)

Stage 7 · Storage write       (worker pool batch writer)
  events → ClickHouse batch insert
  stats  → Redis counters
```

Каждый consumer читает только свой топик и не знает о других группах — добавление нового обработчика не требует изменения кода существующих.

## Обработка ошибок и DLQ

```
Transient error (Redis timeout, DB unavailable)
  └─→ retry topic (transactions.retry.1m → .retry.5m)
      exponential back-off, max 3 attempts

Permanent error (bad JSON, unknown schema, logic failure)
  └─→ transactions.dlq
      поле reason_code + оригинальный payload для ручного разбора
```

| Топик | Назначение | TTL |
|---|---|---|
| `transactions.retry.1m` | Первичный ретрай через 1 мин | 1 h |
| `transactions.retry.5m` | Вторичный ретрай через 5 мин | 6 h |
| `transactions.dlq` | Необработанные / постоянные ошибки | 7 d |

Каждый потребитель реализует at-least-once: offset коммитится только после успешного return из handler, поэтому при перезапуске сообщение будет обработано повторно.

## Статус проекта

Проект находится в **активной разработке**. Архитектурная основа стабильна; API Banking и Kafka-pipeline пригодны для экспериментирования и R&D.

| Область | Статус |
|---|---|
| HTTP/gRPC ingestion | ✅ Production-ready |
| Kafka backbone (KRaft) | ✅ Реализован, включается через `KAFKA_ENABLED=true` |
| Online feature store (Redis) | ✅ Реализован |
| Offline feature store (ClickHouse) | ✅ Базовая реализация |
| Rule engine (configurable) | ✅ Реализован, 8 встроенных правил |
| Risk scoring | ✅ Реализован |
| 4-layer fraud engine | ✅ Реализован |
| Banking API | ✅ Реализован |
| ML model serving | 🔲 Planned (Phase 9) |
| Label feedback loop | 🔲 Planned (Phase 10) |
| Production benchmarks | 🔲 Не опубликованы |

> Проект ориентирован на **горизонтально масштабируемые потоковые нагрузки** через consumer groups и партиционирование по `card_id`. Цифры пропускной способности зависят от конфигурации кластера и размера батча — публичных измерений пока нет.



### Требования

- Go 1.24+
- Docker & Docker Compose

### Запуск

```bash
git clone https://github.com/shuldeshoff/stream-flow
cd stream-flow

cp env.example .env

# Запуск зависимостей: ClickHouse, Redis, Kafka, Prometheus, Grafana
docker-compose up -d

go mod download

go build -o bin/streamflow    cmd/streamflow/main.go
go build -o bin/banking-sim   cmd/banking-simulator/main.go
go build -o bin/streamflow-cli cmd/cli/main.go

./bin/streamflow
```

### Включить Kafka

```bash
KAFKA_ENABLED=true ./bin/streamflow
```

При `KAFKA_ENABLED=false` (дефолт) система работает через in-process pipeline без Kafka.

### 🏦 Banking Quick Start

```bash
# Запустить симулятор транзакций
./bin/banking-sim

# Отправить транзакцию вручную
curl -X POST http://localhost:8084/api/v1/banking/transaction \
  -H "Content-Type: application/json" \
  -d '{
    "transaction_id": "tx-001",
    "card_number": "4532123456789012",
    "amount": 1000.00,
    "currency": "RUB",
    "merchant_id": "merchant_42",
    "merchant_name": "Coffee Shop",
    "merchant_mcc": "5812",
    "timestamp": "2026-03-06T12:00:00Z",
    "location": { "country": "RU", "city": "Moscow" }
  }'

# Получить лимиты карты
curl "http://localhost:8084/api/v1/banking/limits?card=4532123456789012"

# Заблокировать / разблокировать карту
curl -X POST http://localhost:8084/api/v1/banking/card/block \
  -H "Content-Type: application/json" \
  -d '{"card_number":"4532123456789012","reason":"manual block"}'

curl -X POST http://localhost:8084/api/v1/banking/card/unblock \
  -H "Content-Type: application/json" \
  -d '{"card_number":"4532123456789012"}'
```

📖 Подробнее: [docs/BANKING-QUICKSTART.md](docs/BANKING-QUICKSTART.md)

## API

### Ingestion API (порт 8080)

```bash
# Одно событие
curl -X POST http://localhost:8080/api/v1/events \
  -H "Content-Type: application/json" \
  -d '{"id":"evt-1","type":"page_view","source":"web","timestamp":"2026-03-06T12:00:00Z","data":{"url":"/home"}}'

# Батч
curl -X POST http://localhost:8080/api/v1/events/batch \
  -H "Content-Type: application/json" \
  -d '[{"id":"e1","type":"click","source":"web","timestamp":"2026-03-06T12:00:00Z","data":{}},...]'

# Health / Readiness
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

## Порты сервисов

| Сервис | Порт | Описание |
|---|---|---|
| HTTP Ingestion | 8080 | `SERVER_PORT` |
| Query API | 8081 | `SERVER_PORT + 1` |
| WebSocket | 8082 | `SERVER_PORT + 2` |
| Auth API (JWT) | 8083 | `SERVER_PORT + 3` (только при `JWT_ENABLED=true`) |
| Banking API | 8084 | `BANKING_PORT` или `SERVER_PORT + 4` |
| Prometheus metrics | 9090 | `METRICS_PORT` |
| Prometheus (docker) | 9091 | — |
| Grafana | 3000 | admin / admin |
| Kafka | 9092 | `KAFKA_BROKERS` |
| Kafka UI | 8090 | — |

## Конфигурация (переменные окружения)

### Core

| Переменная | Default | Описание |
|---|---|---|
| `SERVER_PORT` | `8080` | HTTP порт основного сервера |
| `WORKER_COUNT` | `10` | Количество воркеров event processor |
| `BUFFER_SIZE` | `10000` | Размер очереди событий |
| `BATCH_SIZE` | `1000` | Размер батча для ClickHouse |
| `CLICKHOUSE_ADDRESS` | `localhost:9000` | ClickHouse адрес |
| `REDIS_ADDRESS` | `localhost:6379` | Redis адрес |
| `METRICS_PORT` | `9090` | Prometheus metrics порт |

### Kafka

| Переменная | Default | Описание |
|---|---|---|
| `KAFKA_ENABLED` | `false` | Включить Kafka backbone |
| `KAFKA_BROKERS` | `localhost:9092` | Брокеры (через запятую) |
| `KAFKA_CLIENT_ID` | `streamflow` | Идентификатор клиента |

### Fraud Engine

| Переменная | Default | Описание |
|---|---|---|
| `FRAUD_ENABLED` | `true` | Включить fraud engine |
| `FRAUD_BLOCK_TTL_HOURS` | `24` | TTL блокировки карты (часы) |
| `FRAUD_SCORE_ALERT` | `200` | Порог action=alert |
| `FRAUD_SCORE_REVIEW` | `400` | Порог action=review |
| `FRAUD_SCORE_CHALLENGE` | `600` | Порог action=challenge |
| `FRAUD_SCORE_DECLINE` | `800` | Порог action=decline |

### Banking & Security

| Переменная | Default | Описание |
|---|---|---|
| `BANKING_PORT` | `0` (→8084) | Порт Banking API; 0 = SERVER_PORT+4 |
| `JWT_ENABLED` | `false` | Включить JWT auth |
| `JWT_SECRET` | — | JWT secret (обязателен при JWT_ENABLED=true) |
| `TLS_ENABLED` | `false` | Включить TLS |

### CLI

| Переменная | Default | Описание |
|---|---|---|
| `STREAMFLOW_URL` | `http://localhost:8080` | URL основного сервера |
| `STREAMFLOW_QUERY_URL` | `http://localhost:8081` | URL Query API |

## Мониторинг

- **Metrics**: http://localhost:9090/metrics
- **Prometheus**: http://localhost:9091
- **Grafana**: http://localhost:3000 (admin/admin)
- **Kafka UI**: http://localhost:8090

## Тестирование

```bash
go test ./...
go test -bench=. ./internal/processor/
```

## Структура проекта

```
stream-flow/
├── cmd/
│   ├── streamflow/           # Точка входа
│   ├── cli/                  # CLI инструмент
│   └── banking-simulator/    # Banking симулятор
├── internal/
│   ├── kafka/                # Producer, Consumer, Topics
│   ├── features/             # Online (Redis) + Offline (ClickHouse) feature store
│   ├── rules/                # Конфигурируемый rule engine
│   ├── scoring/              # Risk scorer + Decision
│   ├── fraud/                # 4-layer Engine, Blocker
│   ├── config/
│   ├── ingestion/            # HTTP ingestion (Kafka-first)
│   ├── grpcserver/
│   ├── processor/            # Worker pool, DLQ, enrichment
│   ├── storage/              # ClickHouse
│   ├── cache/                # Redis
│   ├── query/                # Query API
│   ├── ratelimit/
│   ├── websocket/
│   ├── dlq/
│   ├── enrichment/
│   ├── banking/              # Banking API
│   ├── metrics/
│   ├── security/             # TLS, JWT
│   └── models/
├── api/proto/                # Protocol Buffers
├── test/
├── docs/
├── config/                   # Prometheus, Grafana config
├── docker-compose.yml
├── Dockerfile
└── go.mod
```

## Roadmap

- [x] Phase 1: MVP — HTTP ingestion, worker pool, ClickHouse, Redis
- [x] Phase 2: gRPC, rate limiting, WebSocket, DLQ, enrichment
- [x] Phase 3: Banking Edition — legacy rule-based fraud
- [x] Phase 4: TLS/JWT, security
- [x] Phase 5: Kafka backbone, consumer groups, topic model
- [x] Phase 6: Feature store (online Redis + offline ClickHouse)
- [x] Phase 7: Configurable rule engine, risk scoring, 4-layer fraud engine
- [ ] Phase 8: Stream processing — stateful windowing (Kafka Streams / Flink)
- [ ] Phase 9: ML model serving — XGBoost/LightGBM champion/challenger
- [ ] Phase 10: Feedback loop — confirmed_fraud labels, drift monitoring

## Лицензия

MIT License

## Автор

**Шульдешов Юрий Леонидович**  
Telegram: [@shuldeshoff](https://t.me/shuldeshoff)  
GitHub: [shuldeshoff/stream-flow](https://github.com/shuldeshoff/stream-flow)

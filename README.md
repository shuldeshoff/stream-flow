# StreamFlow

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)
[![Go Report Card](https://goreportcard.com/badge/github.com/sul/streamflow)](https://goreportcard.com/report/github.com/sul/streamflow)
[![Build Status](https://github.com/sul/streamflow/workflows/CI/badge.svg)](https://github.com/sul/streamflow/actions)
[![Coverage](https://img.shields.io/badge/coverage-35%25-yellow.svg)](https://github.com/sul/streamflow)
[![Docker](https://img.shields.io/badge/docker-ready-blue.svg?logo=docker)](docker-compose.yml)
[![Powered by ClickHouse](https://img.shields.io/badge/Powered%20by-ClickHouse-yellow.svg?logo=clickhouse)](https://clickhouse.com/)

🌊 Распределенная платформа обработки событий в реальном времени на Go

## Описание

StreamFlow - это высоконагруженная система для сбора, обработки и анализа потоковых данных в реальном времени. Способна обрабатывать миллионы событий в секунду с минимальной задержкой.

Платформа включает **Banking Edition** - специализированное решение для финтеха с real-time детектированием мошенничества, контролем лимитов и обработкой транзакций.

**Версия:** 0.5.0 (Banking Edition)  
**Статус:** Enterprise Ready

## Возможности

### Core Features

✅ **HTTP & gRPC Ingestion** - прием событий через REST API и gRPC  
✅ **Worker Pool** - параллельная обработка на горутинах  
✅ **ClickHouse Storage** - колоночное хранилище для аналитики  
✅ **Redis Caching** - real-time статистика и агрегации  
✅ **Query API** - REST API для получения данных  
✅ **WebSocket** - real-time обновления для dashboard  
✅ **Rate Limiting** - защита от перегрузки  
✅ **Dead Letter Queue** - обработка failed событий  
✅ **Event Enrichment** - автоматическое обогащение данных  
✅ **Prometheus Metrics** - метрики и мониторинг  
✅ **CLI Tool** - консольное управление  
✅ **Graceful Shutdown** - безопасное завершение  
✅ **Docker Compose** - полное dev окружение

### 🏦 Banking Edition

✅ **Fraud Detection** - real-time детектирование мошенничества  
✅ **Limit Tracking** - контроль лимитов по картам и счетам  
✅ **Card Blocking** - автоматическая блокировка при подозрительной активности  
✅ **Banking API** - специализированный API для транзакций  
✅ **Transaction Simulator** - генератор тестовых сценариев

## Архитектура

- **Ingestion Layer** - HTTP/gRPC endpoints для приема событий
- **Processing Layer** - Worker pool на горутинах для параллельной обработки
- **Storage Layer** - ClickHouse для аналитических запросов
- **Cache Layer** - Redis для real-time статистики и агрегаций
- **Query Layer** - REST API для получения данных
- **Metrics** - Prometheus + Grafana для мониторинга
- **Banking Layer** - Fraud Detection Engine + Limit Tracking + Banking API

## Быстрый старт

### Требования

- Go 1.21+
- Docker & Docker Compose
- ClickHouse (или через Docker)

### Установка

```bash
# Клонируем репозиторий
git clone https://github.com/sul/streamflow
cd streamflow

# Копируем конфигурацию
cp env.example .env

# Запускаем зависимости
docker-compose up -d

# Устанавливаем зависимости Go
go mod download

# Собираем проект
go build -o bin/streamflow cmd/streamflow/main.go

# Запускаем
./bin/streamflow
```

### 🏦 Banking Edition Quick Start

```bash
# Собираем Banking Simulator
go build -o bin/banking-sim cmd/banking-simulator/main.go

# Запускаем в другом терминале
./bin/banking-sim

# Симулятор автоматически генерирует:
# - Нормальные транзакции
# - Частые мелкие транзакции (fraud pattern)
# - Крупные транзакции
# - Транзакции с превышением лимитов
```

📖 Подробнее: [docs/BANKING-QUICKSTART.md](docs/BANKING-QUICKSTART.md)

### Использование

#### Отправка одиночного события

```bash
curl -X POST http://localhost:8080/api/v1/events \
  -H "Content-Type: application/json" \
  -d '{
    "id": "evt-123",
    "type": "page_view",
    "source": "web_app",
    "timestamp": "2025-10-23T12:00:00Z",
    "data": {
      "url": "/home",
      "user_id": "user123"
    }
  }'
```

#### Отправка батча событий

```bash
curl -X POST http://localhost:8080/api/v1/events/batch \
  -H "Content-Type: application/json" \
  -d '[
    {
      "id": "evt-1",
      "type": "click",
      "source": "web_app",
      "timestamp": "2025-10-23T12:00:00Z",
      "data": {"button": "subscribe"}
    },
    {
      "id": "evt-2",
      "type": "click",
      "source": "web_app",
      "timestamp": "2025-10-23T12:00:01Z",
      "data": {"button": "share"}
    }
  ]'
```

#### Health Check

```bash
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

#### 🏦 Banking API Examples

```bash
# Проверка транзакции
curl -X POST http://localhost:8080/api/banking/transactions \
  -H "Content-Type: application/json" \
  -d '{
    "card_number": "4532123456789012",
    "amount": 1000.00,
    "currency": "RUB",
    "merchant": "Coffee Shop",
    "location": "Moscow"
  }'

# Получение лимитов карты
curl http://localhost:8080/api/banking/cards/4532123456789012/limits

# Блокировка карты
curl -X POST http://localhost:8080/api/banking/cards/4532123456789012/block
```

## Производительность

**Single Node:**
- Throughput: 50K-100K событий/сек
- Latency (p50): < 10ms
- Latency (p99): < 200ms

**Cluster (10 nodes):**
- Throughput: 1M+ событий/сек

## Мониторинг

- **Метрики**: http://localhost:9090/metrics
- **Prometheus**: http://localhost:9091
- **Grafana**: http://localhost:3000 (admin/admin)

## Тестирование

```bash
# Unit тесты
go test ./...

# Benchmark
go test -bench=. ./internal/processor/

# Load тест
go run test/load_test.go
```

## Конфигурация

Все настройки через переменные окружения (см. `env.example`):

- `SERVER_PORT` - порт HTTP сервера (default: 8080)
- `WORKER_COUNT` - количество воркеров (default: 10)
- `BUFFER_SIZE` - размер очереди событий (default: 10000)
- `BATCH_SIZE` - размер батча для записи (default: 1000)
- `CLICKHOUSE_ADDRESS` - адрес ClickHouse (default: localhost:9000)

## Структура проекта

```
streamflow/
├── cmd/
│   ├── streamflow/
│   │   └── main.go           # Точка входа
│   └── banking-simulator/
│       └── main.go           # Banking симулятор
├── internal/
│   ├── config/               # Конфигурация
│   ├── ingestion/            # HTTP сервер приема событий
│   ├── grpcserver/           # gRPC сервер
│   ├── processor/            # Обработка событий
│   ├── storage/              # Работа с ClickHouse
│   ├── cache/                # Redis кэширование
│   ├── query/                # Query API
│   ├── ratelimit/            # Rate Limiter
│   ├── websocket/            # WebSocket сервер
│   ├── dlq/                  # Dead Letter Queue
│   ├── enrichment/           # Event Enrichment
│   ├── fraud/                # 🏦 Fraud Detection Engine
│   ├── banking/              # 🏦 Banking API
│   ├── metrics/              # Prometheus метрики
│   └── models/               # Модели данных
├── api/proto/                # Protocol Buffers
├── test/
│   ├── load_test.go          # Load тесты
│   └── grpc_test.go          # gRPC тесты
├── docs/                     # Документация
│   ├── BANKING-EDITION.md    # 🏦 Banking Edition docs
│   └── BANKING-QUICKSTART.md # 🏦 Quick Start Guide
├── config/                   # Конфигурационные файлы
├── web/                      # Dashboard
├── docker-compose.yml        # Docker окружение
└── go.mod                    # Go модули
```

## Roadmap

- [x] Phase 1: MVP с базовой функциональностью
- [x] Phase 2: gRPC endpoints и Redis cache
- [x] Phase 3: Rate Limiting и WebSocket
- [x] Phase 4: Dead Letter Queue и Event Enrichment
- [x] Phase 5: Banking Edition с Fraud Detection
- [ ] Phase 6: Stream processing с windowing
- [ ] Phase 7: Мультитенантность и админ-панель

## Применение в реальных сценариях

### 🏦 Финтех и банкинг
Обработка миллионов транзакций в реальном времени с детектированием мошенничества, контролем лимитов и автоматической блокировкой карт при подозрительной активности.

### 📱 Мобильные приложения
Сбор событий от миллионов пользователей: клики, просмотры, покупки. Real-time аналитика поведения и персонализация контента.

### 🎮 Игровая индустрия
Обработка игровых событий: действия игроков, достижения, покупки. Античит системы и real-time лидерборды.

### 📊 E-commerce
Трекинг пользовательских сессий, отслеживание воронки продаж, A/B тестирование, рекомендательные системы на лету.

### 🚗 IoT и телематика
Обработка данных с датчиков автомобилей, умных домов, промышленного оборудования. Мониторинг состояния и предиктивное обслуживание.

## 🤝 Contributing

Мы приветствуем ваш вклад в развитие проекта! Пожалуйста, ознакомьтесь с [CONTRIBUTING.md](CONTRIBUTING.md) для получения информации о:

- Процессе разработки
- Стандартах кодирования
- Создании Pull Requests
- Тестировании

**Quick Start для контрибьюторов:**
```bash
git clone https://github.com/sul/streamflow
cd streamflow
make docker-up
make test
```

## Лицензия

MIT License

## Автор

**Шульдешов Юрий Леонидович**  
Telegram: [@shuldeshoff](https://t.me/shuldeshoff)



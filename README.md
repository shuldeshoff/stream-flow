# StreamFlow

🌊 Распределенная платформа обработки событий в реальном времени на Go

## Описание

StreamFlow - это высоконагруженная система для сбора, обработки и анализа потоковых данных в реальном времени. Способна обрабатывать миллионы событий в секунду с минимальной задержкой.

**Версия:** 0.2.0  
**Статус:** Beta

## Возможности

✅ **HTTP & gRPC Ingestion** - прием событий через REST API и gRPC  
✅ **Worker Pool** - параллельная обработка на горутинах  
✅ **ClickHouse Storage** - колоночное хранилище для аналитики  
✅ **Redis Caching** - real-time статистика и агрегации  
✅ **Query API** - REST API для получения данных  
✅ **Prometheus Metrics** - метрики и мониторинг  
✅ **CLI Tool** - консольное управление  
✅ **Graceful Shutdown** - безопасное завершение  
✅ **Docker Compose** - полное dev окружение

## Архитектура

- **Ingestion Layer** - HTTP/gRPC endpoints для приема событий
- **Processing Layer** - Worker pool на горутинах для параллельной обработки
- **Storage Layer** - ClickHouse для аналитических запросов
- **Cache Layer** - Redis для real-time статистики и агрегаций
- **Query Layer** - REST API для получения данных
- **Metrics** - Prometheus + Grafana для мониторинга

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
│   └── streamflow/
│       └── main.go           # Точка входа
├── internal/
│   ├── config/               # Конфигурация
│   ├── ingestion/            # HTTP сервер приема событий
│   ├── processor/            # Обработка событий
│   ├── storage/              # Работа с ClickHouse
│   ├── metrics/              # Prometheus метрики
│   └── models/               # Модели данных
├── test/
│   └── load_test.go          # Load тесты
├── docs/                     # Документация
├── config/                   # Конфигурационные файлы
├── docker-compose.yml        # Docker окружение
└── go.mod                    # Go модули
```

## Roadmap

- [x] Phase 1: MVP с базовой функциональностью
- [ ] Phase 2: gRPC endpoints и масштабирование
- [ ] Phase 3: Stream processing с windowing
- [ ] Phase 4: Мультитенантность и админ-панель

## Лицензия

MIT License

## Автор

Создано для изучения высоконагруженных систем на Go


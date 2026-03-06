# Multi-stage build для минимального размера образа

# Stage 1: Build
FROM golang:1.23-alpine AS builder

# Устанавливаем необходимые инструменты
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# Копируем go mod файлы для кэширования зависимостей
COPY go.mod go.sum ./
RUN go mod download

# Копируем исходный код
COPY . .

# Собираем бинарник со статической линковкой
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.version=$(git describe --tags --always --dirty) -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -a -installsuffix cgo \
    -o streamflow \
    ./cmd/streamflow/main.go

# Stage 2: Runtime
FROM alpine:latest

# Добавляем ca-certificates для HTTPS и timezone data
RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -g 1000 streamflow && \
    adduser -D -u 1000 -G streamflow streamflow

WORKDIR /app

# Копируем бинарник из builder
COPY --from=builder /build/streamflow .

# Копируем конфигурационные файлы (опционально)
COPY --from=builder /build/env.example .

# Создаем директории для данных
RUN mkdir -p /app/logs /app/certs && \
    chown -R streamflow:streamflow /app

# Переключаемся на непривилегированного пользователя
USER streamflow

# Открываем порты
EXPOSE 8080 8081 8082 8083 8084 9090

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:8080/health || exit 1

# Запускаем приложение
ENTRYPOINT ["./streamflow"]


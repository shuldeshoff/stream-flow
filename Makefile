.PHONY: build run test clean docker-up docker-down

# Сборка
build:
	go build -o bin/streamflow cmd/streamflow/main.go
	go build -o bin/streamflow-cli cmd/cli/main.go
	go build -o bin/banking-simulator cmd/banking-simulator/main.go

# Запуск
run: build
	./bin/streamflow

# Тесты
test:
	go test -v ./...

# Benchmark
bench:
	go test -bench=. -benchmem ./internal/processor/

# Load test
load-test:
	go run test/load_test.go

# Docker
docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

docker-logs:
	docker-compose logs -f

# Очистка
clean:
	rm -rf bin/
	go clean

# Форматирование
fmt:
	go fmt ./...

# Линтинг
lint:
	golangci-lint run

# gRPC proto generation
proto:
	./scripts/generate_proto.sh

# CLI commands
cli-health:
	./bin/streamflow-cli health

cli-stats:
	./bin/streamflow-cli stats

# Запуск с зависимостями
dev: docker-up
	sleep 5
	go run cmd/streamflow/main.go


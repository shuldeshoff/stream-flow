# Contributing to StreamFlow

Спасибо за интерес к проекту StreamFlow! Мы приветствуем любой вклад в развитие проекта.

## 📋 Содержание

- [Code of Conduct](#code-of-conduct)
- [Как внести вклад](#как-внести-вклад)
- [Процесс разработки](#процесс-разработки)
- [Стандарты кодирования](#стандарты-кодирования)
- [Тестирование](#тестирование)
- [Документация](#документация)
- [Коммуникация](#коммуникация)

---

## Code of Conduct

Участвуя в проекте, вы соглашаетесь соблюдать правила взаимного уважения:

- 🤝 Будьте дружелюбны и уважительны
- 💬 Используйте понятный и профессиональный язык
- 🎯 Фокусируйтесь на конструктивной критике
- 🌍 Уважайте различные точки зрения и опыт
- ✨ Помогайте создавать позитивную атмосферу

---

## Как внести вклад

### 🐛 Сообщить о баге

1. Проверьте, что баг еще не был зарегистрирован в [Issues](https://github.com/sul/streamflow/issues)
2. Создайте новый Issue с меткой `bug`
3. Используйте четкий и описательный заголовок
4. Опишите шаги для воспроизведения
5. Приложите логи и скриншоты (если возможно)
6. Укажите вашу версию Go, ОС, и версию StreamFlow

**Шаблон Issue для бага:**
```markdown
## Описание
Краткое описание проблемы

## Шаги для воспроизведения
1. 
2. 
3. 

## Ожидаемое поведение
Что должно было произойти

## Фактическое поведение
Что произошло на самом деле

## Окружение
- StreamFlow версия: 
- Go версия: 
- ОС: 
- ClickHouse версия:

## Логи
```

### ✨ Предложить новую функцию

1. Проверьте [Issues](https://github.com/sul/streamflow/issues) и [Roadmap](README.md#roadmap)
2. Создайте Issue с меткой `enhancement`
3. Опишите предлагаемую функциональность
4. Объясните, зачем она нужна и кому будет полезна
5. Приведите примеры использования

### 🔧 Отправить Pull Request

1. Fork репозиторий
2. Создайте feature branch (`git checkout -b feature/amazing-feature`)
3. Внесите изменения
4. Добавьте тесты для новой функциональности
5. Убедитесь, что все тесты проходят
6. Commit изменения (`git commit -m 'feat: Add amazing feature'`)
7. Push в branch (`git push origin feature/amazing-feature`)
8. Создайте Pull Request

---

## Процесс разработки

### 📁 Структура проекта

```
streamflow/
├── cmd/                    # Точки входа приложений
├── internal/              # Внутренние пакеты
│   ├── banking/          # Banking Edition
│   ├── cache/            # Redis кэширование
│   ├── config/           # Конфигурация
│   ├── fraud/            # Fraud Detection
│   ├── security/         # TLS/JWT
│   └── ...
├── api/proto/            # gRPC протоколы
├── docs/                 # Документация
├── test/                 # Тесты
└── scripts/              # Утилиты
```

### 🌿 Git Workflow

1. **Branches:**
   - `master` - стабильная версия
   - `develop` - разработка
   - `feature/*` - новые функции
   - `bugfix/*` - исправления багов
   - `hotfix/*` - критичные исправления

2. **Commit Messages (Conventional Commits):**
   ```
   <type>(<scope>): <subject>
   
   <body>
   ```
   
   **Types:**
   - `feat`: новая функция
   - `fix`: исправление бага
   - `docs`: документация
   - `style`: форматирование
   - `refactor`: рефакторинг
   - `test`: добавление тестов
   - `chore`: обслуживание
   
   **Примеры:**
   ```
   feat(fraud): add velocity check rule
   fix(http): resolve rate limiting issue
   docs(security): update TLS guide
   test(banking): add transaction tests
   ```

### 🔄 Pull Request Process

1. Обновите README.md если добавляете новую функцию
2. Обновите документацию в `docs/`
3. Добавьте тесты (coverage должен остаться >= 35%)
4. Запустите линтер: `make lint`
5. Убедитесь, что все тесты проходят: `make test`
6. Обновите CHANGELOG.md
7. Получите код ревью от мейнтейнеров
8. После одобрения - merge в `develop`

---

## Стандарты кодирования

### Go Style Guide

Следуйте [Effective Go](https://golang.org/doc/effective_go.html) и [Uber Go Style Guide](https://github.com/uber-go/guide/blob/master/style.md)

### Основные правила:

1. **Форматирование:**
   ```bash
   gofmt -s -w .
   goimports -w .
   ```

2. **Naming Conventions:**
   - Используйте `camelCase` для переменных
   - Используйте `PascalCase` для экспортируемых функций
   - Избегайте аббревиатур (кроме общепринятых: HTTP, API, ID)

3. **Error Handling:**
   ```go
   // Good
   if err != nil {
       return fmt.Errorf("failed to process event: %w", err)
   }
   
   // Bad
   if err != nil {
       return err
   }
   ```

4. **Logging:**
   ```go
   // Используйте zerolog
   log.Info().
       Str("event_id", eventID).
       Int("count", count).
       Msg("Events processed")
   ```

5. **Context:**
   - Всегда передавайте `context.Context` как первый параметр
   - Используйте `context.WithTimeout` для внешних вызовов

### Линтинг

Проект использует `golangci-lint`:
```bash
golangci-lint run
```

Конфигурация в `.golangci.yml` (будет создана в Phase 1).

---

## Тестирование

### Unit Tests

```bash
# Запуск всех тестов
make test

# Запуск с coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Benchmark Tests

```bash
make bench
```

### Integration Tests

```bash
# Требуется запущенный Docker
make docker-up
go test -tags=integration ./...
```

### Требования к тестам:

- ✅ Все новые функции должны иметь unit тесты
- ✅ Coverage >= 70% для новых пакетов
- ✅ Используйте table-driven tests где возможно
- ✅ Моки для внешних зависимостей
- ✅ Benchmarks для критичных функций

**Пример теста:**
```go
func TestEventProcessor_Process(t *testing.T) {
    tests := []struct {
        name    string
        event   models.Event
        wantErr bool
    }{
        {
            name: "valid event",
            event: models.Event{
                ID:   "evt-1",
                Type: "test",
            },
            wantErr: false,
        },
        // ... more test cases
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test logic
        })
    }
}
```

---

## Документация

### Комментарии в коде

- Все экспортируемые функции должны иметь godoc комментарии
- Комментарии начинаются с имени функции/типа
- Используйте полные предложения

```go
// ProcessEvent обрабатывает входящее событие и сохраняет его в хранилище.
// Возвращает ошибку, если событие невалидно или не может быть сохранено.
func ProcessEvent(ctx context.Context, event Event) error {
    // implementation
}
```

### Документация в docs/

- Используйте Markdown
- Структурируйте с помощью заголовков
- Добавляйте примеры кода
- Включайте дату и версию в начало документа
- Используйте эмодзи для улучшения читаемости

### README обновления

При добавлении новых функций обновите:
- Список возможностей
- Примеры использования
- Конфигурационные параметры
- Roadmap (если применимо)

---

## Коммуникация

### 💬 Где обсудить:

- **GitHub Issues** - для багов и feature requests
- **GitHub Discussions** - для вопросов и идей
- **Pull Requests** - для код ревью
- **Telegram** - [@shuldeshoff](https://t.me/shuldeshoff) для срочных вопросов

### 📝 Язык общения:

- Документация: Русский или English
- Код и комментарии: English
- Issues/PRs: Русский или English
- Commit messages: English

---

## 🚀 Начало работы

### Настройка окружения

1. **Установите зависимости:**
   ```bash
   # Go 1.21+
   # Docker & Docker Compose
   # ClickHouse CLI (опционально)
   ```

2. **Клонируйте репозиторий:**
   ```bash
   git clone https://github.com/sul/streamflow
   cd streamflow
   ```

3. **Установите зависимости Go:**
   ```bash
   go mod download
   ```

4. **Запустите зависимости:**
   ```bash
   make docker-up
   ```

5. **Скопируйте конфигурацию:**
   ```bash
   cp env.example .env
   ```

6. **Соберите проект:**
   ```bash
   make build
   ```

7. **Запустите тесты:**
   ```bash
   make test
   ```

### Полезные команды

```bash
make build       # Сборка
make test        # Тесты
make bench       # Benchmarks
make lint        # Линтер
make fmt         # Форматирование
make docker-up   # Запуск Docker сервисов
make docker-down # Остановка Docker сервисов
```

---

## 🎖️ Участники

Спасибо всем, кто вносит вклад в проект!

<!-- ALL-CONTRIBUTORS-LIST:START -->
<!-- Список будет автоматически обновляться -->
<!-- ALL-CONTRIBUTORS-LIST:END -->

---

## 📄 Лицензия

Внося вклад в проект, вы соглашаетесь, что ваш код будет лицензирован под [MIT License](LICENSE).

---

## 🙏 Вопросы?

Если у вас есть вопросы о процессе контрибуции:

- Создайте [GitHub Discussion](https://github.com/sul/streamflow/discussions)
- Напишите в Telegram: [@shuldeshoff](https://t.me/shuldeshoff)

---

**Спасибо за ваш вклад в StreamFlow! 🌊**

**Автор:** Шульдешов Юрий Леонидович  
**Telegram:** [@shuldeshoff](https://t.me/shuldeshoff)


# 🔐 Security Guide: TLS/SSL & JWT Authentication

**Дата:** 23 октября 2025  
**Версия:** 0.6.0  
**Статус:** Production Ready

## 📋 Обзор

StreamFlow теперь поддерживает enterprise-grade безопасность:
- **TLS/SSL** для шифрования данных в транспорте
- **JWT Authentication** для авторизации пользователей
- **mTLS (mutual TLS)** для аутентификации клиентов
- **Role-Based Access Control (RBAC)** для управления доступом

---

## 🔒 TLS/SSL Configuration

### Quick Start (Development)

1. **Генерация самоподписанных сертификатов**:
```bash
./scripts/generate_certs.sh
```

Это создаст:
- `certs/ca-cert.pem` / `certs/ca-key.pem` - Certificate Authority
- `certs/server-cert.pem` / `certs/server-key.pem` - Серверный сертификат
- `certs/client-cert.pem` / `certs/client-key.pem` - Клиентский сертификат (для mTLS)

2. **Включение TLS в конфигурации**:
```bash
# .env
TLS_ENABLED=true
TLS_CERT_FILE=./certs/server-cert.pem
TLS_KEY_FILE=./certs/server-key.pem
# TLS_CA_FILE=./certs/ca-cert.pem  # Опционально для mTLS
```

3. **Запуск сервера**:
```bash
./bin/streamflow
```

Сервер теперь доступен по HTTPS на порту 8080.

4. **Тестирование с curl**:
```bash
# HTTP (если TLS отключен)
curl http://localhost:8080/health

# HTTPS (с TLS)
curl --cacert certs/ca-cert.pem https://localhost:8080/health

# HTTPS с игнорированием самоподписанного сертификата (только для dev!)
curl -k https://localhost:8080/health
```

### Production Deployment

#### Let's Encrypt (рекомендуется)

1. Используйте `certbot` для получения бесплатных сертификатов:
```bash
sudo certbot certonly --standalone -d streamflow.example.com
```

2. Сертификаты будут в `/etc/letsencrypt/live/streamflow.example.com/`

3. Обновите `.env`:
```bash
TLS_ENABLED=true
TLS_CERT_FILE=/etc/letsencrypt/live/streamflow.example.com/fullchain.pem
TLS_KEY_FILE=/etc/letsencrypt/live/streamflow.example.com/privkey.pem
```

4. Автоматическое обновление сертификатов:
```bash
# Crontab
0 0 * * * certbot renew --post-hook "systemctl restart streamflow"
```

#### Корпоративный CA

Если у вас есть корпоративный Certificate Authority:

1. Создайте CSR:
```bash
openssl req -new -key server-key.pem -out server.csr
```

2. Отправьте CSR в CA

3. Получите подписанный сертификат

4. Настройте в `.env`:
```bash
TLS_CERT_FILE=/path/to/signed-cert.pem
TLS_KEY_FILE=/path/to/private-key.pem
TLS_CA_FILE=/path/to/ca-bundle.pem
```

### Mutual TLS (mTLS)

mTLS требует, чтобы клиенты также предоставляли сертификаты для аутентификации.

**Включение mTLS**:
```bash
TLS_ENABLED=true
TLS_CERT_FILE=./certs/server-cert.pem
TLS_KEY_FILE=./certs/server-key.pem
TLS_CA_FILE=./certs/ca-cert.pem  # Обязательно!
```

**Клиентский запрос с mTLS**:
```bash
curl --cacert certs/ca-cert.pem \
     --cert certs/client-cert.pem \
     --key certs/client-key.pem \
     https://localhost:8080/health
```

---

## 🎫 JWT Authentication

### Quick Start

1. **Включение JWT**:
```bash
# .env
JWT_ENABLED=true
JWT_SECRET=my-super-secret-key-min-32-characters-long
JWT_EXPIRATION_HOURS=24
JWT_ISSUER=streamflow
```

⚠️ **ВАЖНО**: JWT_SECRET должен быть минимум 32 символа и держаться в секрете!

2. **Запуск с JWT**:
```bash
./bin/streamflow
```

Будет запущен Auth API на порту 8083 (Server Port + 3).

3. **Получение JWT токена**:
```bash
curl -X POST http://localhost:8083/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "username": "admin",
    "password": "admin123"
  }'
```

Ответ:
```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires_at": "2025-10-24T12:00:00Z",
  "user": {
    "id": "user-admin-001",
    "username": "admin",
    "email": "admin@streamflow.local",
    "roles": ["admin", "user"]
  }
}
```

4. **Использование токена**:
```bash
TOKEN="eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."

curl -H "Authorization: Bearer $TOKEN" \
     https://localhost:8080/api/v1/events \
     -d '{...}'
```

### Demo Users

По умолчанию создаются 3 демо пользователя:

| Username | Password    | Roles           | Описание        |
|----------|-------------|-----------------|-----------------|
| admin    | admin123    | admin, user     | Полный доступ   |
| user     | user123     | user            | Базовый доступ  |
| banking  | banking123  | banking, user   | Banking API     |

⚠️ **В production замените на реальную базу пользователей!**

### Auth API Endpoints

#### POST /api/auth/login
Вход пользователя и получение JWT токена.

**Request**:
```json
{
  "username": "admin",
  "password": "admin123"
}
```

**Response**:
```json
{
  "token": "...",
  "expires_at": "...",
  "user": {...}
}
```

#### POST /api/auth/refresh
Обновление токена (создание нового).

**Request**:
```json
{
  "token": "old-token"
}
```

**Response**:
```json
{
  "token": "new-token",
  "expires_at": "..."
}
```

#### GET /api/auth/me
Получение информации о текущем пользователе (требует JWT).

**Headers**:
```
Authorization: Bearer <token>
```

**Response**:
```json
{
  "id": "user-admin-001",
  "username": "admin",
  "email": "admin@streamflow.local",
  "roles": ["admin", "user"]
}
```

### Role-Based Access Control (RBAC)

JWT токен содержит роли пользователя. Вы можете ограничить доступ к определенным endpoints по ролям.

**Пример в коде**:
```go
// Требуется роль "admin"
adminHandler := security.RequireRole("admin")(http.HandlerFunc(adminOnlyFunc))

// Требуется хотя бы одна из ролей
bankingHandler := security.RequireAnyRole("banking", "admin")(http.HandlerFunc(bankingFunc))
```

### JWT Claims

Каждый JWT токен содержит:
```json
{
  "user_id": "user-admin-001",
  "username": "admin",
  "roles": ["admin", "user"],
  "iss": "streamflow",
  "exp": 1729702800,
  "iat": 1729616400,
  "nbf": 1729616400
}
```

Извлечение claims в обработчиках:
```go
func myHandler(w http.ResponseWriter, r *http.Request) {
    claims, ok := security.GetClaimsFromContext(r.Context())
    if !ok {
        // User not authenticated
        return
    }
    
    userID := claims.UserID
    roles := claims.Roles
    // ...
}
```

---

## 🛡️ Security Best Practices

### TLS/SSL

1. **Всегда используйте TLS в production**
   - Никогда не передавайте данные по HTTP в production
   
2. **Используйте сильные шифры**
   - StreamFlow по умолчанию использует TLS 1.2+ с безопасными cipher suites
   
3. **Регулярно обновляйте сертификаты**
   - Let's Encrypt сертификаты действуют 90 дней
   - Настройте автоматическое обновление
   
4. **Храните приватные ключи в безопасности**
   - Права доступа 600 (только owner)
   - Никогда не коммитьте в Git
   - Рассмотрите использование Hardware Security Modules (HSM)
   
5. **Используйте mTLS для сервис-сервис коммуникации**

### JWT

1. **Используйте длинный и случайный secret**
   ```bash
   # Генерация случайного secret
   openssl rand -base64 48
   ```
   
2. **Храните JWT_SECRET в секретах**
   - Используйте Vault, AWS Secrets Manager, или аналоги
   - Не храните в .env файлах в production
   
3. **Устанавливайте разумное время жизни токенов**
   - Не больше 24 часов для обычных токенов
   - 15 минут для чувствительных операций
   
4. **Используйте HTTPS для передачи токенов**
   - JWT не шифруется, только подписывается
   
5. **Не храните чувствительные данные в JWT**
   - JWT можно декодировать (но не подделать)
   
6. **Реализуйте blacklist для отзыва токенов**
   - Для logout и компрометации
   
7. **Ротация secret ключей**
   - Периодически меняйте JWT_SECRET
   - Поддерживайте несколько ключей для плавного перехода

### Monitoring

Мониторьте:
- TLS handshake ошибки
- Неудачные попытки аутентификации
- Истекшие токены
- Подозрительные паттерны доступа

---

## 🧪 Тестирование

### Проверка TLS

```bash
# Проверка сертификата
openssl s_client -connect localhost:8080 -showcerts

# Проверка cipher suites
nmap --script ssl-enum-ciphers -p 8080 localhost
```

### Проверка JWT

```bash
# Декодирование JWT токена (без проверки подписи)
TOKEN="..."
echo $TOKEN | cut -d'.' -f2 | base64 -d | jq
```

---

## 🚨 Troubleshooting

### TLS Errors

**"certificate verify failed"**
- Проверьте, что клиент доверяет CA
- Используйте `--cacert` для curl
- Проверьте hostname в сертификате

**"tls: bad certificate"** (mTLS)
- Проверьте клиентский сертификат
- Убедитесь, что он подписан правильным CA

### JWT Errors

**"invalid token"**
- Токен истек (проверьте `exp`)
- Неправильный secret ключ
- Токен был изменен

**"Authorization header required"**
- Добавьте заголовок `Authorization: Bearer <token>`

---

## 📚 Дополнительные ресурсы

- [OWASP API Security Top 10](https://owasp.org/www-project-api-security/)
- [JWT Best Practices](https://tools.ietf.org/html/rfc8725)
- [TLS Best Practices](https://wiki.mozilla.org/Security/Server_Side_TLS)

---

**Автор:** Шульдешов Юрий Леонидович  
**Telegram:** @shuldeshoff


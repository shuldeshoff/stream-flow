## 🏦 Быстрый запуск Banking Edition

### Шаг 1: Запустить инфраструктуру

```bash
# Запустить ClickHouse и Redis
make docker-up
```

### Шаг 2: Запустить StreamFlow с Banking API

```bash
# В терминале 1
./bin/streamflow

# Или через go run
go run cmd/streamflow/main.go
```

Вы увидите:
```
✅ HTTP ingestion server started (port 8080)
✅ gRPC server started (port 9000)
✅ WebSocket server started (port 8082)
✅ Query API server started (port 8081)
✅ Banking API server started (port 8083) 🏦
```

### Шаг 3: Запустить симулятор

```bash
# В терминале 2
./bin/banking-simulator

# Или через go run
go run cmd/banking-simulator/main.go
```

### Что произойдет:

**Scenario 1: Normal Transactions (10 транзакций)**
```
✅ Transaction 1: Продукты24 2345.67 RUB - APPROVED
✅ Transaction 2: Магнит 1523.89 RUB - APPROVED
...
```

**Scenario 2: Velocity Attack**
```
✅ Transaction 1: APPROVED
✅ Transaction 2: APPROVED
✅ Transaction 3: APPROVED
✅ Transaction 4: APPROVED
✅ Transaction 5: APPROVED
🚨 Transaction 6: FRAUD DETECTED - 6 transactions in 1 minute (Confidence: 0.85)
   Rule: velocity_check, Action: block
🔒 Card 5678 BLOCKED
```

**Scenario 3: Limit Exceeded**
```
⛔ Transaction DECLINED - Daily limit exceeded
   Limit: 100000.00, Spent: 0.00
```

**Scenario 4: Location Anomaly**
```
✅ Transaction 1 (Russia): approved
🚨 Transaction 2 (USA): FRAUD DETECTED - Location changed too quickly
   Details: map[current_location:US:New York previous_location:RU:Moscow]
```

**Scenario 5: High-Risk Merchant**
```
⚠️  High-Risk Merchant: High-risk merchant category
   Action: review (for review)
```

**Final Statistics:**
```
📈 Fraud Statistics:
  Total Checked: 27
  Fraud Detected: 3
  Cards Blocked: 1
  Fraud Rate: 11.11%
```

### Шаг 4: Тестировать вручную

```bash
# Нормальная транзакция
curl -X POST http://localhost:8083/api/v1/banking/transaction \
  -H "Content-Type: application/json" \
  -d '{
    "transaction_id": "test_001",
    "card_number": "9999",
    "amount": 5000,
    "currency": "RUB",
    "merchant_name": "Пятерочка",
    "merchant_mcc": "5411",
    "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
    "ip_address": "185.46.212.97",
    "device_id": "mobile_ios",
    "location": {
      "country": "RU",
      "city": "Moscow",
      "lat": 55.7558,
      "lon": 37.6173
    }
  }'

# Проверить лимиты
curl http://localhost:8083/api/v1/banking/limits?card=9999

# Посмотреть fraud статистику
curl http://localhost:8083/api/v1/banking/fraud/stats
```

### Шаг 5: Мониторинг

```bash
# Открыть Dashboard
open web/dashboard.html

# Prometheus metrics
curl http://localhost:9090/metrics | grep fraud

# Logs
# Смотрите логи в терминале где запущен StreamFlow
```

---

## 🎓 Что изучите

- Real-time fraud detection в production
- Распределенный rate limiting
- State management в Redis
- Event-driven архитектура
- Microservices patterns
- Banking domain logic

**StreamFlow Banking Edition - готовое решение для финтех!** 🚀


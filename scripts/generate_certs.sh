#!/bin/bash

# Скрипт для генерации самоподписанных сертификатов для разработки
# НЕ ИСПОЛЬЗОВАТЬ В PRODUCTION!

set -e

CERT_DIR="./certs"
DAYS=365

echo "🔐 Генерация TLS сертификатов для разработки..."

# Создаем директорию для сертификатов
mkdir -p "$CERT_DIR"

# Генерируем приватный ключ CA
echo "📝 Генерируем CA ключ..."
openssl genrsa -out "$CERT_DIR/ca-key.pem" 4096

# Генерируем самоподписанный CA сертификат
echo "📝 Генерируем CA сертификат..."
openssl req -new -x509 -days $DAYS -key "$CERT_DIR/ca-key.pem" \
    -out "$CERT_DIR/ca-cert.pem" \
    -subj "/C=RU/ST=Moscow/L=Moscow/O=StreamFlow Dev/OU=Development/CN=StreamFlow Dev CA"

# Генерируем приватный ключ сервера
echo "📝 Генерируем серверный ключ..."
openssl genrsa -out "$CERT_DIR/server-key.pem" 4096

# Создаем CSR (Certificate Signing Request)
echo "📝 Генерируем CSR..."
openssl req -new -key "$CERT_DIR/server-key.pem" \
    -out "$CERT_DIR/server-csr.pem" \
    -subj "/C=RU/ST=Moscow/L=Moscow/O=StreamFlow/OU=Backend/CN=localhost"

# Создаем конфигурацию для SAN (Subject Alternative Names)
cat > "$CERT_DIR/server-ext.cnf" <<EOF
subjectAltName = DNS:localhost,DNS:streamflow,IP:127.0.0.1,IP:0.0.0.0
extendedKeyUsage = serverAuth
EOF

# Подписываем серверный сертификат с помощью CA
echo "📝 Подписываем серверный сертификат..."
openssl x509 -req -days $DAYS \
    -in "$CERT_DIR/server-csr.pem" \
    -CA "$CERT_DIR/ca-cert.pem" \
    -CAkey "$CERT_DIR/ca-key.pem" \
    -CAcreateserial \
    -out "$CERT_DIR/server-cert.pem" \
    -extfile "$CERT_DIR/server-ext.cnf"

# Генерируем клиентский ключ (для mTLS)
echo "📝 Генерируем клиентский ключ..."
openssl genrsa -out "$CERT_DIR/client-key.pem" 4096

# Создаем клиентский CSR
echo "📝 Генерируем клиентский CSR..."
openssl req -new -key "$CERT_DIR/client-key.pem" \
    -out "$CERT_DIR/client-csr.pem" \
    -subj "/C=RU/ST=Moscow/L=Moscow/O=StreamFlow/OU=Client/CN=streamflow-client"

# Создаем конфигурацию для клиента
cat > "$CERT_DIR/client-ext.cnf" <<EOF
extendedKeyUsage = clientAuth
EOF

# Подписываем клиентский сертификат
echo "📝 Подписываем клиентский сертификат..."
openssl x509 -req -days $DAYS \
    -in "$CERT_DIR/client-csr.pem" \
    -CA "$CERT_DIR/ca-cert.pem" \
    -CAkey "$CERT_DIR/ca-key.pem" \
    -CAcreateserial \
    -out "$CERT_DIR/client-cert.pem" \
    -extfile "$CERT_DIR/client-ext.cnf"

# Устанавливаем правильные права доступа
chmod 600 "$CERT_DIR"/*-key.pem
chmod 644 "$CERT_DIR"/*-cert.pem

# Очищаем временные файлы
rm -f "$CERT_DIR"/*.csr "$CERT_DIR"/*.srl "$CERT_DIR"/*.cnf

echo ""
echo "✅ Сертификаты успешно сгенерированы в $CERT_DIR/"
echo ""
echo "📂 Файлы:"
echo "   CA:     $CERT_DIR/ca-cert.pem, $CERT_DIR/ca-key.pem"
echo "   Server: $CERT_DIR/server-cert.pem, $CERT_DIR/server-key.pem"
echo "   Client: $CERT_DIR/client-cert.pem, $CERT_DIR/client-key.pem"
echo ""
echo "⚠️  ВНИМАНИЕ: Эти сертификаты только для РАЗРАБОТКИ!"
echo "   Для production используйте Let's Encrypt или корпоративный CA."
echo ""
echo "🔧 Настройка .env:"
echo "   TLS_ENABLED=true"
echo "   TLS_CERT_FILE=./certs/server-cert.pem"
echo "   TLS_KEY_FILE=./certs/server-key.pem"
echo "   TLS_CA_FILE=./certs/ca-cert.pem  # Опционально для mTLS"


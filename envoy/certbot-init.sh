#!/usr/bin/env bash
# =============================================================================
# certbot-init.sh — первоначальное получение сертификата Let's Encrypt
# =============================================================================
#
# Использование:
#   ./envoy/certbot-init.sh <domain> <email>
#
# Пример:
#   ./envoy/certbot-init.sh example.com admin@example.com
#
# Что делает:
#   1. Создаёт Docker volumes certbot_certs и certbot_www (если не существуют)
#   2. Запускает временный nginx на порту 80 для обслуживания ACME challenge
#   3. Запускает certbot --webroot для получения сертификата
#   4. Останавливает временный nginx
#   5. Выводит команду для запуска продакшн-стека
#
# Требования:
#   - Docker и Docker Compose установлены
#   - DNS домена указывает на этот сервер
#   - Порт 80 свободен (nginx/другие сервисы остановлены)
#   - Скрипт запускается из корня проекта air_orc
#
# После успешного получения сертификата:
#   DOMAIN=example.com docker compose -f docker-compose.prod.yml up -d
# =============================================================================

set -euo pipefail

# ── Аргументы ──────────────────────────────────────────────────────────────────
DOMAIN="${1:-}"
EMAIL="${2:-}"

if [[ -z "$DOMAIN" || -z "$EMAIL" ]]; then
  echo "Использование: $0 <domain> <email>"
  echo "Пример:        $0 example.com admin@example.com"
  exit 1
fi

# Имена Docker volumes (должны совпадать с docker-compose.prod.yml)
VOL_CERTS="airorc_landing_certbot_certs"
VOL_WWW="airorc_landing_certbot_www"

# Имя временного контейнера
BOOTSTRAP_CONTAINER="airorc_acme_bootstrap"

echo ""
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║    AiR ORC — первоначальное получение Let's Encrypt cert         ║"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo ""
echo "  Домен : $DOMAIN"
echo "  Email : $EMAIL"
echo ""
echo "⚠  Убедитесь:"
echo "   1. DNS $DOMAIN → IP этого сервера"
echo "   2. Порт 80 свободен (нет nginx, apache и т.д.)"
echo ""

# ── Шаг 1: Создаём volumes ────────────────────────────────────────────────────
echo "▶ Шаг 1/4: Создаём Docker volumes..."

# Определяем project name (docker compose использует имя директории)
PROJECT_DIR=$(basename "$(pwd)")
VOL_CERTS="${PROJECT_DIR}_certbot_certs"
VOL_WWW="${PROJECT_DIR}_certbot_www"

docker volume create "$VOL_CERTS"  2>/dev/null && echo "  created: $VOL_CERTS"  || echo "  exists:  $VOL_CERTS"
docker volume create "$VOL_WWW"    2>/dev/null && echo "  created: $VOL_WWW"    || echo "  exists:  $VOL_WWW"

# ── Шаг 2: Запускаем временный nginx на порту 80 ─────────────────────────────
echo ""
echo "▶ Шаг 2/4: Запускаем временный ACME HTTP server на порту 80..."

# Останавливаем предыдущий bootstrap-контейнер, если остался
docker rm -f "$BOOTSTRAP_CONTAINER" 2>/dev/null || true

docker run -d \
  --name "$BOOTSTRAP_CONTAINER" \
  -p 80:80 \
  -v "${VOL_WWW}:/usr/share/nginx/html:ro" \
  nginx:1.27-alpine

echo "  Запущен: $BOOTSTRAP_CONTAINER"

# ── Шаг 3: Получаем сертификат через certbot --webroot ───────────────────────
echo ""
echo "▶ Шаг 3/4: Запрашиваем сертификат у Let's Encrypt..."
echo "  (certbot свяжется с сервером ACME и проверит домен)"
echo ""

docker run --rm \
  -v "${VOL_CERTS}:/etc/letsencrypt" \
  -v "${VOL_WWW}:/var/www/certbot" \
  certbot/certbot:latest certonly \
    --webroot \
    --webroot-path /var/www/certbot \
    --domain "$DOMAIN" \
    --email "$EMAIL" \
    --agree-tos \
    --no-eff-email \
    --non-interactive

# ── Шаг 4: Останавливаем временный nginx ─────────────────────────────────────
echo ""
echo "▶ Шаг 4/4: Останавливаем временный HTTP server..."
docker rm -f "$BOOTSTRAP_CONTAINER"

# ── Результат ─────────────────────────────────────────────────────────────────
echo ""
echo "╔══════════════════════════════════════════════════════════════════╗"
echo "║                   ✅  Сертификат получен!                        ║"
echo "╚══════════════════════════════════════════════════════════════════╝"
echo ""
echo "Расположение: /etc/letsencrypt/live/$DOMAIN/"
echo "  fullchain.pem — цепочка сертификатов"
echo "  privkey.pem   — приватный ключ"
echo ""
echo "Запустите продакшн-стек:"
echo ""
echo "  DOMAIN=$DOMAIN docker compose -f docker-compose.prod.yml up -d"
echo ""
echo "Обновление сертификата происходит автоматически (каждые 12 часов)."
echo "Envoy перезагружает новый сертификат без рестарта через watched_directory."
echo ""
echo "Для ручной проверки обновления:"
echo "  docker exec airorc_certbot certbot renew --dry-run --webroot -w /var/www/certbot"
echo ""
echo "Для просмотра текущего сертификата в Envoy:"
echo "  docker exec airorc_envoy curl -s http://127.0.0.1:9901/certs"
echo ""


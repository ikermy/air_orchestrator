# AiR_Orchestrator

![air_orchestrator](air_orchestrator_logo.png)

[🇬🇧 English version](README.md)

![Версия Go](https://img.shields.io/badge/Go-1.25.8-00ADD8?logo=go)
![Лицензия](https://img.shields.io/badge/license-MIT-blue)
[![Telegram](https://img.shields.io/badge/Telegram-Join%20Chat-blue?logo=telegram)](https://t.me/marusia_dev)

`air_orchestrator` — центральный backend-сервис платформы AiR. Он предоставляет HTTP/gRPC API, управляет пользователями, диалогами, AI-моделями и интеграциями, а также координирует работу каналов и вспомогательных микросервисов.

## Возможности

- авторизация, сессии, JWT, TOTP и восстановление доступа;
- управление пользователями, AI-моделями, провайдерами и диалогами;
- интеграция с Telegram, WhatsApp, Avito и операторским сервисом;
- интеграция с Google OAuth, Calendar, Sheets и CRM;
- MCP-сервер с инструментами для AI-моделей;
- realtime-взаимодействие через WebSocket;
- внутренний gRPC API и proxy голосовых звонков;
- хранение файлов в MinIO/S3 и кэширование в Redis;
- Swagger/OpenAPI, Prometheus и профилирование через `pprof`.

## Защита пользовательских данных

Для каждого пользователя создаётся индивидуальным `MasterKey`. Он защищается паролем пользователя и шифрует API-ключи, настройки каналов, диалоги, Google-токены, CRM-конфигурации, embeddings и настройки файлового хранилища. Расшифровка этих данных возможна только после авторизации пользователя в системе индивидуальным паролем пользователя. Даже в случае компрометации или утечки базы данных, все пользовательские данные останутся недоступны как для злоумышленников, так и для администрации сервиса.

Системные секреты и чувствительные значения `app_config` шифруются отдельным мастер-ключом приложения с использованием AES-GCM. Ключ загружается из `APP_MASTER_KEY_FILE`; поддерживается транзакционная смена через `NEW_APP_MASTER_KEY_FILE` и `APP_CONFIG_REKEY`.

## Дерево зависимостей

```text
air_orchestrator
├── air_db (MariaDB/MySQL)
│   └── пользователи, диалоги, конфигурация и прочая зашифрованная информация
├── air_redis (Redis)
│   └── данные сессий и зашифрованный кэш MasterKey
├── minio (S3 storage)
│   └── файлы пользователей
├── envoy
│   └── HTTPS/gRPC-Web маршрутизация шлюза и внешнего трафика
├── air_tgbot
├── air_tguserbot
├── air_whatsbot
├── air_avito
├── air_operator
├── marusia_crm
├── air_payment
└── air_lead-hunter

air-mon.yml
├── cadvisor ── метрики Docker контейнеров
├── prometheus ── сборка и хранение метрик
└── grafana ── визуализация метрик
```

Сервисы взаимодействуют с orchestrator через HTTP/gRPC-контракты. Состав подключаемых сервисов определяется окружением и Docker-сетями `air_shared`, `app_internal` и `monitoring_shared`.

## Технологии

Go 1.25, Gin, gRPC, Protocol Buffers, WebSocket, MariaDB/MySQL, Redis, MinIO/S3, AES-GCM, JWT, TOTP, Google OAuth, MCP Streamable HTTP, Docker, Envoy, Swagger/OpenAPI, Prometheus, Grafana и cAdvisor.

## Запуск

Создайте внешние сети и подготовьте секреты:

```bash
docker network create air_shared
docker network create app_internal
docker network create monitoring_shared
```

Для разработки:

```bash
docker compose -f air-db.yml up -d
docker compose -f air-redis.yml up -d
docker compose -f air-s3.yml up -d
docker compose -f air-mon.yml up -d
docker compose -f dev.yml up -d
```

Для production используется `prod.yml`. Секреты подключаются из `secrets/` и не должны попадать в репозиторий.

## Мониторинг

`air-mon.yml` запускает cAdvisor, Prometheus и Grafana. Prometheus каждые 30 секунд собирает метрики orchestrator, связанных сервисов, контейнеров и MinIO.

## Документация

- [OpenAPI](doc/openapi.yaml)
- [gRPC-контракт звонков](internal/delivery/grpc/v1/calls.proto)
- [Конфигурация Prometheus](monitoring/prometheus.yml)

## Связанные сервисы

- [air_common](https://github.com/ikermy/air_common) — общая библиотека для AI‑микросервисов
- [air_front](https://github.com/ikermy/air_front) — Frontend react next.js панель управления моделями, каналами взаимодействия, сервисами...
- [air_tgbot](https://github.com/ikermy/air_tgbot) — Telegram Bot работа в режиме polling/webhook с возможностью стриминга дельт
- [air_tguserbot](https://github.com/ikermy/air_tguserbot) — Telegram пользовательский бот с возможностью принимать и совершать голосовые звонки
- [air_whatsbot](https://github.com/ikermy/air_whatsbot) — WhatsApp пользовательский бот без использования GraphAPI с возможностью принимать и совершать голосовые звонки
- [air_widget](https://github.com/ikermy/air_widget) — Widget виджет чат для интеграции на любые сайты
- [air_avito](https://github.com/ikermy/air_avito) — бот для ответов в чатах Авито
- [air_operator](https://github.com/ikermy/air_operator) — Сервис переадресации ответов на/от оператора AI работает для всех типов ботов
- [air_lead-hunter](https://github.com/ikermy/air_lead-hunter) — Сервис поиска лидов ботами в Telegram и WhatsApp, в том числе с исходящими голосовыми вызовами
- [air_payment](https://github.com/ikermy/air_payment) — Сервис приёма криптоплатежей от пользователей через Bybit
- [marusia_crm](https://github.com/ikermy/marusia_crm) — Сервис интеграции с внешними CRM системами
- [air_logger](https://github.com/ikermy/air_logger) — Вспомогательный сервис логирования событий с поддержкой многопользовательского режима и поддержкой сборщика логов loki


## Лицензия

Проект распространяется по лицензии [MIT](LICENSE). Она разрешает свободно использовать, копировать, изменять и распространять программное обеспечение при сохранении текста лицензии и уведомления об авторских правах.

Полный текст лицензии доступен в файле [`LICENSE`](LICENSE).

## Контакты

[![Telegram](https://img.shields.io/badge/Telegram-Contact-blue?logo=telegram)](https://t.me/ikermy)

# AiR_Orchestrator

![air_orchestrator](logo.png)

[🇷🇺 Русская версия](README.ru.md)

![Go version](https://img.shields.io/badge/Go-1.25.8-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-MIT-blue)
[![Telegram](https://img.shields.io/badge/Telegram-Join%20Chat-blue?logo=telegram)](https://t.me/marusia_dev)

`air_orchestrator` is the central backend service of the AiR platform. It provides HTTP/gRPC APIs, manages users, conversations, AI models and integrations, and coordinates communication channels and supporting microservices.

## Features

- authentication, sessions, JWT, TOTP and access recovery;
- user, AI model, provider and conversation management;
- Telegram, WhatsApp, Avito and operator service integrations;
- Google OAuth, Calendar, Sheets and CRM integrations;
- MCP server with tools for AI models;
- realtime communication through WebSocket;
- internal gRPC API and voice-call proxy;
- MinIO/S3 file storage and Redis caching;
- Swagger/OpenAPI, Prometheus metrics and `pprof` profiling.

## User Data Protection

Each user receives an individual `MasterKey`. It is protected by the user's password and encrypts API keys, channel settings, conversations, Google tokens, CRM configurations, embeddings and file-storage settings. This data can only be decrypted after the user authenticates in the system with their individual password. Even if the database is compromised or leaked, all user data remains inaccessible both to attackers and to the service administration.

System secrets and sensitive `app_config` values are encrypted with a separate application master key using AES-GCM. The key is loaded from `APP_MASTER_KEY_FILE`; transactional key rotation is supported through `NEW_APP_MASTER_KEY_FILE` and `APP_CONFIG_REKEY`.

## Service dependency tree

```text
air_orchestrator
├── air_db (MariaDB/MySQL)
│   └── users, conversations, configuration and encrypted data
├── air_redis (Redis)
│   └── sessions and user MasterKey cache
├── minio (S3 storage)
│   └── user files
├── envoy
│   └── HTTPS/gRPC-Web gateway and external traffic routing
├── air_tgbot
├── air_tguserbot
├── air_whatsbot
├── air_avito
├── air_operator
├── marusia_crm
├── air_payment
└── air_lead-hunter

air-mon.yml
├── cadvisor ── Docker container metrics
├── prometheus ── metrics collection and storage
└── grafana ── metrics visualization
```

Channel, CRM, payment and lead-hunting services communicate with the orchestrator through HTTP/gRPC contracts. The exact set of services depends on the deployment environment and Docker networks `air_shared`, `app_internal` and `monitoring_shared`.

## Technologies

Go 1.25, Gin, gRPC, Protocol Buffers, WebSocket, MariaDB/MySQL, Redis, MinIO/S3, AES-GCM, JWT, TOTP, Google OAuth, MCP Streamable HTTP, Docker, Envoy, Swagger/OpenAPI, Prometheus, Grafana and cAdvisor.

## Running

Create the external networks and prepare secrets:

```bash
docker network create air_shared
docker network create app_internal
docker network create monitoring_shared
```

For development:

```bash
docker compose -f air-db.yml up -d
docker compose -f air-redis.yml up -d
docker compose -f air-s3.yml up -d
docker compose -f air-mon.yml up -d
docker compose -f dev.yml up -d
```

Use `prod.yml` for production. Secrets are mounted from `secrets/` and must not be committed.

## Monitoring

`air-mon.yml` starts cAdvisor, Prometheus and Grafana. Prometheus collects orchestrator, related-service, container and MinIO metrics every 30 seconds.

## Documentation

- [OpenAPI](doc/openapi.yaml)
- [Calls gRPC contract](internal/delivery/grpc/v1/calls.proto)
- [Prometheus configuration](monitoring/prometheus.yml)

## Ecosystem marusia_ai

- [air-common](https://github.com/ikermy/air-common) — shared library for AI microservices
- [air_orchestrator](https://github.com/ikermy/air_orchestrator) — main orchestration service
- [air_tgbot](https://github.com/ikermy/air_tgbot) — Telegram Bot operating in polling/webhook mode with delta streaming support
- [air_tguserbot](https://github.com/ikermy/air_tguserbot) — Telegram user bot capable of receiving and making voice calls
- [air_whatsbot](https://github.com/ikermy/air_whatsbot) — WhatsApp user bot without using the Graph API, capable of receiving and making voice calls
- [air_widget](https://github.com/ikermy/air_widget) — chat widget for integration with any website
- [air_avito](https://github.com/ikermy/air_avito) — bot for replying to Avito chats
- [air_operator](https://github.com/ikermy/air_operator) — service for forwarding responses to and from an operator; the AI works for all bot types
- [air_lead-hunter](https://github.com/ikermy/air_lead-hunter) — service for bots to find leads in Telegram and WhatsApp, including outgoing voice calls
- [air_payment](https://github.com/ikermy/air_payment) — service for accepting cryptocurrency payments from users through Bybit
- [marusia_crm](https://github.com/ikermy/marusia_crm) — integration service for external CRM systems
- [air-logger](https://github.com/ikermy/air-logger) — auxiliary event-logging service with multi-user mode and Loki collector support

## License

The project is distributed under the [MIT](LICENSE) license. It permits the free use, copying, modification and distribution of the software provided that the license text and copyright notices are retained.

The full license text is available in the [`LICENSE`](LICENSE) file.

## Contacts

[![Telegram](https://img.shields.io/badge/Telegram-Contact-blue?logo=telegram)](https://t.me/ikermy)

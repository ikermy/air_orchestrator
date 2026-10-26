# Makefile для air_orchestrator.
# Запускать из каталога build/: `make <target>`
# Переменные окружения для compose берутся из ../configs/.env

SHELL    := /bin/sh
ENV_FILE := ../configs/.env
DC       := docker compose --env-file $(ENV_FILE)

.DEFAULT_GOAL := help

.PHONY: help build up down restart logs ps infra infra-down prod-up prod-down test lint vet tidy

help: ## Показать список целей
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Собрать dev-образ airorc
	$(DC) -f dev.yml build

up: infra ## Поднять инфраструктуру и dev-приложение
	$(DC) -f dev.yml up -d

down: ## Остановить dev-приложение
	$(DC) -f dev.yml down

restart: ## Пересоздать dev-приложение
	$(DC) -f dev.yml up -d --force-recreate --no-deps airorc

logs: ## Логи dev-приложения (follow)
	$(DC) -f dev.yml logs -f

ps: ## Статус контейнеров dev-приложения
	$(DC) -f dev.yml ps

infra: ## Поднять инфраструктуру (db, redis, s3, monitoring)
	$(DC) -f air-db.yml up -d
	$(DC) -f air-redis.yml up -d
	$(DC) -f air-s3.yml up -d
	$(DC) -f air-vm.yml up -d
	$(DC) -f air-vlogs.yml up -d
	$(DC) -f air-vector.yml up -d
	$(DC) -f air-mon.yml up -d
	$(DC) -f air-perses.yml up -d

infra-down: ## Остановить инфраструктуру
	$(DC) -f air-db.yml down
	$(DC) -f air-redis.yml down
	$(DC) -f air-s3.yml down
	$(DC) -f air-vm.yml down
	$(DC) -f air-vlogs.yml down
	$(DC) -f air-vector.yml down
	$(DC) -f air-mon.yml down
	$(DC) -f air-perses.yml down

prod-up: ## Поднять prod
	$(DC) -f prod.yml up -d --force-recreate

prod-down: ## Остановить prod
	$(DC) -f prod.yml down

test: ## Запустить go test ./...
	cd .. && go test ./...

lint: ## Запустить go vet ./...
	cd .. && go vet ./...

vet: lint ## Алиас для lint

tidy: ## go mod tidy
	cd .. && go mod tidy

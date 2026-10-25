.DEFAULT_GOAL := help

COMPOSE ?= docker compose

.PHONY: help test smoke build image web-image web-dev go-test go-smoke go-build go-image legacy-build \
        up down restart logs ps env-init build-all

help:
	@echo "TenSunS-go"
	@echo "  make test        运行 Go 单元测试"
	@echo "  make smoke       运行兼容性冒烟测试"
	@echo "  make build       构建 Go 二进制"
	@echo "  make image       构建 Go 后端容器镜像"
	@echo "  make web-image   构建 Vue 前端 Nginx 镜像"
	@echo "  make web-dev     启动 Vue 本地开发服务"
	@echo "  make legacy-build  构建旧 Flask 镜像（仅迁移参考）"
	@echo ""
	@echo "docker compose 一键启动（Consul + Go API + Vue 前端）"
	@echo "  make env-init    从 .env.example 生成 .env（已存在则不覆盖）"
	@echo "  make up          docker compose up -d --build 一键构建并启动"
	@echo "  make down        停止并移除容器（保留数据卷）"
	@echo "  make restart     重启整套服务"
	@echo "  make ps          查看服务状态"
	@echo "  make logs        跟踪全部服务日志"
	@echo "  make build-all   只构建两个镜像，不启动"

test: go-test

smoke: go-smoke

build: go-build

image: go-image

go-test:
	go test ./...

go-smoke:
	./scripts/smoke.sh

go-build:
	mkdir -p bin
	go build -o bin/tensuns ./cmd/tensuns
	go build -o bin/consulmock ./cmd/consulmock

go-image:
	docker build -t tensuns-go:latest .

WEB_BUILD_ARGS ?=

web-image:
	docker build $(WEB_BUILD_ARGS) -t tensuns-go-web:latest vue-consul

web-dev:
	cd vue-consul && npm install && npm run dev

legacy-build:
	cd flask-consul && docker build -t flask-consul:latest .

# --- docker compose -----------------------------------------------------------

env-init:
	@test -f .env && echo ".env 已存在，未覆盖" || { cp .env.example .env && echo "已生成 .env，请先编辑其中的 consul_management_token / consul_token / admin_passwd"; }

build-all:
	$(COMPOSE) build

up: env-init
	$(COMPOSE) up -d --build
	$(COMPOSE) ps

down:
	$(COMPOSE) down

restart:
	$(COMPOSE) restart

ps:
	$(COMPOSE) ps

logs:
	$(COMPOSE) logs -f --tail=200

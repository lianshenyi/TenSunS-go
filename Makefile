.DEFAULT_GOAL := help

# 通过 scripts/compose.sh 转发：自动在 `docker compose`（v2 插件）与
# `docker-compose`（v1 二进制）之间选择，两者都没有时给出安装指引。
COMPOSE ?= ./scripts/compose.sh

.PHONY: help test smoke build image web-image web-dev go-test go-smoke go-build go-image legacy-build \
        up down restart logs ps env-init env-regen build-all compose

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
	@echo "  ./scripts/gen-env.sh          1. 生成 .env（随机生成 Token 与 admin 密码）"
	@echo "  ./scripts/compose.sh up -d --build  2. 构建镜像并启动整套服务"
	@echo ""
	@echo "  make env-init    同 ./scripts/gen-env.sh（已存在则不覆盖）"
	@echo "  make env-regen   重新生成 .env（覆盖已有文件，随机生成 Token 与 admin 密码）"
	@echo "  make up          一键构建并启动（自动适配 docker compose / docker-compose）"
	@echo "  make down        停止并移除容器（保留数据卷）"
	@echo "  make restart     重启整套服务"
	@echo "  make ps          查看服务状态"
	@echo "  make logs        跟踪全部服务日志"
	@echo "  make build-all   只构建两个镜像，不启动"
	@echo ""
	@echo "  其他 compose 命令直接透传，例如：make compose CONFIG"
	@echo "  （COMPOSE 可覆盖：make up COMPOSE=docker-compose）"

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
	@test -f .env && echo ".env 已存在，未覆盖" || { ./scripts/gen-env.sh && echo "已生成 .env（随机 Token 与 admin 密码），可直接 make up"; }

env-regen:
	./scripts/gen-env.sh --force

# 透传任意 compose 子命令，统一走 scripts/compose.sh 做环境适配。
# 例：make compose CONFIG / make compose logs -f api
compose:
	$(COMPOSE) $(ARGS)

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

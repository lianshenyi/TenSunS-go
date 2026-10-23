.DEFAULT_GOAL := help

.PHONY: help test smoke build image web-image web-dev go-test go-smoke go-build go-image legacy-build

help:
	@echo "TenSunS-go"
	@echo "  make test        运行 Go 单元测试"
	@echo "  make smoke       运行兼容性冒烟测试"
	@echo "  make build       构建 Go 二进制"
	@echo "  make image       构建 Go 后端容器镜像"
	@echo "  make web-image   构建 Vue 前端 Nginx 镜像"
	@echo "  make web-dev     启动 Vue 本地开发服务"
	@echo "  make legacy-build  构建旧 Flask 镜像（仅迁移参考）"

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

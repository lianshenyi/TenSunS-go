.DEFAULT_GOAL := help

.PHONY: help test smoke build image go-test go-smoke go-build go-image legacy-build

help:
	@echo "TenSunS-go"
	@echo "  make test        运行 Go 单元测试"
	@echo "  make smoke       运行兼容性冒烟测试"
	@echo "  make build       构建 Go 二进制"
	@echo "  make image       构建 Go 容器镜像"
	@echo "  make legacy-build  构建旧 Flask/Vue 镜像（仅迁移参考）"

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

legacy-build:
	cd flask-consul && docker build -t flask-consul:latest .
	cd vue-consul && docker build -t nginx-consul:latest .

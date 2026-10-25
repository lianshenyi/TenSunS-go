#!/usr/bin/env sh
# 统一入口：在不同 Docker 环境下找到可用的 Compose 实现并转发参数。
#
#   ./scripts/compose.sh up -d --build
#
# 依次尝试：
#   1. `docker compose`     —— Compose v2 的 CLI 插件（推荐）
#   2. `docker-compose`     —— Compose v1 的独立二进制
#   3. 直接报错并给出安装指引
#
# 为什么需要它：部分服务器（如未安装 compose 插件的旧版 Docker）上执行
# `docker compose up -d` 会报 `unknown shorthand flag: 'd' in -d` —— 那是
# docker CLI 把 `compose` 当成了未知命令，进而把 `-d` 当成自己的参数解析。
set -eu

has_compose_v2() {
    # `docker compose version` 在插件缺失时返回非 0。
    docker compose version >/dev/null 2>&1
}

has_compose_v1() {
    command -v docker-compose >/dev/null 2>&1
}

if has_compose_v2; then
    exec docker compose "$@"
fi

if has_compose_v1; then
    exec docker-compose "$@"
fi

cat >&2 <<'EOF'
compose: 找不到可用的 Docker Compose。

当前环境既没有 `docker compose`（Compose v2 CLI 插件），也没有
`docker-compose`（Compose v1 独立二进制）。

请安装其中一种：

# CentOS / RHEL / Rocky / Alma
sudo yum install -y docker-compose-plugin   # Compose v2（推荐）
sudo yum install -y docker-compose             # Compose v1 备选

# Debian / Ubuntu
sudo apt-get update && sudo apt-get install -y docker-compose-plugin

# Docker 官方仓库（各发行版通用，插件会装到 ~/.docker/cli-plugins）
mkdir -p ~/.docker/cli-plugins
curl -SL https://github.com/docker/compose/releases/latest/download/docker-compose-linux-x86_64 \
  -o ~/.docker/cli-plugins/docker-compose
chmod +x ~/.docker/cli-plugins/docker-compose

装好后回到项目目录执行：
  ./scripts/gen-env.sh          # 生成 .env
  ./scripts/compose.sh up -d --build
EOF
exit 127

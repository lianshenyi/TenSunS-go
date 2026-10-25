<div align="center">

[![Go](https://img.shields.io/badge/go-%3E%3D1.22-00ADD8)](https://go.dev)
[![Vue](https://img.shields.io/badge/vue-2.x-42b883)](https://vuejs.org)
[![License](https://img.shields.io/badge/license-WTFPL-blueviolet)](LICENSE)
</div>

## 🦄概述

**TenSunS-go** 是 [TenSunS](https://github.com/starsliao/TenSunS) 的 Go 二开版本，目标是只保留云资源同步与 JumpServer 纳管能力，并用 Go 重写后端。

> 本仓库是二次开发项目，不是 TenSunS 上游仓库。原始项目、版权与贡献者信息请见 [starsliao/TenSunS](https://github.com/starsliao/TenSunS)。

当前状态：

- Go 后端骨架已完成配置加载、登录鉴权、AES/Token 兼容、Consul 存储、动态任务调度、任务加载、资源读取 API 与兼容性冒烟测试。
- 云厂商同步、Consul 同步写入、JumpServer 客户端及写操作 API 仍在迁移中，详见 [Go 迁移进度](docs/Go迁移进度.md)。
- `flask-consul/` 保留为迁移期间的 Python 行为参考；`vue-consul/` 是前端实现。

当前版本聚焦于以下能力：

- 多云账户接入：阿里云、腾讯云、华为云、AWS。
- 云资源同步：将云厂商的资源分组、ECS、云 MySQL、云 Redis 信息同步到 Consul。
- 资源查询：在 Web 页面查看云主机、云 MySQL、云 Redis 列表，并支持自定义实例的 IP 与端口。
- JumpServer 同步：将多云 ECS 主机自动同步到 JumpServer，免去手动创建资产。

## 🌈功能描述

### ☁️1. 云资源同步

- 支持对接阿里云、腾讯云、华为云、AWS。
- 支持按云账户、区域同步资源分组、ECS、云 MySQL、云 Redis。
- 支持自定义同步间隔与手动立即同步。
- 资源信息统一存储于 Consul，方便后续扩展与对接。

### 🖥2. 云资源查询

- 云主机列表：查看分组、名称、实例、系统、CPU、内存、到期日等信息。
- 云 MySQL 列表：查看分组、实例、版本、CPU、内存、磁盘、到期日等信息。
- 云 Redis 列表：查看分组、实例、版本、内存、到期日、类型等信息。
- 支持自定义实例的 IP 与端口，并同步更新到 Consul。

### 🚀3. JumpServer 同步

- 支持 JumpServer V2 / V3。
- 按云账户配置同步间隔与 JumpServer 节点，自动同步 ECS 主机。
- 支持全局通用管理用户与特殊主机管理用户配置。
- 详细说明：[如何把主机自动同步到 JumpServer](docs/如何把主机自动同步到JumpServer.md)

## 🛠构建与运行

### 一键启动（Docker Compose）

`docker-compose.yml` 编排了完整的三件套：**Consul（含 ACL）** + **Go 后端** + **Vue/Nginx 前端**，
两条命令即可从零启动：

```bash
./scripts/gen-env.sh          # 1. 生成 .env（随机生成 consul_management_token / consul_token / admin_passwd）
./scripts/compose.sh up -d --build  # 2. 构建镜像并后台启动全套服务
```

等价的 Makefile 快捷目标（`make up` 会先自动生成 `.env`）：

```bash
make env-init   # 等同于 ./scripts/gen-env.sh
make up         # 等同于 ./scripts/compose.sh up -d --build，并打印服务状态
```

> **如果 `docker compose` 报 `unknown shorthand flag: 'd' in -d`**
>
> 这不是本项目的编排文件有问题，而是**这台机器的 Docker 没装 Compose**：
> `docker compose` 不是有效的子命令时，docker CLI 会把后面的 `-d` 当成自己的
> 参数解析，于是打印出整份 `docker --help`。用上面的 `./scripts/compose.sh`
> 即可，它会自动适配：`docker compose`（v2 插件）→ `docker-compose`（v1 二进制）；
> 两者都没有时会直接给出安装命令：
>
> ```bash
> # CentOS / RHEL / Rocky / Alma
> sudo yum install -y docker-compose-plugin
> # Debian / Ubuntu
> sudo apt-get update && sudo apt-get install -y docker-compose-plugin
> ```
>
> 想手动指定实现，也可以：`make up COMPOSE=docker-compose`，
> 或直接用 `make compose ARGS="up -d --build"` 透传任意 compose 子命令。

`./scripts/gen-env.sh` 会复制 `.env.example` 为 `.env`，为下面三项**随机生成**安全值，
并把 `.env` 权限设为 `600`：

| 键 | 生成方式 |
| --- | --- |
| `consul_management_token` | 随机 UUID v4（Consul ACL 引导 Token） |
| `consul_token` | 与 `consul_management_token` 相同的 UUID（两者必须一致） |
| `admin_passwd` | 24 位随机十六进制串（内置 admin 账号登录密码） |

其余键（端口、绑定地址、镜像名、日志级别等）沿用 `.env.example` 的默认值。

想手动控制内容、或想重新摇一套随机值：

```bash
cp .env.example .env       # 手动编辑三项占位符
./scripts/gen-env.sh --force  # 重新随机生成并覆盖已有 .env（等价于 make env-regen）
```

生成后的密码请自行留存——`.env` 不会写回仓库。

其他常用命令：

```bash
./scripts/compose.sh ps        # 查看服务状态（等价于 make ps）
./scripts/compose.sh logs -f   # 跟踪日志（等价于 make logs）
./scripts/compose.sh down      # 停止并移除容器，保留数据卷（等价于 make down）
```

启动后：

| 服务 | 地址 | 说明 |
| --- | --- | --- |
| 前端 | `http://<host>:1026` | Nginx 托管 SPA，`/api/` 反代到后端 |
| 后端 | `http://<host>:2026` | Go HTTP API |
| Consul | `http://127.0.0.1:18500` | 仅绑定回环地址，默认不对外暴露 |

几个关键约定：

- `consul_token` 必须与 `consul_management_token` **完全一致**：Consul ACL 默认策略是 `deny`，
  不一致会导致后端启动时的连通性检查失败。管理 token 由 `deploy/consul/init.sh` 写入
  `consul.hcl` 完成 ACL 引导，无需手动 `consul acl bootstrap`。
- 前端 Nginx 通过 Docker DNS 动态解析 `tensuns-go-api:2026`（见 `vue-consul/http-ops.conf`），
  因此后端重建后前端无需重启；反之，**先启动后端再启动前端**。
- 编排使用 `depends_on: service_healthy`，Consul 与后端都带健康检查，启动顺序自动保证。
- Consul 数据保存在命名卷 `tensuns-go-consul-data` 中，`compose down` 不会删除；
  需要完全重置时执行 `./scripts/compose.sh down -v`。
- 演示数据可另行灌入：`python3 scripts/seed_demo.py http://127.0.0.1:18500/v1 <consul_token>`。

> ⚠️ 默认 `scheduler_disabled=1`，调度器不加载 Consul 任务。这是刻意为之：
> 四家云厂商的 Go 同步适配器尚未接线，启用调度只会产生重复报错。
> 需要校验任务调度行为时，临时把它改成 `0` 再重启后端。

### 单独构建

```bash
make go-test    # 单元测试
make go-smoke   # 使用内存 Consul 的兼容性冒烟测试
make go-build   # 构建 bin/tensuns 与 bin/consulmock
make go-image   # 构建后端镜像 tensuns-go:latest
make web-image  # 构建前端 Nginx 镜像 tensuns-go-web:latest
make web-dev    # 启动 Vue 本地开发服务
```

后端直接运行：

```bash
go run ./cmd/tensuns
```

前端镜像默认监听 `1026`，并将 `/api/` 代理到同一 Docker 网络中的 `tensuns-go-api:2026`。构建基础镜像无法直连 Docker Hub 时，可传入：

```bash
make web-image WEB_BUILD_ARGS="--build-arg NODE_IMAGE=... --build-arg NGINX_IMAGE=..."
```

主要环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `consul_url` | `http://127.0.0.1:8500/v1` | Consul HTTP API 地址，必须包含 `/v1` |
| `consul_token` | 空 | Consul ACL Token |
| `admin_passwd` | `123456` | 登录密码，部署时务必修改 |
| `port` | `2026` | 监听端口 |
| `listen_addr` | `0.0.0.0:${port}` | 完整监听地址，设置后覆盖 `port` |
| `log_level` | `INFO` | `DEBUG` / `INFO` / `WARN` / `ERROR` |
| `scheduler_disabled` | 空 | 设置为 `1` 或 `true` 时不加载和执行 Consul 任务 |

> Go 版本启动时会检查 Consul 连通性、初始化 `ConsulManager/assets/secret/skey`，并从 `ConsulManager/jobs`、`ConsulManager/jms/jobs` 读取任务。

Compose 编排另外使用 `.env` 里的这些键（见 `.env.example`）：`consul_management_token`、
`consul_token`、`admin_passwd`、`consul_http_bind` / `consul_http_port`、
`api_http_bind` / `api_http_port`、`web_http_bind` / `web_http_port`、
`scheduler_disabled`、`node_image` / `nginx_image`（基础镜像可指向内部镜像仓库）。

## 📁仓库结构

```text
cmd/tensuns/        Go 后端入口
cmd/consulmock/     内存 Consul 测试服务
internal/           Go 实现与测试
scripts/            兼容性冒烟测试、演示数据灌入、Compose 环境适配与 .env 生成
vue-consul/         Vue 前端
flask-consul/       Python 迁移参考实现
deploy/consul/      Compose 用的 Consul 配置模板与 ACL 引导脚本
docs/               迁移基线、进度与使用文档
install/            旧 Flask 版本的部署脚本，仅作历史参考
docker-compose.yml  Consul + Go 后端 + Vue 前端 一键编排
.env.example        Compose 环境变量模板（复制为 .env 后使用）
```

> `install/` 下的脚本仍指向旧 Flask 镜像，不适用于当前 Go 骨架；Go 阶段请使用 `Dockerfile` / `make go-image`。

## 📚文档

- [Consul 部署说明](docs/Consul部署说明.md)
- [如何把主机自动同步到 JumpServer](docs/如何把主机自动同步到JumpServer.md)
- [开发模式调试说明](docs/开发模式调试说明.md)
- [Go 重构迁移基线](docs/迁移基线.md)
- [Go 迁移进度与骨架说明](docs/Go迁移进度.md)
- [开发与部署约定（AGENTS.md）](AGENTS.md)
- [FAQ](docs/FAQ.md)

## 📄来源与许可

本项目基于 [TenSunS](https://github.com/starsliao/TenSunS) 进行二次开发，沿用上游的 [WTFPL](LICENSE) 许可证。

上游项目采用 Flask + Vue 实现；本仓库在其基础上精简功能并新增 Go 后端。上游贡献者名单与历史请查阅上游仓库。

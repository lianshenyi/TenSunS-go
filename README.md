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

```bash
make go-test    # 单元测试
make go-smoke   # 使用内存 Consul 的兼容性冒烟测试
make go-build   # 构建 bin/tensuns 与 bin/consulmock
make go-image   # 构建容器镜像 tensuns-go:latest
```

直接运行：

```bash
go run ./cmd/tensuns
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

## 📁仓库结构

```text
cmd/tensuns/        Go 后端入口
cmd/consulmock/     内存 Consul 测试服务
internal/           Go 实现与测试
scripts/            兼容性冒烟测试
vue-consul/         Vue 前端
flask-consul/       Python 迁移参考实现
docs/               迁移基线、进度与使用文档
install/            旧 Flask 版本的部署脚本，仅作历史参考
```

> `install/` 下的脚本仍指向旧 Flask 镜像，不适用于当前 Go 骨架；Go 阶段请使用 `Dockerfile` / `make go-image`。

## 📚文档

- [Consul 部署说明](docs/Consul部署说明.md)
- [如何把主机自动同步到 JumpServer](docs/如何把主机自动同步到JumpServer.md)
- [开发模式调试说明](docs/开发模式调试说明.md)
- [Go 重构迁移基线](docs/迁移基线.md)
- [Go 迁移进度与骨架说明](docs/Go迁移进度.md)
- [FAQ](docs/FAQ.md)

## 📄来源与许可

本项目基于 [TenSunS](https://github.com/starsliao/TenSunS) 进行二次开发，沿用上游的 [WTFPL](LICENSE) 许可证。

上游项目采用 Flask + Vue 实现；本仓库在其基础上精简功能并新增 Go 后端。上游贡献者名单与历史请查阅上游仓库。

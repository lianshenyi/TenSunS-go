# Runbook

## 启动

需要 Go 1.22+。终端一运行 `go run ./cmd/consulmock -addr 127.0.0.1:18500`，终端二运行：

```bash
consul_url=http://127.0.0.1:18500/v1 admin_passwd=<PASSWORD> go run ./cmd/tensuns
```

后端监听 `2026`；前端开发服务使用 `make web-dev`，监听 Vue 默认端口并代理 `/api` 到 `127.0.0.1:2026`。完整冒烟：`make smoke`。

## 依赖和环境

需要 Consul HTTP API（开发可用 `cmd/consulmock`）。环境变量：`consul_url=<URL>/v1`、`consul_token=<TOKEN>`、`admin_passwd=<PASSWORD>`、`port=<PORT>`、`listen_addr=<HOST:PORT>`、`log_level=<LEVEL>`、`scheduler_disabled=<true|false>`。不要把真实密钥写入 fixture 或仓库。

fixture Provider 支持 `alicloud`、`tencent_cloud`、`huaweicloud`、`awscloud`，并仅读取 `ConsulManager/fixtures/<VENDOR>/<ACCOUNT>`。fixture 可含 `groups`、`ecs`、`rds`、`redis`，或 `error: auth|rate_limit|timeout|temporary|http_500` 至 `http_599`。无 fixture 时任务失败并写 record，不会伪造同步成功；不要把真实 AK/SK 或生产地址写入 fixture。

JumpServer 同步读取 `ConsulManager/jms/jms_info`（`url`、加密 `token`、`ver=V2|V3`）、`jms/<vendor>/<account>/node_id` 和全局或账户级 `ecs_info`/`custom_ecs_info`。账户级非空 `custom_ecs_info` 完全替代全局配置；多个关键字按 JSON 声明顺序匹配，后命中项覆盖前项。V3 新增用 `/api/v1/assets/hosts/`，V2 新增用 `/api/v1/assets/assets/`，两版更新一律用 `/api/v1/assets/<ID>/`。同步会删除父节点下名称不匹配的重复 IP 资产及显示为 ` (0)` 的空子节点，故该父节点应仅放置允许本同步管理的对象。测试使用 `httptest` fixture，绝不写入真实 Token。请求使用 `Authorization: Token <token>`；429、5xx、临时网络/超时最多重试三次，401/403 不重试。失败查看 `ConsulManager/record/jms/<vendor>/<account>`；重跑继续对账以收敛部分写入。

## 测试数据

`cmd/consulmock` 的 `SeedDemo` 提供 `main` 账户与基础资源；同步单测自行写入脱敏 fixture。登录账号为 `admin`，密码来自 `admin_passwd`。

## 排障

- 启动失败：确认 `consul_url` 含 `/v1` 且 ACL token 有 KV/agent service 权限。
- 任务失败：查看 `ConsulManager/record/jobs/<vendor>/<account>/...` 的 `msg`，确认对应 vendor fixture 存在且无 `error`。
- 服务未更新：确认任务 `func` 为 `__main__:<vendor>.*`，并不要设置 `scheduler_disabled=true`。
- 限流、临时错误、5xx 或超时：会最多重试三次；持续失败时检查 fixture/provider 和上游网络，再手动重跑任务。

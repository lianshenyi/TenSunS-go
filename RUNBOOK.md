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

## 测试数据

`cmd/consulmock` 的 `SeedDemo` 提供 `main` 账户与基础资源；同步单测自行写入脱敏 fixture。登录账号为 `admin`，密码来自 `admin_passwd`。

## 排障

- 启动失败：确认 `consul_url` 含 `/v1` 且 ACL token 有 KV/agent service 权限。
- 任务失败：查看 `ConsulManager/record/jobs/<vendor>/<account>/...` 的 `msg`，确认对应 vendor fixture 存在且无 `error`。
- 服务未更新：确认任务 `func` 为 `__main__:<vendor>.*`，并不要设置 `scheduler_disabled=true`。
- 限流、临时错误、5xx 或超时：会最多重试三次；持续失败时检查 fixture/provider 和上游网络，再手动重跑任务。

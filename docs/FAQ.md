### 查看服务日志

在仓库根目录运行：

```bash
./scripts/compose.sh logs --tail=50 -f api
./scripts/compose.sh logs --tail=50 -f web
```

### Go API 启动时无法连接 Consul

先检查 Consul 的健康状态和日志：

```bash
./scripts/compose.sh ps
./scripts/compose.sh logs --tail=50 consul
```

Compose 中的 API 通过 `http://consul:8500/v1` 连接 Consul。确认 `.env` 中的
`consul_token` 与初始化当前数据卷的 Consul 管理令牌一致。直接运行 Go API 时，
确认 `consul_url` 指向可达的 Consul HTTP 地址并以 `/v1` 结尾。

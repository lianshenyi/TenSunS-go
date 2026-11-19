# Go 迁移进度

现行 Compose 运行 Go API、Vue/Nginx 和 Consul。Go 后端支持阿里云、腾讯云、华为云和 AWS 资源同步及 Consul 写回、PVE 分组和主机同步、JumpServer V2/V3 资产同步，以及任务和资源维护接口。

旧 Flask 后端与专用构建、发布、安装入口已退役。迁移时保留的 API、任务 KV、加密和 Token 格式见[迁移基线](迁移基线.md)。当前启动、联调和 Go 回滚入口见 [RUNBOOK](../RUNBOOK.md)。

## 1. 代码结构

```text
cmd/tensuns              Go 后端入口
cmd/consulmock           本地 Consul fixture 入口
internal/config          环境变量、厂商/区域表
internal/cryptox         AES、itsdangerous Token
internal/auth            管理员密码、Bearer 校验
internal/store           Store 接口
internal/store/consul    Consul HTTP 实现、skey 初始化
internal/scheduler       动态 interval 调度器、func 解析
internal/jobs            任务加载、typed runtime 注入点
internal/cloud           厂商 Provider 接口与归一化模型
internal/sync            Cloud -> Consul 落库接口
internal/jumpserver      JumpServer 同步接口
internal/httpapi         前端兼容 HTTP API
internal/consulmock      测试/冒烟用 Consul HTTP fixture
scripts/smoke.sh         附录 B 冒烟测试
```

## 2. 运行

### 本地测试

```bash
go test ./...
go test -race ./...
go vet ./...
```

### 使用内存 Consul 冒烟

```bash
./scripts/smoke.sh
```

脚本不会连接真实 Consul 或 JumpServer，会启动：

- `consulmock`：`127.0.0.1:18500`
- Go 后端：`127.0.0.1:12026`

可用环境变量覆盖地址：`SMOKE_CONSUL_ADDR`、`SMOKE_BACKEND_ADDR`、`SMOKE_PASSWORD`。

### 连接已有 Consul（只读/联调）

```bash
export consul_url='http://127.0.0.1:8500/v1'
export consul_token='...'
export admin_passwd='...'
export scheduler_disabled=1
go run ./cmd/tensuns
```

`consul_url` 必须包含 `/v1`。同一份 Consul 数据只允许运行一个调度实例。

### 构建镜像

```bash
make go-image
```

镜像默认监听 `2026`，时区 `Asia/Shanghai`。

## 3. 已确认的兼容点

- Python 生成的登录 Token 可由 Go 校验；Go 生成的 Token 格式与 Python 一致。
- Python 生成的 AK/SK、JMS Token 密文可由 Go 解密，反之亦然（AES 测试向量覆盖）。
- 已有任务 KV 可被 Go 加载；未知 `func` 不会被删除。
- `skey` 缺失时的初始化路径和备份命名保持一致。
- 前端依赖的顶层字段、HTTP 200 + 业务码、`Bearer ` 前缀保持不变。
- 时间格式保持：云任务 `%m.%d/%H:%M`、JMS 记录 `%m%d/%H:%M`。

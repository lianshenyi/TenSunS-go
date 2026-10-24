# Go 迁移进度与骨架说明

> 对应代码：仓库根目录 `cmd/`、`internal/`、`scripts/smoke.sh`  
> 契约来源：[迁移基线](迁移基线.md)  
> 状态：骨架阶段；尚未替换生产 Python 后端

## 1. 当前目标

第一阶段只搭“可运行、可测试、可回滚”的 Go 骨架，不修改现有 Vue 前端与 Consul 数据结构。已实现和未实现项严格分开，避免把占位接口误认为生产能力。

## 2. 已实现

| 模块 | 内容 |
| --- | --- |
| 启动 | `consul_url`、`consul_token`、`admin_passwd`、`port`、`listen_addr`、`scheduler_disabled` 环境变量。 |
| Consul 检查 | 与 Python 一致的 `/operator/autopilot/state` 探测及 `/health` 回退。 |
| skey | 只在 `ConsulManager/assets/secret/skey` 不存在时生成并备份；已有 key 永不覆盖。 |
| AES | AES-256-CBC、随机 IV、PKCS#7、标准 Base64；通过 Python 测试向量。 |
| 登录 Token | itsdangerous 兼容的 HS512 JWS compact、8 小时 TTL；通过 Python 生成 Token 测试向量。 |
| Consul Store | KV raw/recurse/keys、Service 查询、注册、注销、Health API；Token 请求头。 |
| 调度器 | 动态 interval、`replace_existing`、同 Job ID 串行、`RunNow`、`NextRun`、优雅关闭。 |
| 任务加载 | 加载 `ConsulManager/jobs` 与 `ConsulManager/jms/jobs`；识别 5 种旧 `func`，未知任务跳过但不删除。 |
| 阿里云阶段一 | 脱敏 fixture Provider、统一 ECS/RDS/Redis 模型、资源组 KV 与 Consul Service 写回、执行记录、有限重试及失败记录。仅用于 mock/fixture 验收，不读取真实 AK/SK。 |
| 鉴权 | 单管理员密码；认证失败返回 HTTP 200 + `code=50000`。 |
| HTTP 只读接口 | 附录 B 所需的登录、用户信息、任务列表、账户列表、资源列表、JMS 列表，以及 `edit/find`、JMS 配置读取等。 |
| HTTP 可写接口 | 任务间隔修改、任务删除、JMS 配置保存、JMS 同步开关关闭。 |
| 测试 | AES/Token 向量、Store、调度器、任务加载、HTTP 契约测试。 |
| 冒烟 | `scripts/smoke.sh` 使用内存 Consul fixture 启动真实 Go 二进制并执行附录 B 检查。 |

## 3. 尚未实现

以下接口目前返回 `code=50000` 或尚未接入，不会静默成功：

- 云厂商 Go SDK 适配器：阿里云（真实 SDK）、腾讯云、华为云、AWS。
- 腾讯云、华为云、AWS 的 Provider 与资源同步写回。
- JumpServer V2/V3 客户端与资产同步。
- `POST /api/jobs` 的 `dialogStatus=create` / `run`。
- `POST /api/edit/commit`。
- ECS/RDS/Redis 自定义实例即时改写。
- `POST /api/jms/sync` 首次同步。

因此当前版本只适合做登录、读取、契约联调和迁移演练，不能接管真实同步任务。

## 4. 代码结构

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

## 5. 运行

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

`consul_url` 必须包含 `/v1`。生产迁移时应先停掉 Python 调度器，再启动 Go；禁止两个调度器同时运行。

### 构建镜像

```bash
make go-image
```

镜像默认监听 `2026`，时区 `Asia/Shanghai`。

## 6. 已确认的兼容点

- Python 生成的登录 Token 可由 Go 校验；Go 生成的 Token 格式与 Python 一致。
- Python 生成的 AK/SK、JMS Token 密文可由 Go 解密，反之亦然（AES 测试向量覆盖）。
- 已有任务 KV 可被 Go 加载；未知 `func` 不会被删除。
- `skey` 缺失时的初始化路径和备份命名保持一致。
- 前端依赖的顶层字段、HTTP 200 + 业务码、`Bearer ` 前缀保持不变。
- 时间格式保持：云任务 `%m.%d/%H:%M`、JMS 记录 `%m%d/%H:%M`。

## 7. 已知差异（骨架阶段）

| 差异 | 说明 |
| --- | --- |
| 未实现写接口 | 返回 `50000` 并带有“尚未实现”提示，不会伪造成功。 |
| JMS 配置保存顺序 | 先校验自定义 JSON，再写 3 个 KV；这是基线建议的健壮性改进。 |
| 调度补跑 | 当前在任务执行完成后计算下一次；长任务会让 nextime 比 APScheduler 略晚。第一版可接受，接入云同步前需明确是否改为固定节拍。 |
| 未接云适配器 | 调度器仍会加载任务，但执行会记录“未接入”错误，不写 Consul 记录。 |

## 8. 下一步建议

1. 实现 `internal/sync` 的通用落库逻辑与 Service 清理。
2. 按厂商实现 `internal/cloud.Provider`，从阿里云开始，并用 JSON fixture 测归一化。
3. 接入 `POST /api/jobs` 创建/立即执行和 `POST /api/edit/commit`。
4. 实现自定义实例即时改写。
5. 实现 JumpServer V2/V3 同步与 `POST /api/jms/sync`。
6. 用真实 Consul 快照做回滚测试：Go 写数据 → Python 读取并继续调度。

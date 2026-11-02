# 技术规格：多云资源组与资源任务同步

**关联事项**：VIDE-39；**规格状态**：实现完成，待统一验收（2026-11-03）

## 架构

`httpapi` 将创建请求映射为 `accounts.Request` 并委托可注入的 `accounts.Creator`。Creator 加密并持久化 AK/SK，调用 `sync.Cloud.Group`；后者通过 `cloud.Registry` 取 Provider。生产入口注册四家 Provider。所有 Provider 只在内存中解密既有 KV 的 AK/SK；任何返回错误须是固定、脱敏的厂商/操作文本。

| Vendor | group 映射（只读） | ECS（只读） | RDS（只读，仅 MySQL） | Redis（只读） |
| --- | --- | --- | --- | --- |
| `alicloud` | Resource Manager `ListResourceGroups(PageNumber=1, PageSize=100)`，`Id -> DisplayName` | ECS `DescribeInstances(MaxResults=100)` | RDS `DescribeDBInstances(Engine=MySQL, MaxResults=100)` | R-KVStore `DescribeInstances(PageSize=50)` |
| `tencent_cloud` | DCDB `DescribeProjects`，`ProjectId -> Name` | CVM `DescribeInstances` | CDB `DescribeDBInstances` | Redis `DescribeInstances` |
| `huaweicloud` | EPS `ListEnterpriseProject`，`id -> name` | ECS `ListServersDetails` | RDS `ListInstances(datastore_type=MySQL)` | DCS `ListInstances` |
| `awscloud` | 固定 `{"1":"1"}`，AWS 无资源组 API | EC2 `DescribeInstances` | RDS `DescribeDBInstances` | ElastiCache `DescribeCacheClusters` |

同步层写入：

- `ConsulManager/assets/{vendor}/group/{account}`：`map[groupID]groupName`
- `ConsulManager/record/jobs/{vendor}/{account}/group`：`status`、`count`、`update`、`msg`

同步成功后 Creator 生成兼容旧任务格式的 group 任务和请求选择的 ECS/RDS/Redis 任务，写入 `ConsulManager/jobs/{id}` 并 `Upsert` 到调度器。任务格式为 `id`、`func`、`args`、`minutes`、`trigger=interval`、`replace_existing=true`。

资源任务执行时，`sync.Cloud` 调用 `Provider.ECS/RDS/Redis`，按 `vendor_account_type` 清理不再存在的旧 Consul Service，再注册最新实例，并写 `ConsulManager/record/jobs/{vendor}/{account}/{type}/{region}`。成功 record 为 `status=20000`、`count`、`on`、`off`、`update`、`msg`；任何凭据、SDK、分页、归一化、Consul 错误为 `status=50000` 的脱敏失败 record，返回 error 让调度器记录该次失败但继续后续任务。资源组失败 record 为 `status=50000,count=0`，账户创建不写任何任务 KV。

## 请求契约

沿用 `POST /api/jobs`：`job_dict` 包含 `dialogStatus=create`、`vendor`、`account`、`ak`、`sk`、`restype`、`region`、`proj_interval` 及各资源 interval；ECS 可选 `isextip`。vendor/account 非空、interval 为正、资源类型限于 group/ecs/rds/redis、region 仅能用于所选资源任务。

接口成功返回 `{code:20000,data:"...创建成功！"}`；校验、provider 或持久化失败返回现有业务错误 `{code:50000,...}`。未注入 Creator 的兼容服务器仍保持“尚未实现”响应。

## 依赖与边界

增加各厂商官方 Go SDK；Provider 维持 `Group/ECS/RDS/Redis` 契约，调度器通过既有 `func` 字符串路由。阿里云使用区域 endpoint，完整处理 ECS/RDS token 与 Redis page-number 分页；资源组 ID 由既有 sync 层转换为展示名。所有调用仅为列举/描述 API，禁止云端写操作。SDK 错误只作为脱敏操作名返回，AK/SK 不得被日志、响应或记录包装/输出。

## 测试

- `internal/cloud/*`：官方 SDK 调用参数、返回映射、凭据加载/API 错误和脱敏。
- `internal/sync`：fake registry provider 的成功与失败、KV/record 写入。
- `internal/accounts`：成功时任务及调度 Upsert；失败时没有任务。
- `internal/jobs`：四类任务的路由与单次失败不影响后续调度。
- `internal/httpapi`：携带认证的 create 请求贯通 fake provider、加密凭据、KV 和任务调度。

## VIDE-39 交付与验证规格

### 自动化与 Linux 门禁

- AWS、腾讯云、华为云 fixture 必须调用实际 Provider，覆盖 Group/ECS/RDS/Redis 正常响应、分页和 endpoint 错误。
- Provider→内存 Consul 链路必须断言 ECS/RDS/Redis Service 的名称、地址、端口、映射后的 `Meta.group`，并断言 endpoint 错误写入 `status=50000`。
- `SecretResolver` 必须覆盖加密 AK/SK 成功解密、缺失和非法密文失败，且输出不得包含凭据。
- Go 1.22 Linux 门禁固定为 `go mod tidy -go=1.22` 无差异、`go test ./...`、`go vet ./...`、`go build ./cmd/tensuns` 全部退出 0。

### 集成环境发布门禁

- 发布基线为远端 `main` 的 `689b05c536b266b15f02d710b0ef1b38385f141c`；API 启动日志应能定位同一提交，Web 产物应包含第三方创建跳转文案。
- `tensuns-go-consul` 与 `tensuns-go-api` 必须 healthy，`tensuns-go-web` 必须运行且首页返回 200；直连 API 与 Nginx 代理均运行完整 smoke。
- BUG-039-001 的浏览器路径只允许读取 `GET /api/edit/cloud?vendor=third_party` 并进入完整主机集合编辑器，不得发送 `POST /api/jobs` 或保存正式数据。
- 兼容旧页面的 `POST /api/jobs`（`dialogStatus=create`、`vendor=third_party`）必须返回 `code=40000` 明确提示，且不得新增 `ConsulManager/jobs/third_party/`。
- 发布前后比较 Consul KV 键集合和服务标签集合；允许调度器正常更新既有 record 的时间戳，但禁止新增、删除或改写本次范围外的正式数据。
- 回滚点必须包含发布前源码、配置、镜像、容器 inspect、Consul 快照和可执行回滚脚本；任一门禁失败立即恢复发布前版本。

### 验证边界

`10.0.10.236` 是集成测试与人工验收环境，不是生产环境。本阶段不使用三家非生产真实云账户；生产启用前仍需在隔离账户验证真实签名、IAM 权限、区域 endpoint、分页、限流/超时和回滚兼容性。

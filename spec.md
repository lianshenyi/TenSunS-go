# 技术规格：新增云账户资源组同步

## 架构

`httpapi` 将创建请求映射为 `accounts.Request` 并委托可注入的 `accounts.Creator`。Creator 加密并持久化 AK/SK，调用 `sync.Cloud.Group`；后者通过 `cloud.Registry` 取 Provider。生产入口注册 `alicloud` Provider：它从既有 KV 在内存中解密凭据，使用官方 `resourcemanager-20200331/v2` SDK 调用只读 `ListResourceGroups(PageSize=100)`，将 `Id -> DisplayName` 返回给同步层。同步层写入：

- `ConsulManager/assets/{vendor}/group/{account}`：`map[groupID]groupName`
- `ConsulManager/record/jobs/{vendor}/{account}/group`：`status`、`count`、`update`、`msg`

同步成功后 Creator 生成兼容旧任务格式的 group 任务和请求选择的 ECS/RDS/Redis 任务，写入 `ConsulManager/jobs/{id}` 并 `Upsert` 到调度器。任务格式为 `id`、`func`、`args`、`minutes`、`trigger=interval`、`replace_existing=true`。

## 请求契约

沿用 `POST /api/jobs`：`job_dict` 包含 `dialogStatus=create`、`vendor`、`account`、`ak`、`sk`、`restype`、`region`、`proj_interval` 及各资源 interval；ECS 可选 `isextip`。vendor/account 非空、interval 为正、资源类型限于 group/ecs/rds/redis、region 仅能用于所选资源任务。

接口成功返回 `{code:20000,data:"...创建成功！"}`；校验、provider 或持久化失败返回现有业务错误 `{code:50000,...}`。未注入 Creator 的兼容服务器仍保持“尚未实现”响应。

## 依赖与边界

增加阿里云官方 `github.com/alibabacloud-go/resourcemanager-20200331/v2` SDK。Provider 已有 `Group(context.Context, account) (map[string]string,error)` 契约；本期只消费该方法。只注册 `alicloud`，且不调用任何写云资源 API；其他 vendor 保持未注册失败。SDK 错误只作为带上下文的失败原因返回，AK/SK 不得被日志、响应或记录包装/输出。

## 测试

- `internal/cloud/alicloud`：官方 SDK 调用参数、返回映射、凭据加载/API 错误。
- `internal/sync`：fake registry provider 的成功与失败、KV/record 写入。
- `internal/accounts`：成功时任务及调度 Upsert；失败时没有任务。
- `internal/httpapi`：携带认证的 create 请求贯通 fake provider、加密凭据、KV 和任务调度。

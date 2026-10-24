# SPEC-001 四云 fixture 同步闭环

对应需求：PRD-001、PRD-002、PRD-003、PRD-004。

## 输入

fixture 位于 `ConsulManager/fixtures/{vendor}/{account}`；本阶段 vendor 为 `alicloud`、`tencent_cloud`、`huaweicloud`、`awscloud`。格式为 `groups`、`ecs`、`rds`、`redis` 四个可选字段；资源字段采用 `internal/cloud/model` 的 JSON 字段。`error` 为 `auth`、`rate_limit`、`timeout`、`temporary` 或 `http_500` 至 `http_599` 时，Provider 返回相应可分类错误；其他错误文本保持不可重试错误，绝不返回成功空集。

## 执行

1. `cloud.Registry` 找到任务 vendor 对应的 fixture Provider；Provider 只能读取该 vendor 的 fixture 命名空间。
2. 同步服务最多尝试三次：仅 `rate_limit`、临时错误和 context deadline 可重试，退避 10ms/20ms；鉴权失败立即停止。
3. 资源组写 `ConsulManager/assets/{vendor}/group/{account}`。
4. ECS/RDS/Redis 先筛出同一 service、region、account 的旧 Service，删除不再存在的 ID，随后按迁移基线 7.4 与 Python 元数据格式注册当前资源。自定义 IP/端口 KV 仍优先。
5. 成功写 `ConsulManager/record/jobs/{vendor}/{account}/{kind}[/{region}]`；失败写 `status=50000`、实际错误和失败时间，并保留已有 count/on/off。

## 一致性

Consul 不提供跨 Service 事务。任何删除或注册失败都使本轮失败并保留失败记录；下一次相同 fixture 会再次对账，因此操作幂等、可收敛。单任务由现有 Scheduler 的 job-ID 锁串行。

## 验收用例

- SPEC-001-01：四云各四类资源成功写入，group KV 与 ECS/RDS/Redis Service 的 ID、Name、Address、Port、Tags、全部 Meta、TCP check 和 record 满足第 7 节。
- SPEC-001-02：三家新增云的 `auth` fixture 写 50000 record 且不重试；限流/临时/5xx、超时最多三次后写失败 record。
- SPEC-001-03：一次注册失败写失败 record；重跑后 Service 集合收敛，且不会产生重复 ID。
- SPEC-001-04：Scheduler 加载四个任务，依次经 Runtime 与 fixture Provider 执行，并写出 group/ECS/RDS/Redis 四类成功 record。

## SPEC-002 JumpServer V2/V3 fixture 同步闭环

对应 PRD-005；唯一兼容基线为 `docs/迁移基线.md` 第 8 节。

1. 读取 node_id、全局及账户级 ecs_info、jms_info 和 Consul ECS Service；解密 Token，使用 `Authorization: Token <token>`。
2. V3 写 `/assets/hosts/` 的 address/name/accounts/platform；V2 写 `/assets/assets/` 的 ip/hostname/admin_user/platform。两者均建立缺失资源组（含未分组），按 IP 删除缺失资产，且只在名称或节点变化时通过 `PUT /api/v1/assets/{id}/` 更新。
3. 429、5xx 和 deadline 最多三次；401/403 不重试。错误写 `record/jms/{vendor}/{account}` 的 50000；部分写入后的下一次同步必须收敛。
4. 成功记录使用 `%m%d/%H:%M` 和父节点资产总数。Scheduler 的 `sync_jms.run` 和手动 `/api/jms/sync` 调用同一客户端；真实凭据联调不属于仓库测试。
5. `custom_ecs_info` 优先采用账户级非空配置；其 JSON 键按原始声明顺序作大小写不敏感子串匹配，后命中项覆盖前项。同步随后删除名称不匹配的重复 IP 资产，以及 V3 `meta.node.id` 或旧版 `meta.data.id` 标识的 `" (0)"` 空子节点。
6. V2、V3 各自覆盖正常路径、账户级有序覆盖、重复/空对象边界、单项失败后的重跑收敛及再次运行幂等。
7. 严格 HTTP fixture 必须拒绝非 `PUT /api/v1/assets/{id}/` 更新路径，并断言 V2/V3 各自更新 payload。429、deadline 和 403 分别断言调用次数（前三者为三次、403 为一次）及最终 `50000` record。手动 `/api/jms/sync` 与 Scheduler→Runtime 均以真实 `jumpserver.Client` 运行，并覆盖 `20000`、`50000` record。

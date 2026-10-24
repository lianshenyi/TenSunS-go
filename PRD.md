# PRD-001 多云 fixture 资源同步

## 目标

在不接入真实云凭据的前提下，以脱敏 fixture 验证阿里云、腾讯云、华为云与 AWS 的资源组、ECS、MySQL RDS 与 Redis 从 Provider 到 Consul 的可重复同步闭环。

## 范围

- PRD-001：Provider 从各自命名空间读取脱敏 fixture，并输出迁移基线 7.2 定义的统一资源模型。
- PRD-002：将资源组写入既有 group KV；将资源写入既有 Consul Service 命名、Tags、Meta 和 TCP check 格式。
- PRD-003：调度任务执行后写入既有 record KV；失败保留上次数量但使用 `status=50000`。
- PRD-004：429、5xx 和超时有限重试；鉴权错误不重试；重复执行收敛到相同 Service 集合。

## 非目标

不接入任意云 SDK 或真实 AK/SK；不实现 JumpServer、写 API 或改变第 8 节 JumpServer 行为。

## 验收

以 `docs/迁移基线.md` 第 7 节为资源模型与 Consul 契约唯一基线。四云 fixture 覆盖成功、鉴权失败、限流/临时/5xx、超时、部分写入失败和幂等重试；Scheduler→Runtime→fixture 的端到端路径写出四类 record。

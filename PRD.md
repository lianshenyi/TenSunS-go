# PRD-001 多云 fixture 资源同步

## 目标

在不接入真实云凭据的前提下，以脱敏 fixture 验证阿里云、腾讯云、华为云与 AWS 的资源组、ECS、MySQL RDS 与 Redis 从 Provider 到 Consul 的可重复同步闭环。

## 范围

- PRD-001：Provider 从各自命名空间读取脱敏 fixture，并输出迁移基线 7.2 定义的统一资源模型。
- PRD-002：将资源组写入既有 group KV；将资源写入既有 Consul Service 命名、Tags、Meta 和 TCP check 格式。
- PRD-003：调度任务执行后写入既有 record KV；失败保留上次数量但使用 `status=50000`。
- PRD-004：429、5xx 和超时有限重试；鉴权错误不重试；重复执行收敛到相同 Service 集合。
- PRD-005：按第 8 节从 Consul 读取 JumpServer V2/V3 配置、ECS Service 与账户级覆盖，使用脱敏 HTTP fixture 完成节点和资产对账。

## 非目标

不接入任意云 SDK/AKSK 或真实 JumpServer 凭据；真实 JumpServer 联调是外部环境验收边界。

## 验收

资源同步以第 7 节为基线，JumpServer 以第 8 节为唯一兼容基线。V2/V3 fixture 覆盖 Token 鉴权、节点/资产对账、临时/5xx 重试、失败 record、幂等重跑与 Scheduler→Runtime 端到端。

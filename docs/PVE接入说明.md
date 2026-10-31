# Proxmox VE 接入与同步

本期支持 `vendor=pve`，展示名为 `Proxmox VE`，仅发现 QEMU VM。接入地址必须是完整 HTTPS API 基地址，例如 `https://pve.example:8006/api2/json`。使用独立只读服务账号的 privsep API Token，不支持长期保存账号密码。客户端只接受标准 `*http.Transport`，拒绝明文 HTTP、`InsecureSkipVerify=true` 和不可审计的自定义 transport；因此生产环境必须配置受信任 CA 与可匹配的主机名。

将以下 JSON 写入 `ConsulManager/assets/pve/api/{account}`。`token_secret` 必须使用项目现有 AES-256-CBC 加密；不得提交、记录或回显明文 Token。

```json
{"api_base_url":"https://pve.example:8006/api2/json","token_id":"reader@pve!tensuns","token_secret":"<AES encrypted>","cluster":"lab-cluster","ip_selection":{"nic_priority":["ens18"],"cidr_priority":["10.0.0.0/8"]}}
```

任务沿用既有格式：资源组 `pve/{account}/group`，ECS `pve/{account}/ecs/{cluster}`，JMS `pve/{account}/jms`。`region` 是集群名，VM 所在 PVE 节点写入 ECS Meta 的 `node` 与 `group`。

IP 仅选择非 loopback、非 link-local 的 IPv4；依次按 `cidr_priority`、`nic_priority`、字典序选取。账户/VM 级静态覆盖仍使用 `ConsulManager/assets/sync_ecs_custom/{iid}`。VM 必须运行、已启用 QEMU Guest Agent，且客体系统的 `qemu-guest-agent` 正常运行；缺失、超时或没有合格 IP 只在执行记录标注 `ip_unavailable`，不会使整个同步失败，也不会删除原有服务。

启用 JumpServer 时，配置既有 `jms_info`、`ecs_info` 和 `ConsulManager/jms/pve/{account}/node_id`，再创建 JMS 任务。同步器优先读取账户级 `ConsulManager/jms/pve/{account}/ecs_info`；该值不存在或为空时回退全局 `ConsulManager/jms/ecs_info`。V2 使用 `protocols` 与 `admin_user`，V3 使用协议对象（含固定的同端口 `sftp`）与账号模板 `accounts:[{"template":"..."}]`；Token 仍只从加密的 `jms_info.token` 读取。

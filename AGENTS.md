# AGENTS.md

本文件记录 TenSunS-go 的二次开发约定、常用命令与部署验证结果，供后续开发者和自动化 Agent 使用。

## 1. 项目定位

- 本仓库是 [TenSunS](https://github.com/starsliao/TenSunS) 的二次开发版本，不是上游仓库。
- 目标范围：多云资源（分组、ECS、云 MySQL、云 Redis）同步到 Consul，并把 ECS 同步到 JumpServer。
- 当前后端正在从 Flask 迁移到 Go；`flask-consul/` 仅作为行为对照保留。
- 迁移契约以 [docs/迁移基线.md](docs/迁移基线.md) 为准，当前完成度见 [docs/Go迁移进度.md](docs/Go迁移进度.md)。

## 2. 当前实现边界

已实现：

- Go 配置加载、Consul 连通性检查与 skey 初始化。
- 与 Python 兼容的 AES-256-CBC 和 itsdangerous HS512 Token。
- Consul KV / 服务注册 / 健康检查封装。
- 动态 interval 调度器、任务加载、任务解析。
- 登录、用户信息、任务列表、云账户、云资源列表、JumpServer 列表/配置等读接口。
- `cmd/consulmock` 内存 Consul 与 `scripts/smoke.sh` 兼容性冒烟测试。

尚未实现：

- 阿里云、腾讯云、华为云、AWS 的 Go 同步适配器。
- 同步结果写回 Consul。
- JumpServer Go 客户端。
- 新建/运行任务、云账户提交、自定义实例保存、JumpServer 首次同步等写接口。

未实现部分会返回业务码 `50000` 或暂不提供；在适配器接入前，不要用 Go 进程接管生产同步任务。

## 3. 仓库结构

```text
cmd/tensuns/        Go 后端入口
cmd/consulmock/     内存 Consul 测试服务
internal/           配置、鉴权、加密、Consul、调度、HTTP API
scripts/            冒烟测试、演示数据灌入
vue-consul/         Vue 2 前端
flask-consul/       Python 迁移参考实现
deploy/consul/      Compose 用的 Consul 配置模板与 ACL 引导脚本
docs/               迁移基线与使用文档
install/            旧 Flask 部署脚本，仅历史参考，不适用于 Go
docker-compose.yml  Consul + Go 后端 + Vue 前端 一键编排
.env.example        Compose 环境变量模板（复制为 .env 后使用）
```

## 4. 本地开发命令

```bash
make test       # go test ./...
make smoke      # 内存 Consul + Go 后端 + 附录 B 冒烟测试
make build      # 构建 bin/tensuns 和 bin/consulmock
make image      # 构建后端镜像 tensuns-go:latest
make web-image  # 构建前端镜像 tensuns-go-web:latest
make web-dev    # 启动 Vue 本地开发服务，/api 默认代理到 127.0.0.1:2026
```

Docker Compose 一键启动（Consul + Go 后端 + Vue 前端）：

```bash
make env-init   # 从 .env.example 生成 .env（已存在则不覆盖）
make up         # docker compose up -d --build，然后打印 docker compose ps
make ps         # 查看服务状态
make logs       # 跟踪全部服务日志
make down       # 停止并移除容器（保留数据卷）
```

`make up` 之前需要先编辑 `.env`：至少修改 `consul_management_token`、`consul_token`、`admin_passwd`。
`consul_token` 必须与 `consul_management_token` 完全一致——Consul ACL 默认策略是 `deny`，
不一致会让后端启动时的连通性检查直接失败。

`make web-dev` 可通过 `TENSUNS_API` 覆盖后端地址，例如 `TENSUNS_API=http://10.0.10.236:2026 make web-dev`。

Go 代码要求：

- 使用标准库优先，保持无第三方 Go 依赖（当前 `go.mod` 无 `require`）。
- 修改 API 响应、字段名、状态码、时间格式前，先查 `docs/迁移基线.md` 并同步更新冒烟测试。
- AES 格式为 `Base64(IV + AES-256-CBC(PKCS#7(plaintext)))`；Token 为 HS512 JWS，有效期 8 小时。
- Consul 任务 KV 的 `func`、`args`、`id` 必须兼容 Python 版本，禁止随意改名。

## 5. 部署验证记录

验证日期：2026-10-23。

验证主机：

- 地址：`10.0.10.236`
- 系统：Rocky Linux 9.4
- Docker：28.0.4
- 部署目录：`/opt/tensuns-go`
- 仓库：`https://github.com/lianshenyi/TenSunS-go.git`
- 验证提交：`a02a102`（初始提交）

运行状态：

| 组件 | 容器/路径 | 端口/说明 |
| --- | --- | --- |
| Consul 1.19.2 | `tensuns-go-consul` | 仅本机 `127.0.0.1:18500 -> 8500`，数据卷 `tensuns-go-consul-data` |
| Go 后端 | `tensuns-go-api` | `0.0.0.0:2026 -> 2026`，镜像 `tensuns-go:verify` |
| Vue 前端 | `tensuns-go-web` | `0.0.0.0:1026 -> 1026`，镜像 `tensuns-go-web:verify`，`/api/` 代理到后端 |
| 验证环境变量 | `/opt/tensuns-go-verify/tensuns.env` | 权限 600 |
| 登录密码 | `/opt/tensuns-go-verify/admin_passwd` | 权限 600，不写入仓库 |
| 演示数据脚本 | `/opt/tensuns-go-verify/seed_demo.py` | 向真实 Consul 写入 14 个 KV、4 个服务 |

部署和验证命令：

```bash
# 服务器上构建
cd /opt/tensuns-go
git pull --ff-only
docker build -t tensuns-go:verify .

# 启动/重建后端
docker rm -f tensuns-go-api 2>/dev/null || true
docker run -d --name tensuns-go-api --network tensuns-go-net \
  -p 0.0.0.0:2026:2026 \
  --env-file /opt/tensuns-go-verify/tensuns.env \
  tensuns-go:verify

# 构建/启动前端（本机 Docker Hub 镜像不可达，使用可访问的基础镜像）
docker build \
  --build-arg NODE_IMAGE=registry.cn-shenzhen.aliyuncs.com/starsl/node:14-alpine \
  --build-arg NGINX_IMAGE=registry.cn-shenzhen.aliyuncs.com/starsl/nginx:1.24.0-alpine \
  -t tensuns-go-web:verify vue-consul
docker rm -f tensuns-go-web 2>/dev/null || true
docker run -d --name tensuns-go-web --network tensuns-go-net \
  -p 0.0.0.0:1026:1026 tensuns-go-web:verify

# 本机执行兼容性冒烟测试。仅在调度器启用时校验 nextime。
PW=$(ssh root@10.0.10.236 'cat /opt/tensuns-go-verify/admin_passwd')
python3 scripts/smoke.py http://10.0.10.236:2026 "$PW"
```

本次验证结果：

- Dockerfile 在目标机成功构建，Go 二进制以非 root 用户运行。
- 后端成功连接真实 Consul，读取 skey、任务与演示资源。
- 冒烟测试通过：`PASS: login, info, jobs, edit/cloud, nodes/res, jms/list, jms/config`。
- Consul 中确认生成 `ConsulManager/assets/secret/skey`，并有 14 个 `ConsulManager/` KV、4 个演示服务。
- 冒烟阶段临时启用调度器后，5 个任务成功加载并计算 `nextime`；当前 Cloud/JMS 适配器尚未接入，任务执行会报未接线错误。
- 前端 `http://10.0.10.236:1026` 返回 200；静态 JS 与 gzip 资源正常；经 Nginx 代理的登录接口返回 `20000`，`/api/jobs` 返回 4 个云任务。
- Nginx 使用 Docker DNS 动态解析后端；重建 `tensuns-go-api` 后无需重启前端，代理仍可正常登录。

当前保留状态：

- `tensuns-go-api` 最终以 `scheduler_disabled=1` 运行，避免未接线的任务反复报错。
- `tensuns-go-web` 保持运行，访问地址为 `http://10.0.10.236:1026`；容器需先启动后端，因为 Nginx 在启动时解析 `tensuns-go-api`。
- 因此最终状态下 `/api/jobs` 的 `nextime` 为 `无`，这是预期行为，不是回归。
- 重新做冒烟验证时，可临时去掉 env 文件中的 `scheduler_disabled=1` 后重启后端；验证完成后再恢复。
- 演示数据中的 JumpServer 地址和 Token 仅用于测试，不是真实凭据。

### 5.1 Docker Compose 一键启动验证

验证日期：2026-10-25。

验证主机：`10.0.10.236`（Rocky Linux 9.4，Docker 29.7.2，Compose v5.5.0），部署目录 `/opt/tensuns-go`，
验证提交 `eb52fcb`（在 `119b8cc` 基础上新增 Compose 编排、`.env.example`、`deploy/consul/`）。

编排文件：`docker-compose.yml`，三个服务：

| 服务 | 镜像 | 端口 | 说明 |
| --- | --- | --- | --- |
| `consul` | `hashicorp/consul:1.19.2` | `127.0.0.1:18500 -> 8500` | server 模式，ACL 开启，配置由 `deploy/consul/init.sh` 渲染 |
| `api` | `tensuns-go:latest`（本地构建） | `0.0.0.0:2026 -> 2026` | Go 后端，`depends_on: consul (service_healthy)` |
| `web` | `tensuns-go-web:latest`（本地构建） | `0.0.0.0:1026 -> 1026` | Nginx + Vue SPA，`depends_on: api (service_healthy)` |

关键设计：

- Consul ACL 引导由 `deploy/consul/init.sh` 完成：脚本把 `CONSUL_MANAGEMENT_TOKEN` 写进
  渲染出的 `consul.hcl`（`initial_management` + `agent`），因此**全新环境无需手动
  `consul acl bootstrap`**，`docker compose up` 即可用。
- 后端以网络别名 `tensuns-go-api` 注册，与 `vue-consul/http-ops.conf` 里的
  `http://tensuns-go-api:2026` 对应；Nginx 用 Docker DNS 动态解析，后端重建后前端无需重启。
- Consul 只绑定回环地址，不对公网暴露；API 与前端按需绑定 `0.0.0.0`。
- 敏感配置全部走 `.env`（已 git-ignore），仓库里只有 `.env.example`。

验证结果：

- `docker compose config` 通过。
- `docker compose down` 后重新 `docker compose up -d --build` 成功，耗时 2m03s，
  两个镜像均从本地源码构建；启动顺序按 healthcheck 自动排布（consul healthy → api healthy → web）。
- 三个容器最终状态：`tensuns-go-consul`（healthy）、`tensuns-go-api`（healthy）、`tensuns-go-web`（up）。
- 登录接口经 Nginx 代理与直连后端均返回 `{"code":20000,...}`。
- 临时启用调度器（`scheduler_disabled=0`）后，冒烟测试通过：
  `PASS: login, info, jobs, edit/cloud, nodes/res, jms/list, jms/config`。
- Consul 中确认 14 个种子 KV + 后端写入的 `skey`（共 15 个 KV）与 4 个演示服务。
- 外网访问 `http://10.0.10.236:1026` 返回 200；`/static/js/app.*.js` 返回 200 且 gzip 生效
  （209524 → 73169 字节）。
- 本次验证已恢复默认 `scheduler_disabled=1`，最终 `/api/jobs` 的 `nextime` 为 `无`，属预期行为。

踩坑记录：

- 读 Consul KV 时必须带 `X-Consul-Token` 请求头。`CONSUL_HTTP_TOKEN` 这个环境变量 curl 并不认，
  用它带 `curl` 会退化成匿名请求；ACL 默认 `deny` 下匿名读 KV 返回空数组（HTTP 200，容易误判成"没数据"）。
  正确写法：`curl -H "X-Consul-Token: $consul_token" http://127.0.0.1:18500/v1/kv/?keys`。
- 冒烟脚本 `scripts/smoke.py` 会硬断言 `nextime` 是时间格式，调度器关闭时值为 `无`，
  因此**必须在启用调度器的前提下跑冒烟**。

后续可做（本次未执行）：

- 镜像推送到 `ccr.ccs.tencentyun.com/devops-sre` 并做远端 digest 校验。
- 生产域名 / TLS 反代。

## 6. 安全与提交约定

- 禁止提交 `ConsulManager/assets/secret/skey`、`bak-skey*`、真实云厂商 AK/SK、JumpServer Token、`.env` 或部署密码。
- 部署密码保存在目标机 `/opt/tensuns-go-verify/`，不要复制到仓库或提交历史。
- 不要在 Go 适配器完成前把调度器用于生产；当前 `jobs.Runtime` 的 Cloud/JMS 依赖为空。
- `install/` 下的脚本仍指向旧 Flask 镜像，Go 阶段使用根目录 `Dockerfile` / `make image`，前端使用 `vue-consul/Dockerfile` / `make web-image`。
- 保留 `LICENSE` 与上游来源说明；可以继续清理上游品牌链接，但不要删除许可证。

## 7. 后续优先级

1. 实现四家云厂商的 Go 适配器，并补真实 Consul 写入。
2. 实现 JumpServer V2/V3 客户端与同步流程。
3. 补齐任务新建/立即运行、云账户提交、自定义实例写接口。
4. 为写接口和同步流程补充集成测试，再考虑启用生产调度。

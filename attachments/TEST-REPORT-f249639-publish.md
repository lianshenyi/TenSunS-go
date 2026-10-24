# 构建 / 分发 / 部署验收报告 — TenSunS-go

- **议题**：VIDE-36 确认其他后端代码的重构完成情况（阶段一至三已由 QA 验收通过）
- **触发**：用户「推送下镜像」
- **日期**：2026-10-24
- **基准提交**：`f2496396e9f0acaafce329fa63f1a4d2e35d6f5a`（`fix(jumpserver): verify canonical asset updates`），分支 `agent/go/vide-36`
- **构建机**：10.0.10.236（Docker 28.0.4，buildx v0.36.1）
- **反代机**：10.0.10.80（nginx 1.20.1）

---

## 1. 同步代码

**通过**

```bash
# 本地：从已验收提交导出纯净源码树
cd /Users/kylian/git/work/TenSunS
git archive f2496396e9f0acaafce329fa63f1a4d2e35d6f5a | tar -x -C /tmp/tensuns-release-f249639

# 同步到构建机
rsync -az --delete --exclude='.git' --exclude='.multica' \
  /tmp/tensuns-release-f249639/ root@10.0.10.236:/opt/tensuns-go-build-f249639/
```

导出树 191 个文件、1.3 MB。`git archive` 保证不含 `.git`、`.multica/` 与任何本地未提交改动。

## 2. 容器构建

**通过**

```bash
# 后端
ssh root@10.0.10.236 'cd /opt/tensuns-go-build-f249639 && \
  docker build \
    --build-arg VERSION=f2496396e9f0 \
    --build-arg COMMIT=f2496396e9f0acaafce329fa63f1a4d2e35d6f5a \
    --build-arg BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
    -t tensuns-go:git-f2496396e9f0 .'

# 前端（本机 Docker Hub 不可达，沿用可访问的阿里云镜像）
ssh root@10.0.10.236 'cd /opt/tensuns-go-build-f249639 && \
  docker build \
    --build-arg NODE_IMAGE=registry.cn-shenzhen.aliyuncs.com/starsl/node:14-alpine \
    --build-arg NGINX_IMAGE=registry.cn-shenzhen.aliyuncs.com/starsl/nginx:1.24.0-alpine \
    -t tensuns-go-web:git-f2496396e9f0 vue-consul'
```

| 镜像 | 大小 | 备注 |
| --- | --- | --- |
| `tensuns-go:git-f2496396e9f0` | 24.1 MB | 非 root 用户 `tensuns`，`EXPOSE 2026`，内嵌 version/commit/build_date |
| `tensuns-go-web:git-f2496396e9f0` | 64.9 MB | Nginx 静态站点 + `/api/` 反代 |

后端镜像启动验证（`docker run --rm --entrypoint /usr/local/bin/tensuns`）确认嵌入版本正确：

```
level=INFO msg="starting TenSunS Go backend" version=f2496396e9f0 \
  commit=f2496396e9f0acaafce329fa63f1a4d2e35d6f5a built=2026-10-24T04:57:23Z
```

## 3. 本地回归

**通过**（在 `/tmp/tensuns-release-f249639` 执行）

```
go test ./...     # 全部 ok，无 FAIL
```

```
ok  github.com/lianshenyi/TenSunS-go/internal/cloud/alicloud  0.182s
ok  github.com/lianshenyi/TenSunS-go/internal/config             0.278s
ok  github.com/lianshenyi/TenSunS-go/internal/cryptox            0.375s
ok  github.com/lianshenyi/TenSunS-go/internal/httpapi            0.816s
ok  github.com/lianshenyi/TenSunS-go/internal/jobs               1.021s
ok  github.com/lianshenyi/TenSunS-go/internal/jumpserver         1.739s
ok  github.com/lianshenyi/TenSunS-go/internal/scheduler          2.717s
ok  github.com/lianshenyi/TenSunS-go/internal/store/consul       2.232s
ok  github.com/lianshenyi/TenSunS-go/internal/sync              3.867s
```

## 4. 部署到 10.0.10.236

**通过**

```bash
# 后端
ssh root@10.0.10.236 'docker rm -f tensuns-go-api 2>/dev/null || true
docker run -d --name tensuns-go-api --network tensuns-go-net \
  -p 0.0.0.0:2026:2026 \
  --env-file /opt/tensuns-go-verify/tensuns.env \
  tensuns-go:git-f2496396e9f0'

# 前端
ssh root@10.0.10.236 'docker rm -f tensuns-go-web 2>/dev/null || true
docker run -d --name tensuns-go-web --network tensuns-go-net \
  -p 0.0.0.0:1026:1026 tensuns-go-web:git-f2496396e9f0'
```

改动前已备份原容器规格：`/opt/tensuns-go-verify/tensuns-go-api.inspect.bak.1792817990`、`tensuns-go-web.inspect.bak.1792817990`。

启动日志：

```
level=INFO msg="starting TenSunS Go backend" version=f2496396e9f0 commit=f2496396e9f0acaafce329fa63f1a4d2e35d6f5a ...
level=INFO msg="consul authentication and connectivity verified"
level=WARN msg="scheduler is disabled; existing tasks will not run"
level=INFO msg="HTTP API listening" addr=0.0.0.0:2026
```

容器状态：

```
tensuns-go-api    tensuns-go:git-f2496396e9f0       Up  0.0.0.0:2026->2026/tcp
tensuns-go-web    tensuns-go-web:git-f2496396e9f0  Up  0.0.0.0:1026->1026/tcp
tensuns-go-consul hashicorp/consul:1.19.2         Up  127.0.0.1:18500->8500/tcp
```

## 5. 兼容性冒烟测试

**通过**

按 `AGENTS.md` 约定，冒烟测试需临时启用调度器以校验 `nextime`。已备份 env 并在验证后恢复：

```bash
ssh root@10.0.10.236 'cp /opt/tensuns-go-verify/tensuns.env /opt/tensuns-go-verify/tensuns.env.bak.1792818056
sed -i "s/^scheduler_disabled=1/scheduler_disabled=0/" /opt/tensuns-go-verify/tensuns.env
# 重启后端 → 5 个任务成功加载
```

调度器启用后的任务加载日志：

```
level=INFO msg="job scheduled" id=alicloud/main/jms minutes=3 next=10.24/13:04
level=INFO msg="job scheduled" id=alicloud/main/ecs/cn-hangzhou minutes=10 next=10.24/13:11
level=INFO msg="job scheduled" id=alicloud/main/group minutes=60 next=10.24/14:01
level=INFO msg="job scheduled" id=alicloud/main/rds/cn-hangzhou minutes=20 next=10.24/13:21
level=INFO msg="job scheduled" id=alicloud/main/redis/cn-hangzhou minutes=20 next=10.24/13:21
level=INFO msg="tasks loaded from Consul" loaded=5 skipped=0
level=INFO msg="cloud runtime enabled" providers="[alicloud tencent_cloud huaweicloud awscloud]"
```

冒烟结果（针对真实 Consul 与部署容器）：

```
$ python3 scripts/smoke.py http://10.0.10.236:2026 "$PW"
PASS: login, info, jobs, edit/cloud, nodes/res, jms/list, jms/config
```

验证完成后已恢复 `scheduler_disabled=1` 并重启后端。

## 6. nginx 反代与域名访问

**通过**

`/etc/nginx/site-enable/` 下新增独立站点文件，未改动既有 `kylianlian.com.site`：

```bash
scp tensuns-go.kylianlian.com.conf root@10.0.10.80:/tmp/tensuns-go.kylianlian.com.site
ssh root@10.0.10.80 'install -m 644 /tmp/tensuns-go.kylianlian.com.site \
  /etc/nginx/site-enable/tensuns-go.kylianlian.com.site
nginx -t && nginx -s reload'
```

`nginx -t` 输出：

```
nginx: the configuration file /etc/nginx/nginx.conf syntax is ok
nginx: configuration file /etc/nginx/nginx.conf test is successful
```

域名验证（`tensuns-go.kylianlian.com`，DNS 已指向 10.0.10.80，泛域名证书已覆盖）：

| 检查项 | 结果 |
| --- | --- |
| `https://tensuns-go.kylianlian.com/` | 200，`<title>TenSunS</title>`，4851 bytes |
| `http://` → `https://` | 301 |
| `POST /api/user/login` | `code=20000`，返回 `Bearer` token |
| `GET /api/jobs?query_dict=...` | `code=20000`，`all_jobs=4` |
| 静态资源 | `/static/js/chunk-vuejs.*.js` 等正常返回 |

## 7. 镜像仓库发布

**通过**

仓库：`ccr.ccs.tencentyun.com`，命名空间 `devops-sre`。使用目标机已有的登录态，未重新登录、未读取或输出凭据。

| 镜像 | 标签 | 远端 digest |
| --- | --- | --- |
| `devops-sre/tensuns-go-backend` | `git-f2496396e9f0` | `sha256:c003a588ebba768254e5f3c9aa514e331df6ea3d48b6997a310f66c0bc8e6dab` |
| `devops-sre/tensuns-go-frontend` | `git-f2496396e9f0` | `sha256:761afc07f71437b6e67ce1db77b3a501db37b9470221d0e459f6e1f46bd87c66` |

标签为不可变标签（`git-<commit-sha12>`），未推 `latest`。

digest 核对 —— `docker buildx imagetools inspect` 的 index digest 与本地 `docker inspect --format '{{.Id}}'` 完全一致：

```
backend : remote sha256:c003a588ebba768254e5f3c9aa514e331df6ea3d48b6997a310f66c0bc8e6dab
          local  sha256:c003a588ebba768254e5f3c9aa514e331df6ea3d48b6997a310f66c0bc8e6dab
frontend: remote sha256:761afc07f71437b6e67ce1db77b3a501db37b9470221d0e459f6e1f46bd87c66
          local  sha256:761afc07f71437b6e67ce1db77b3a501db37b9470221d0e459f6e1f46bd87c66
```

按 digest 试拉成功：

```
$ docker pull ccr.ccs.tencentyun.com/devops-sre/tensuns-go-backend@sha256:c003a588...
Status: Image is up to date
$ docker pull ccr.ccs.tencentyun.com/devops-sre/tensuns-go-frontend@sha256:761afc07...
Status: Image is up to date
```

## 8. 未验证 / 边界

- **真实云 SDK/AKSK 联调**未执行：本次构建的镜像使用 fixture/mock Provider，真实凭据联调仍属外部环境验收边界。
- **真实 JumpServer 凭据联调**未执行：仓库测试使用脱敏 `httptest` fixture。
- **`scheduler_disabled=1`** 为最终保留状态：真实云 Provider 与真实 JumpServer 凭据未在生产验证前，不启用调度器，避免未接线的任务反复报错。因此最终状态下 `/api/jobs` 的 `nextime` 为 `无`，属预期行为。
- 演示数据中的 JumpServer 地址与 Token 仅为测试数据，不是真实凭据。
- 未在仓库中新增任何凭据、`.env` 或 `skey`；部署密码仍只保存在目标机 `/opt/tensuns-go-verify/`。

## 9. 回滚

```bash
# 容器侧
ssh root@10.0.10.236 'docker rm -f tensuns-go-api tensuns-go-web
docker run -d --name tensuns-go-api --network tensuns-go-net -p 0.0.0.0:2026:2026 \
  --env-file /opt/tensuns-go-verify/tensuns.env tensuns-go:visual-20261023-r4
docker run -d --name tensuns-go-web --network tensuns-go-net -p 0.0.0.0:1026:1026 \
  tensuns-go-web:visual-20261023-r4'

# nginx 侧
ssh root@10.0.10.80 'rm /etc/nginx/site-enable/tensuns-go.kylianlian.com.site && nginx -t && nginx -s reload'
```

回滚不删除镜像，可快速恢复。

## 10. 汇总

| 步骤 | 状态 |
| --- | --- |
| 同步代码（`git archive` 纯净导出） | 通过 |
| 后端镜像构建 | 通过 |
| 前端镜像构建 | 通过 |
| 本地 `go test ./...` | 通过 |
| 部署到 10.0.10.236（后端 + 前端） | 通过 |
| 兼容性冒烟测试（真实 Consul） | 通过 |
| nginx 反代 `nginx -t` + reload | 通过 |
| 域名访问验证 | 通过 |
| 镜像推送 + digest 核对 + 按 digest 试拉 | 通过 |
| 真实云 / JumpServer 凭据联调 | 未验证（既定外部边界） |

## 11. 独立复核（2026-10-24，构建分发助手复核）

因上游委派任务 `01a16c52-6967-72a6-9a36-5fb88855b346` 因 provider 配额耗尽中断，交付物在本轮独立复核，不依赖原报告的自述。

复核命令与结果：

```bash
# 远端 digest 与本地 image id 一致
ssh root@10.0.10.236 'docker buildx imagetools inspect ccr.ccs.tencentyun.com/devops-sre/tensuns-go-backend:git-f2496396e9f0'
# -> Digest: sha256:c003a588ebba768254e5f3c9aa514e331df6ea3d48b6997a310f66c0bc8e6dab
ssh root@10.0.10.236 'docker inspect ccr.ccs.tencentyun.com/devops-sre/tensuns-go-backend:git-f2496396e9f0 --format "{{.Id}}"'
# -> sha256:c003a588ebba768254e5f3c9aa514e331df6ea3d48b6997a310f66c0bc8e6dab  (一致)

# 按 digest 试拉
ssh root@10.0.10.236 'docker pull ccr.ccs.tencentyun.com/devops-sre/tensuns-go-backend@sha256:c003a588...'
# -> Status: Image is up to date
ssh root@10.0.10.236 'docker pull ccr.ccs.tencentyun.com/devops-sre/tensuns-go-frontend@sha256:761afc07...'
# -> Status: Image is up to date

# 未推 latest
ssh root@10.0.10.236 'docker pull ccr.ccs.tencentyun.com/devops-sre/tensuns-go-backend:latest'
# -> Error response from daemon: ... not found

# 前端镜像内容确实来自 f249639：在 /opt/tensuns-go-build-f249639 重新构建
ssh root@10.0.10.236 'cd /opt/tensuns-go-build-f249639 && docker build \
  --build-arg NODE_IMAGE=registry.cn-shenzhen.aliyuncs.com/starsl/node:14-alpine \
  --build-arg NGINX_IMAGE=registry.cn-shenzhen.aliyuncs.com/starsl/nginx:1.24.0-alpine \
  -t tensuns-go-web:probe-f249639 vue-consul'
# -> 导出的 amd64 manifest sha256:d04bb357a6b2c787f8db4edda203de9fd661d152135c5fde435f5462d555c88c
#    与仓库中 frontend:git-f2496396e9f0 的 linux/amd64 manifest 完全一致
#    （镜像 Created 时间戳沿用 10-23 的构建缓存层，故与首次构建相同）

# 后端镜像版本内嵌正确
ssh root@10.0.10.236 'docker logs tensuns-go-api | grep "starting TenSunS"'
# -> version=f2496396e9f0 commit=f2496396e9f0acaafce329fa63f1a4d2e35d6f5a built=2026-10-24T04:57:23Z

# 非 root 运行
ssh root@10.0.10.236 'docker exec tensuns-go-api id'
# -> uid=100(tensuns) gid=101(tensuns)

# nginx 配置与仓库中 deploy/ 副本 md5 一致，且配置整体合法
ssh root@10.0.10.80 'md5sum /etc/nginx/site-enable/tensuns-go.kylianlian.com.site'
# -> a0111913e0fa8b76e43880d5d44c57d6
git show agent/go/vide-36:deploy/tensuns-go.kylianlian.com.site | md5sum
# -> a0111913e0fa8b76e43880d5d44c57d6  (一致)
ssh root@10.0.10.80 'nginx -t'
# -> test is successful

# 域名验证
curl -sS -o /dev/null -w "%{http_code}\n" https://tensuns-go.kylianlian.com/   # 200, <title>TenSunS</title>
curl -sSI http://tensuns-go.kylianlian.com/                                   # 301 -> https
curl -sS https://tensuns-go.kylianlian.com/static/js/app.3aa07f65.js          # 200, 209524 bytes
# 登录与任务列表（经域名）
# -> /api/user/login code=20000 ; /api/jobs code=20000, 4 个任务

# 代码回归（f249639 干净检出）
go build ./... && go vet ./... && go test ./...      # 全部通过
./scripts/smoke.sh                                   # PASS: login, info, jobs, edit/cloud, nodes/res, jms/list, jms/config
```

复核结论：

- 上表第 10 节「通过」结论经独立复跑全部成立；前端镜像大小原记为 64.3 MB，实际为 64.9 MB，已在本文件第 2 节更正。
- `scheduler_disabled=1` 保留为最终状态，故 `/api/jobs` 的 `nextime` 为 `无`，属预期行为。
- 真实云 SDK/AKSK 与真实 JumpServer 凭据联调仍未执行，属既定外部环境验收边界。


已在目标机 `10.0.10.236` 完成前端镜像切换，访问地址：`http://10.0.10.236:1026`。仅替换 `tensuns-go-web`；API、Consul、既有网络和其他容器未改动。

- 正式容器：`tensuns-go-web:visual-20261023-r2`，状态 `Up`，端口 `0.0.0.0:1026->1026`；首页 HTTP 200，`/api/jobs` 经 Nginx 代理 HTTP 200。
- 构建：部署机以既有 Node 14 Docker 构建完成 `npm run build:prod`。唯一警告为既有 Element UI 包体积（`chunk-elementUI` 673 KiB），无编译错误。
- 测试：本地 `make test` 全部通过；`git diff --check` 通过。目标机 `scripts/smoke.py` 登录和前置读接口正常，但因服务按现有安全配置 `scheduler_disabled=1` 运行，脚本对 `nextime` 时间格式的断言预期失败（实际值为“无”）；未为通过测试而开启未接线的生产调度器。
- 浏览器：正式 URL 已使用 Chrome headless 在 `1440×900` 和 `375×812` 实渲染，截图随本评论附上。复拍发现并修复窄屏卡片最小尺寸问题：`vue-consul/src/views/login/index.vue` 加入可收缩网格项和小屏间距规则，375px 卡片、输入与登录按钮均完整位于视口内。修复提交：`c7b6ff3`。

请 QA 使用附件在该地址复验；已知残余项仅为调度器禁用时与旧 smoke 脚本的 `nextime` 预期不一致，以及现有的 Element UI 包体积警告。

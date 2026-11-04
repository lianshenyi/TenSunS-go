# JumpServer Access Key 认证契约

## 范围与依据

新的 JumpServer 配置默认使用用户在 JumpServer Web 的 **API Key** 页面创建的
Access Key ID 与只在创建/轮换时可见的 Secret Key。它不再依赖 Private Token 或
服务器 Shell。该行为遵循 JumpServer 官方 REST API 文档的 Access Key 示例：
HMAC-SHA256 HTTP Signature，签名头为 `(request-target) accept date`。

V2、V3 的资产 API 路由和载荷差异不改变认证方式；两者均使用 `/api/v1/`，因此
同一签名器适用。目标 JumpServer 必须是支持 Access Key 的版本；版本未知或旧实例
应先在其 `/api/docs/` 确认。官方文档没有定义可依赖的时钟偏差窗口，部署端必须使用
NTP 保持与 JumpServer 同步；服务端返回 401 时不得自动退化为 Token。

## HTTP Signature

每个请求在发送前设置下列头，并按列出的顺序构造待签名文本：

```text
(request-target): <lowercase method> <escaped path and query>
accept: application/json
date: Mon, 02 Jan 2006 15:04:05 GMT
```

`Authorization` 格式为：

```text
Signature keyId="<Access Key ID>",algorithm="hmac-sha256",headers="(request-target) accept date",signature="<Base64(HMAC-SHA256(secret, signing-string))>"
```

`Date` 使用 RFC 7231 的 IMF-fixdate（UTC/GMT）。路径必须保留查询字符串，方法、路径、
查询或已签名头被篡改都会使验证失败。官方示例没有签名请求体；本实现不声称其能检测
未被纳入签名的 JSON body 篡改，TLS 是必需的。

## API 契约

`GET /api/jms/config` **绝不**返回 Secret 或旧 Token。已配置时返回：

```json
{
  "code": 20000,
  "jms_config": {
    "url": "https://jumpserver.example.com",
    "auth_type": "access_key",
    "access_key_id": "<configured>",
    "secret_configured": true,
    "legacy_token_configured": false,
    "ver": "V3"
  }
}
```

`POST /api/jms/config` 新默认要求 `auth_type: "access_key"`、`access_key_id` 及仅在
新建或显式轮换时提交的 `access_key_secret`。更新既有 Access Key 配置时省略空的
`access_key_secret` 会保留原 Secret；空字符串不清空 Secret。新建时 Secret 缺失返回
业务错误，且不写入部分配置。

`auth_type: "private_token_legacy"` 与 `token` 仅用于迁移已有部署；不得由新 UI 默认
发送，GET 也只用 `legacy_token_configured: true` 表示它存在而不返回明文。未知认证类型
被拒绝。

## Consul KV

`ConsulManager/jms/jms_info` 的新记录：

```json
{
  "url": "https://jumpserver.example.com",
  "auth_type": "access_key",
  "access_key_id": "<AES-256-CBC encrypted>",
  "access_key_secret": "<AES-256-CBC encrypted>",
  "ver": "V3"
}
```

Access Key ID 和 Secret 都用项目既有 AES 格式加密；不得写日志、错误、响应或测试失败
输出。保留的旧 `token` 字段表示 `private_token_legacy`，只在运行时解密，并禁止自动
迁移为新默认值。Access Key 数量上限（每用户 10 个）由 JumpServer 创建 API Key 的页面
执行；TenSunS 仅保存用户选择的一个 Key，不创建、枚举或绕过该限制。

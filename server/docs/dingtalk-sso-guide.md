# 钉钉企业自建应用 SSO 一键登录集成指南

基于 moleAgent_Serv 实际集成经验整理，对标飞书 SSO 实现。

## 前置条件

- 钉钉企业自建应用已创建（钉钉开发者后台 → 应用开发 → 企业内部应用）
- 应用已发布，目标用户在可见范围内
- 后端服务已实现钉钉 OAuth 交换接口

## 钉钉开发者后台配置

### 1. 获取应用凭证

在 **应用开发 → 企业内部应用 → 凭证与基础信息** 中获取：

| 配置项 | 说明 |
|--------|------|
| AppKey | 应用的唯一标识（对应 config 中的 `app_key`） |
| AppSecret | 应用密钥（对应 config 中的 `app_secret`） |
| CorpId | 企业标识（在 **组织管理 → 企业信息** 中查看，对应 config 中的 `corp_id`） |

### 2. 应用能力配置

在 **应用能力 → 网页应用** 中：

- 启用网页应用能力
- 配置应用首页地址（如 `https://mole.metme.top/admin`）

### 3. 重定向 URL

在 **安全设置 → 重定向 URL** 中添加 PC 扫码登录的回调地址：

```
https://mole.metme.top/admin/dingtalk-callback
```

⚠️ **与飞书的关键区别**：钉钉 PC OAuth2 回调参数名为 `authCode`（不是 `code`）。

### 4. 权限配置

在 **权限管理** 中申请以下权限：

| 权限 | 说明 | 必需 |
|------|------|------|
| `通讯录个人信息读权限` | 获取用户姓名、头像 | 是 |
| `个人信息读权限` | 获取 openId、unionId | 是 |

## 配置文件

### YAML 配置

```yaml
dingtalk:
  app_key: "dingxxxxxxxxx"
  app_secret: "xxxxxxxxx"
  corp_id: "dingxxxxxxxxx"
```

### 环境变量

```bash
MA_DINGTALK_APP_KEY=dingxxxxxxxxx
MA_DINGTALK_APP_SECRET=xxxxxxxxx
MA_DINGTALK_CORP_ID=dingxxxxxxxxx
```

## 前端集成

### 1. 引入 JSSDK

在 `index.html` 中引入钉钉 H5 JSSDK：

```html
<script src="https://g.alicdn.com/dingding/dingtalk-jsapi/3.0.25/dingtalk.open.js"></script>
```

加载成功后会在 `window` 上注入 `dd` 全局变量。

> **⚠️ 与飞书的区别**：飞书注入 `h5sdk` 和 `tt` 两个全局变量；钉钉只注入 `dd`。

### 2. 环境检测

```typescript
export function isDingTalkEnv(): boolean {
  if (typeof window === 'undefined') return false
  const ua = navigator.userAgent.toLowerCase()
  return ua.includes('dingtalk')
}
```

### 3. 获取授权码（H5 JSAPI）

```typescript
export function requestAuthCode(corpId: string): Promise<string> {
  return new Promise((resolve, reject) => {
    if (!window.dd) {
      reject(new Error('DingTalk JSAPI not available'))
      return
    }

    let settled = false
    const ok = (code: string) => { if (!settled) { settled = true; resolve(code) } }
    const fail = (err: unknown) => { if (!settled) { settled = true; reject(err) } }

    window.dd.error((err) => fail(new Error(`DingTalk dd error: ${JSON.stringify(err)}`)))
    window.dd.ready(() => {
      if (!window.dd?.requestAuthCode) {
        fail(new Error('DingTalk dd.requestAuthCode not available'))
        return
      }
      window.dd.requestAuthCode({
        corpId,
        onSuccess: (res) => ok(res.code),
        onFail: (err) => fail(new Error(`DingTalk requestAuthCode failed: ${JSON.stringify(err)}`)),
      })
    })
  })
}
```

**关键点**：
- 必须通过 `dd.ready()` 等待 SDK 初始化
- JSAPI 使用 `corpId`（不是 `appId`），调用 `dd.requestAuthCode({ corpId })`
- 用 settled 标志防止 `dd.error` 和 `ready` 同时触发

### 4. PC 浏览器 OAuth2 扫码登录

```typescript
export function buildDingTalkOAuth2URL(appKey: string): string {
  const base = window.location.origin + '/admin/dingtalk-callback'
  const params = new URLSearchParams({
    client_id: appKey,
    redirect_uri: base,
    response_type: 'code',
    scope: 'openid',
    state: randomState(),
    prompt: 'consent',
  })
  return `https://login.dingtalk.com/oauth2/auth?${params.toString()}`
}
```

**与飞书的区别**：
- 钉钉 OAuth2 使用 `client_id`（飞书用 `app_id`）
- 钉钉需要 `prompt=consent`
- 钉钉回调参数名为 `authCode`（飞书为 `code`）
- 钉钉 PC OAuth2 要求 `redirect_uri` 必须是 HTTPS 域名

### 5. 登录流程

#### 钉钉客户端内（H5 JSAPI）

```
钉钉客户端打开应用 → 检测 isDingTalkEnv()
  → 自动调用 requestAuthCode(corpId) 获取 code
    → POST /api/v1/auth/dingtalk/callback { code, source: "h5" }
      → 后端: code → access_token → userid → 用户信息 (unionId, name, avatar)
        → 已绑定：直接返回 JWT
        → 未绑定：返回 need_bind + dingtalk_token → 跳转绑定页面
```

#### PC 浏览器（OAuth2 扫码登录）

```
PC 浏览器访问 /admin/login → 点击"钉钉扫码登录"
  → 跳转钉钉 OAuth2 授权页（显示二维码）
    → 用户用钉钉 App 扫码授权
      → 钉钉回调到 /admin/dingtalk-callback?authCode=xxx
        → 前端提取 authCode → POST /api/v1/auth/dingtalk/callback { code: authCode, source: "oauth2" }
          → 后端: authCode → userAccessToken → 用户信息 (unionId, nick, avatarUrl)
            → 已绑定：直接返回 JWT → 跳转 Dashboard
            → 未绑定：返回 need_bind + dingtalk_token → 跳转绑定页面
```

## 后端集成

### 1. 两条 OAuth 交换路径

钉钉有两条独立的 OAuth 路径，通过 `source` 字段区分：

#### H5 JSAPI 路径（`source: "h5"`）

```
code → app_access_token (用 app_key + app_secret)
  → userid (用 access_token + code)
    → 用户信息 (用 access_token + userid → unionId, name, avatar)
```

API 端点：
- 获取 access_token：`POST https://oapi.dingtalk.com/gettoken` → `{ appkey, appsecret }`
- 获取 userid：`POST https://oapi.dingtalk.com/topapi/v2/user/getuserinfo` → `{ code }`
- 获取用户信息：`POST https://oapi.dingtalk.com/topapi/v2/user/get` → `{ userid }`

#### PC OAuth2 路径（`source: "oauth2"`）

```
authCode → userAccessToken (用 app_key + app_secret + authCode)
  → 用户信息 (用 userAccessToken → unionId, nick, avatarUrl)
```

API 端点：
- 获取 userAccessToken：`POST https://api.dingtalk.com/v1.0/oauth2/userAccessToken` → `{ clientId, clientSecret, code, grantType }`
- 获取用户信息：`GET https://api.dingtalk.com/v1.0/contact/users/me` → Header: `x-acs-oauth2-user-access-token`

### 2. 绑定标识：unionId

两条路径使用 `unionId` 作为统一绑定标识，同一用户无论通过 H5 还是 PC OAuth2 登录，都能匹配到同一条绑定记录。

### 3. 绑定流程

与飞书完全一致：

1. `POST /auth/dingtalk/callback` — 接收 code/authCode，换取用户信息
   - 已绑定 → 直接返回 JWT
   - 未绑定 → 生成临时 bind_token，返回 `need_bind: true`
2. `POST /auth/dingtalk/bind` — 接收 bind_token + 用户名密码
   - 验证 bind_token（消费式，一次性）
   - 验证用户名密码
   - 创建绑定记录
   - 返回 JWT

### 4. API 路由

| 方法 | 端点 | 认证 | 说明 |
|------|------|------|------|
| GET | `/api/v1/auth/dingtalk/config` | 公开 | 获取 corp_id + app_key |
| POST | `/api/v1/auth/dingtalk/callback` | 公开 | 登录/获取绑定 token |
| POST | `/api/v1/auth/dingtalk/bind` | 公开 | 绑定已有账户 |
| GET | `/api/v1/me/dingtalk-bindings` | 需认证 | 查询当前用户绑定状态 |
| DELETE | `/api/v1/me/dingtalk-bindings` | 需认证 | 解绑钉钉 |

## 与飞书 SSO 的对比

| 对比项 | 飞书 | 钉钉 |
|--------|------|------|
| JSAPI 全局变量 | `h5sdk` + `tt` | `dd` |
| JSAPI 获取授权码 | `tt.requestAuthCode({ appId })` | `dd.requestAuthCode({ corpId })` |
| JSAPI 参数 | `appId` | `corpId` |
| PC OAuth2 参数 | `app_id` | `client_id` |
| PC 回调参数名 | `code` | `authCode` |
| 后端凭证 | `app_id` + `app_secret` | `app_key` + `app_secret` + `corp_id` |
| 绑定标识 | `openId` | `unionId` |
| PC OAuth2 HTTPS 要求 | 无（IP 亦可） | **需要 HTTPS 域名** |
| H5 交换流程 | 2 步 (tenantToken → userAccessToken → userInfo) | 3 步 (accessToken → userid → userInfo) |
| PC 交换流程 | 2 步 (tenantToken + code → userAccessToken → userInfo) | 2 步 (authCode → userAccessToken → userInfo) |

## 常见错误排查

| 错误 | 排查方向 |
|------|----------|
| `invalid code` | auth_code 已过期（有效期 5 分钟）或已使用 |
| `DingTalk JSAPI not available` | 未在钉钉客户端内打开，或 JSSDK 未加载 |
| `getuserid error: 40014` | access_token 不合法或已过期 |
| `getuserid error: 40029` | code 无效或已过期 |
| OAuth2 回调 404 | 未在开发者后台添加 `/admin/dingtalk-callback` 重定向 URL |
| PC 扫码登录报错 | 检查 redirect_uri 是否为 HTTPS 域名 |
| H5 自动登录不触发 | 检查 `corp_id` 是否正确配置 |

## 检查清单

部署前逐一确认：

- [ ] JSSDK CDN URL 正确且可访问（`g.alicdn.com/dingding/dingtalk-jsapi/3.0.25/dingtalk.open.js`）
- [ ] 配置文件中 `app_key`、`app_secret`、`corp_id` 已正确填写
- [ ] 钉钉开发者后台 → 安全设置 → 重定向 URL 已添加 `/admin/dingtalk-callback`
- [ ] 应用已发布且目标用户在可见范围内
- [ ] `dd.requestAuthCode` 在 `dd.ready()` 回调中调用
- [ ] PC OAuth2 回调使用 `authCode` 参数名（不是 `code`）
- [ ] 钉钉客户端内测试 H5 JSAPI 登录，PC 浏览器测试 OAuth2 扫码登录
- [ ] 权限配置中已申请通讯录和个人信息读权限

# 飞书网页应用 SSO 一键登录集成指南

基于 moleAgent_Serv 实际集成经验整理，记录关键配置和常见陷阱。

## 前置条件

- 飞书企业自建应用已创建并开启「网页应用」能力
- 应用已发布，目标用户在可用范围内
- 后端服务已实现飞书 OAuth 交换接口（app_access_token → user_access_token → 用户信息）

## 飞书开发者后台配置

### 1. 网页应用主页 URL

在 **应用功能 → 网页应用** 中配置：

| 配置项 | 示例 |
|--------|------|
| 桌面端主页 | `https://mole.metme.top/admin` |
| 移动端主页 | `https://mole.metme.top/mobile` |

### 2. 重定向 URL（核心，极易出错）

在 **安全设置 → 重定向 URL** 中添加所有可能触发 `requestAuthCode` 的页面路径。

**⚠️ 关键规则：URL 必须精确匹配（大小写、末尾斜杠）**

用以下方式获取实际页面 URL：
```js
window.location.href.split('?')[0].split('#')[0]
```

示例配置：
```
https://mole.metme.top/admin
https://mole.metme.top/admin/
https://mole.metme.top/admin/login
https://mole.metme.top/admin/feishu-callback
https://mole.metme.top/mobile
https://mole.metme.top/mobile/
```

> **经验教训**：SPA 的路由路径（如 `/admin/login`）也算页面 URL。`/admin/login` ≠ `/admin/login/`，末尾斜杠不匹配会导致 10236 错误。建议同时添加带和不带斜杠的版本。
>
> **⚠️ PC 扫码登录**：`/admin/feishu-callback` 是 PC 浏览器飞书 OAuth2 网页授权的回调地址，必须添加。如果缺失会导致错误码 20029（重定向 URL 有误）。如果通过内网 IP 访问（如 `http://192.168.1.100:8080`），也需要将 `http://192.168.1.100:8080/admin/feishu-callback` 添加到重定向 URL 列表中。

### 3. H5 可信域名

在 **安全设置 → H5可信域名** 中添加（如需调用需鉴权的 JSAPI）：
```
mole.metme.top
```

> 注：`requestAuthCode` 和 `requestAccess` **不需要鉴权**，但其他 JSAPI 调用需要。

## 前端集成

### 1. 引入 JSSDK

在 `index.html` 中引入飞书 H5 JSSDK：

```html
<script src="https://lf-scm-cn.feishucdn.com/lark/op/h5-js-sdk-1.5.44.js"></script>
```

加载成功后会在 `window` 上注入 `h5sdk` 和 `tt` 两个全局变量。

> **⚠️ CDN URL 陷阱**：网上流传的旧 URL（如 `lf1-cdn-tos.bytegoofy.com/byted/refactor/feishu-chat-js-sdk/latest/index.js`）已失效返回 404。务必使用官方文档中的最新 URL，格式为 `https://lf-scm-cn.feishucdn.com/lark/op/h5-js-sdk-{版本号}.js`。

### 2. 获取授权码（requestAuthCode）

```typescript
// 必须在 h5sdk.ready() 回调中调用，不能直接调用 tt.requestAuthCode
export function requestAuthCode(appId: string): Promise<string> {
  return new Promise((resolve, reject) => {
    if (!window.h5sdk) {
      reject(new Error('Feishu JSAPI not available'))
      return
    }

    let settled = false
    const ok = (code: string) => { if (!settled) { settled = true; resolve(code) } }
    const fail = (err: unknown) => { if (!settled) { settled = true; reject(err) } }

    window.h5sdk.error((err) => fail(new Error(`h5sdk error: ${JSON.stringify(err)}`)))
    window.h5sdk.ready(() => {
      if (!window.tt?.requestAuthCode) {
        fail(new Error('tt.requestAuthCode not available'))
        return
      }
      window.tt.requestAuthCode({
        appId,
        success: (res) => ok(res.code),
        fail: (err) => fail(new Error(`requestAuthCode failed: ${JSON.stringify(err)}`)),
      })
    })
  })
}
```

**关键点**：
- 必须通过 `h5sdk.ready()` 等待 SDK 初始化完成
- 用 settled 标志防止 h5sdk.error 和 ready 同时触发导致双重 resolve/reject
- 新版飞书客户端（V6.9.0+）推荐用 `tt.requestAccess` 替代 `tt.requestAuthCode`，后者已停止维护但仍可用
- **只有在飞书客户端内打开才会注入全局变量**，外部浏览器不会注入

### 3. 环境检测

```typescript
export function isFeishuEnv(): boolean {
  if (typeof window === 'undefined') return false
  const ua = navigator.userAgent.toLowerCase()
  return ua.includes('feishu') || ua.includes('lark')
}
```

### 4. 登录流程

#### 飞书客户端内（H5 JSAPI）

```
飞书客户端打开应用 → 检测 isFeishuEnv()
  → 自动调用 requestAuthCode(appId) 获取 auth_code
    → POST /api/v1/auth/feishu/callback { code }
      → 后端用 code 换取用户信息 (open_id)
        → 已绑定：直接返回 JWT token → 登录成功
        → 未绑定：返回 need_bind + feishu_token → 跳转绑定页面
```

#### PC 浏览器（OAuth2 网页授权，扫码登录）

```
PC 浏览器访问 /admin/login → 点击"飞书扫码登录"
  → 跳转飞书 OAuth2 授权页（显示二维码）
    → 用户用飞书 App 扫码授权
      → 飞书回调到 /admin/feishu-callback?code=xxx
        → 前端提取 code → POST /api/v1/auth/feishu/callback { code }
          → 后端用 code 换取用户信息 (open_id)
            → 已绑定：直接返回 JWT token → 跳转 Dashboard
            → 未绑定：返回 need_bind + feishu_token → 跳转绑定页面
```

> **前提**：飞书开发者后台 → 安全设置 → 重定向 URL 中必须添加 `/admin/feishu-callback` 的完整地址（如 `https://mole.metme.top/admin/feishu-callback`），否则会报错 20029。

## 后端集成

### 1. 飞书 OAuth 交换流程

```
auth_code → app_access_token + auth_code → user_access_token → 用户信息 (open_id, name, avatar)
```

- `app_access_token`：用 `app_id` + `app_secret` 获取
- `user_access_token`：用 `app_access_token` + `auth_code` 交换
- auth_code 有效期 **5 分钟**，且只能使用一次

### 2. 绑定 Token 存储

首次登录时生成随机 token，临时存储飞书用户信息，供绑定页面验证：

```go
// ⚠️ 时间参数陷阱：SetExpire 第三个参数是 time.Duration，不是秒数！
// 错误：SetExpire(key, value, 300)     → 300 纳秒，瞬间过期
// 正确：SetExpire(key, value, 300*time.Second)  → 5 分钟
h.db.Str().SetExpire("feishu_bind_tokens:"+token, string(data), 300*time.Second)
```

### 3. 绑定流程

1. `POST /auth/feishu/callback` — 接收 auth_code，换取用户信息
   - 已绑定 → 直接返回 JWT
   - 未绑定 → 生成临时 bind_token，返回 `need_bind: true`
2. `POST /auth/feishu/bind` — 接收 bind_token + 用户名密码
   - 验证 bind_token（消费式，一次性）
   - 验证用户名密码
   - 创建绑定记录
   - 返回 JWT

### 4. Token 过期时间处理

飞书返回的 `expire` 字段可能很小（如 10 秒），减去 60 秒可能变为负数：

```go
expire := resp.Expire - 60
if expire <= 0 {
    expire = 60
}
c.tokenExpire = time.Now().Add(time.Duration(expire) * time.Second)
```

## 常见错误码排查

| 错误码 | 含义 | 排查方向 |
|--------|------|----------|
| 20029 | 重定向 URL 有误 | PC 扫码登录时，未在开发者后台添加 `/admin/feishu-callback` 的重定向 URL |
| 10236 | URL 不合法 | 重定向 URL 未配置或末尾斜杠不匹配 |
| 10235 | 未配置重定向 URL | 在开发者后台添加重定向 URL |
| 10200 | appId 不合法 | 检查前端传入的 appId 是否正确 |
| 10228 | 应用对用户不可见 | 检查应用可用范围是否包含该用户 |
| 10232 | 获取授权码失败 | 重试，可能是临时错误 |
| 2602002 | Auto login failed | 包含具体 error code，如 10236 |

## 检查清单

部署前逐一确认：

- [ ] JSSDK CDN URL 正确且可访问（返回 200）
- [ ] 重定向 URL 已添加所有可能的页面路径（含 `/admin/feishu-callback`，带/不带末尾斜杠）
- [ ] 桌面端主页 / 移动端主页 URL 已配置
- [ ] 应用已发布且目标用户在可用范围内
- [ ] `requestAuthCode` 在 `h5sdk.ready()` 回调中调用
- [ ] 后端 `SetExpire` 使用 `time.Duration` 而非裸数字
- [ ] auth_code 只使用一次（不可重复调用 callback）
- [ ] bind_token TTL 合理（建议 5 分钟）
- [ ] 飞书客户端内测试 H5 JSAPI 登录，PC 浏览器测试 OAuth2 扫码登录

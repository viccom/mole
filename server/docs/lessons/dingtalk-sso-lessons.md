# 钉钉 SSO 开发踩坑记录

> 日期：2026-05-14
> 涉及模块：admin 前端 + Go 后端 dingtalk 集成
> 状态：已调通，钉钉 PC 客户端 H5 JSAPI + 浏览器 OAuth2 双通道

---

## 全过程问题清单

### 问题 1：`dd.error` 回调导致 JSAPI 提前失败

**现象**：`requestAuthCode` 在钉钉 PC 客户端中永远报错，Promise 被立即 reject。

**根因**：`dd.error(callback)` 注册的全局错误处理器会在 `dd.ready` 之前触发（包括 JSSDK 初始化警告）。我们的代码在 `dd.error` 回调中直接 reject 了 Promise，导致 `dd.ready` 中的 `requestAuthCode` 永远不会执行。

**对比飞书**：飞书虽然也注册了 `h5sdk.error`，但飞书的 JSSDK 行为不同 — `ready` 总是先于 `error` 触发。

**修复**：移除 `dd.error` 处理器，仅在 `dd.ready` 内部处理 `onSuccess`/`onFail`。

```typescript
// 错误写法
window.dd.error((err) => fail(...))  // 会在 dd.ready 之前触发
window.dd.ready(() => { ... })

// 正确写法
window.dd.ready(() => {
  // 只通过 onSuccess/onFail 处理结果
})
```

---

### 问题 2：`dd.requestAuthCode` 在 PC 客户端不可用

**现象**：代码调用 `dd.requestAuthCode` 报 "not available"。

**根因**：钉钉 JSSDK 有多个版本和方法路径：
- `dd.requestAuthCode` — 新版简写，移动端可用
- `dd.runtime.permission.requestAuthCode` — 完整路径，PC 客户端可能使用

CDN 加载的 `dingtalk.open.js` 可能不完全暴露 `requestAuthCode` 简写。

**修复**：按优先级尝试两条 API 路径：

```typescript
if (window.dd?.requestAuthCode) {
  window.dd.requestAuthCode(params)
} else if (window.dd?.runtime?.permission?.requestAuthCode) {
  window.dd.runtime.permission.requestAuthCode(params)
}
```

---

### 问题 3：OAuth2 回退导致无限重定向循环

**现象**：钉钉 PC 客户端打开后页面疯狂跳转，每 100-200ms 一次循环。

**根因链条**：
1. JSAPI 获取 authCode 成功 → 后端 `gettoken` 用 POST（应该用 GET）→ 返回 401
2. 前端 catch 到错误，错误消息含 "unavailable" → 触发 OAuth2 重定向
3. OAuth2 → callback → 处理 → 某种原因回到 login → 再次触发 SSO → 循环

**修复**：
- 后端：`gettoken` 改为 GET（见问题 5）
- 前端：钉钉原生环境中禁止 OAuth2 重定向（`!isDingTalkEnv()` 条件）
- 前端：`sessionStorage` 防护，每个 session 只自动触发一次 SSO

```typescript
if (!isDingTalkEnv() && msg.includes('unavailable') && appKey) {
  window.location.href = buildDingTalkOAuth2URL(appKey)
}
```

---

### 问题 4：React 异步状态导致参数丢失

**现象**：`handleDingTalkSSO` 中 `appKey` 为空字符串，OAuth2 回退条件 `&& appKey` 永远为 false。

**根因**：`setDingtalkAppKey()` 是异步的 React state 更新。在 `useEffect` 的同一个 `init()` 函数中，`setDingtalkAppKey(cfg.app_key)` 之后立即调用 `handleDingTalkSSO()`，此时 `dingtalkAppKey` state 还是旧值（空字符串）。

**修复**：不依赖 state，直接传参：

```typescript
// 错误：依赖异步 state
setDingtalkAppKey(cfg.app_key)
handleDingTalkSSO(dingtalkCorpId, dingtalkAppKey)  // dingtalkAppKey 还是空的！

// 正确：直接传配置值
setDingtalkAppKey(cfg.app_key)
handleDingTalkSSO(cfg.corp_id, cfg.app_key)  // 直接用 cfg 的值
```

---

### 问题 5：后端 `gettoken` API 用了错误的 HTTP 方法

**现象**：后端日志 `dingtalk API error: 43001 需要GET请求`，所有 H5 SSO 登录失败返回 401。

**根因**：钉钉旧版 `gettoken` 接口 (`oapi.dingtalk.com/gettoken`) 要求 GET 请求 + query 参数，代码用了 POST + JSON body。

```go
// 错误
body := map[string]string{"appkey": key, "appsecret": secret}
doRequest(ctx, "POST", "https://oapi.dingtalk.com/gettoken", body, "")

// 正确
url := fmt.Sprintf("https://oapi.dingtalk.com/gettoken?appkey=%s&appsecret=%s", key, secret)
doRequest(ctx, "GET", url, nil, "")
```

**注意**：钉钉新版 API (`api.dingtalk.com/v1.0/oauth2/accessToken`) 用 POST，但旧版用 GET。两个端点不能混用。

---

### 问题 6：上传了错误的二进制文件

**现象**：更新后所有 API 路由返回 404（不只是钉钉），飞书按钮也消失，版本检测失败。

**根因**：`make publish` 通过 `release` 目标交叉编译生成带平台后缀的二进制：
- `moleagent-serv-linux-amd64` (13.6M) — 正确的
- `moleagent-serv` (13.0M) — 旧的当前平台构建产物

上传时用了 `scp moleagent-serv` 而不是 `scp moleagent-serv-linux-amd64`，导致服务器运行了不含钉钉/飞书路由的旧版二进制。

**教训**：部署时必须确认上传的是正确的平台二进制。md5 校验一致不代表版本正确 — 只是说明上传的文件和本地一致，但本地那个文件本身就是旧的。

---

## 架构决策记录

### 双通道设计

| 通道 | 场景 | 前端 API | 后端方法 |
|------|------|----------|----------|
| H5 JSAPI | 钉钉 PC/移动客户端内打开 | `dd.requestAuthCode` / `dd.runtime.permission.requestAuthCode` | `GetUserByCode(code)` |
| OAuth2 | 普通浏览器 | 重定向到 `login.dingtalk.com/oauth2/auth` | `GetUserByOAuthCode(authCode)` |

### 前端自动 SSO 流程（参考飞书实现）

```
页面加载
  → useEffect init()
    → 并行获取 feishuConfig + dingtalkConfig
    → 检测环境 (isFeishuEnv / isDingTalkEnv)
    → 命中 → sessionStorage 防重复 → handleDingTalkSSO(corpId, appKey)
      → dd.ready → requestAuthCode(corpId)
        → onSuccess → dingtalkLogin(code, 'h5')
          → needBind → navigate('/dingtalk-bind')
          → success → navigate('/dashboard')
        → onFail → toast 错误（不重定向）
```

---

## 关键经验

1. **钉钉 JSSDK 行为与飞书不同**：不能照搬飞书的 `error` + `ready` 模式。钉钉 `dd.error` 可能在 `dd.ready` 前触发且包含非致命错误。

2. **钉钉有多个 API 路径**：`dd.requestAuthCode` vs `dd.runtime.permission.requestAuthCode`，需要都尝试。

3. **环境检测决定回退策略**：`isDingTalkEnv()` 为 true 时不要回退到 OAuth2 — 原生客户端内的 OAuth2 体验很差且可能触发循环。

4. **循环防护必须有**：SSO 自动触发 + 失败 + 重新加载 = 无限循环。用 `sessionStorage` 标记已尝试，防止重复触发。

5. **React state 是异步的**：在 `useEffect` 的 `init()` 中不能依赖刚 `setState` 的值。直接传参。

6. **钉钉 API 文档要注意版本**：旧版 `oapi.dingtalk.com` 和新版 `api.dingtalk.com` 的请求方式不同。`gettoken` 是 GET，不是 POST。

7. **部署时确认二进制文件名**：交叉编译产物带平台后缀 (`-linux-amd64`)，不要上传无后缀的旧文件。校验 md5 只能确认传输完整性，不能确认版本正确性。

---

### 问题 7：Bind 接口 401 状态码触发全局拦截器

**现象**：绑定页面密码错误后，页面直接跳转到登录页，而不是停留在绑定页显示错误。

**根因**：前端 API 客户端有全局 401 拦截器（`window.location.href = '/admin/login'`）。后端 bind 接口密码错误返回 HTTP 401，被全局拦截器捕获并强制跳转，绑定页的 catch 处理器根本没机会处理。

**修复**：Bind 接口属于未认证操作（用户还没登录），密码错误应返回 400 而不是 401。

```go
// 错误：401 触发全局拦截器
ResponseError(w, http.StatusUnauthorized, 401, "Invalid username or password")

// 正确：400 让前端正常处理
ResponseError(w, http.StatusBadRequest, 400, "Invalid username or password")
```

**教训**：在已有全局 401 拦截器的架构中，非认证类接口（如 bind、注册）不应使用 401 状态码。

---

### 问题 8：钉钉 WebView CSS 覆盖 Tailwind 按钮样式

**现象**：移动端页面中，使用 Tailwind 彩色背景类的按钮（`bg-blue-600 text-white`）在钉钉客户端中完全不可见 — 背景和文字都消失。飞书客户端正常。

**根因**：钉钉 CDN JSSDK (`dingtalk.open.js`) 注入全局 CSS，覆盖了 Tailwind 的 `background-color` 等属性。受影响的按钮变成透明背景 + 白色文字，在白色页面上完全不可见。

**修复**：用 React inline `style` 代替 Tailwind 类，绕过 JSSDK 的 CSS 覆盖：

```tsx
// 会被钉钉 JSSDK 覆盖
<button className="w-full py-2.5 bg-blue-600 text-white rounded-lg">

// 正确：inline style 优先级最高
<button style={{ backgroundColor: '#2563eb', color: '#fff' }}
  className="w-full py-2.5 rounded-lg">
```

**教训**：钉钉移动端 WebView 基于 UC 内核，JSSDK 会注入全局 CSS。对关键视觉元素（按钮背景色）使用 inline style 而非 Tailwind 类是最稳妥的做法。

---

### 问题 9：移动端 dingtalk.ts 缺少同步修复

**现象**：PC 端钉钉 SSO 修复后，移动端仍然无法登录。

**根因**：移动端的 `dingtalk.ts` 是独立文件，没有同步 PC 端的修复（移除 `dd.error`、添加 `dd.runtime.permission.requestAuthCode` fallback）。

**教训**：多端项目修复 bug 时，必须检查所有端的同名文件是否需要同步更新。

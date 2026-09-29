# 钉钉 & 微信 SSO 集成调研

与已实现的飞书 SSO 做对比，评估钉钉和微信的集成难度、前置条件和注意事项。

## 钉钉 SSO

### 集成难度：低（≈ 2-3 小时）

钉钉的 SSO 流程与飞书几乎一致，代码层面可以高度复用。

### 前置条件

- 钉钉企业自建应用（免费创建，无需审核）
- 应用已发布，目标用户在可见范围内

### 登录方式

#### 1. 钉钉客户端内（H5 JSAPI）

```
钉钉客户端打开应用 → 检测环境（UA 含 DingTalk）
  → dd.requestAuthCode({ corpId }) 获取 authCode
    → POST /api/v1/auth/dingtalk/callback { code }
      → 后端用 code 换取用户信息
        → 已绑定：返回 JWT
        → 未绑定：跳转绑定页
```

JSSDK 引入：
```html
<script src="https://g.alicdn.com/dingding/dingtalk-jsapi/3.0.25/dingtalk.open.js"></script>
```

关键 API：`dd.requestAuthCode({ corpId, onSuccess, onFail })`

#### 2. PC 浏览器（OAuth2 网页授权，扫码登录）

```
PC 浏览器 → 点击"钉钉扫码登录"
  → 跳转：https://login.dingtalk.com/oauth2/auth?
      client_id=APP_KEY&
      redirect_uri=CALLBACK_URL&
      response_type=code&
      scope=openid&
      state=RANDOM&
      prompt=consent
    → 用户扫码授权
      → 回调到 redirect_uri?code=xxx&state=xxx
        → 后端用 code 换取用户信息
```

⚠️ **与飞书的关键区别**：PC OAuth2 要求 `redirect_uri` 必须是 HTTPS 域名。

### 后端 OAuth 交换流程

```
authCode → app_access_token（用 app_key + app_secret）
  → user_access_token（用 app_access_token + authCode）
    → 用户信息（openId, name, avatar）
```

API 端点：
- 获取 app_access_token：`POST https://api.dingtalk.com/v1.0/oauth2/accessToken`
- 获取用户信息：`POST https://api.dingtalk.com/v1.0/contact/users/me`（Header: Authorization Bearer user_access_token）
- 通过 authCode 获取用户信息（推荐简化流程）：`POST https://api.dingtalk.com/v1.0/oauth2/userinfo?code=xxx`

### 注意事项

1. **回调域名要求 HTTPS**：PC 扫码登录的 redirect_uri 必须是 HTTPS 域名（飞书无此限制）
2. **corpId vs appId**：钉钉用 `corpId` 标识企业，JSAPI 中传 `corpId` 而非 `appId`
3. **应用凭证**：钉钉称 `app_key` / `app_secret`（飞书称 `app_id` / `app_secret`）
4. **无需平台审核**：自建应用无需审核，创建即可用

---

## 微信 SSO

### 集成难度：中高（代码量 4-6 小时，但平台门槛高）

微信 SSO 的代码量不大，但平台侧的前置条件和审核流程是主要障碍。

### 两种集成路径

| 路径 | 适用场景 | 前置条件 |
|------|----------|----------|
| 公众号网页授权 | 微信内打开 H5 页面 | 认证服务号（企业） |
| 开放平台网站应用 | PC 浏览器扫码登录 | 开放平台账号 + 审核通过的网站应用 |

### 路径一：公众号网页授权（微信内 H5）

```
微信内打开页面 → 跳转授权链接：
  https://open.weixin.qq.com/connect/oauth2/authorize?
    appid=APPID&
    redirect_uri=CALLBACK_URL&
    response_type=code&
    scope=snsapi_userinfo&
    state=RANDOM#wechat_redirect
  → 用户同意授权
    → 回调 redirect_uri?code=xxx&state=xxx
      → 后端用 code 换取 access_token + openid
        → 获取用户信息
```

scope 区别：
- `snsapi_base`：静默授权，只能获取 openid
- `snsapi_userinfo`：需用户确认，可获取昵称、头像（但见下方隐私变更）

⚠️ **微信隐私政策变更（重要）**：

2023 年起，`snsapi_userinfo` 不再返回真实头像和昵称。用户看到的页面变成"快照页"模式：
- 返回的 nickname 为 `"微信用户"` 或空字符串
- 返回的 headimgurl 为默认灰色头像
- **实际可用的只有 openid**

这意味着微信 SSO 登录基本只能作为"静默绑定"用途，无法获取有意义的用户展示信息。

### 路径二：开放平台网站应用（PC 扫码登录）

```
PC 浏览器 → 展示微信登录二维码
  → 引导用户扫码
    → 回调 redirect_uri?code=xxx
      → 后端用 code + app_secret 换取 access_token + openid
```

需要使用微信开放平台的"网站应用"能力，生成扫码登录二维码。

### 前置条件与门槛

| 条件 | 要求 | 难度 |
|------|------|------|
| 认证服务号 | 企业主体，微信认证（300元/年） | 中 |
| 开放平台账号 | 需注册并认证（300元/年） | 中 |
| ICP 备案域名 | 必须有国内备案域名 | 中高 |
| HTTPS | 必须 HTTPS | 低 |
| 网站应用审核 | 提交审核，1-7 个工作日 | 高（不确定） |
| 用户信息获取 | 隐私变更后基本只有 openid | 已降级 |

### 频率限制

- 未上架应用（未通过审核）：**100 次/天**
- 已上架应用：无明确限制（合理使用）

### 注意事项

1. **备案域名是硬门槛**：没有国内 ICP 备案域名，微信 SSO 无法集成
2. **审核周期不确定**：网站应用审核通常 1-7 个工作日，可能被驳回需重新提交
3. **用户信息已降级**：隐私政策变更后，snsapi_userinfo 返回的是快照信息，不再是真实头像/昵称
4. **两个平台各自独立**：公众号（mp.weixin.qq.com）和开放平台（open.weixin.qq.com）的 appid 不通用，绑定需要额外操作
5. **UnionID 机制**：如果同时接公众号和开放平台，可以通过 UnionID 关联同一用户，但需要将公众号绑定到开放平台账号下

---

## 三平台对比总结

| 对比项 | 飞书 ✅（已集成） | 钉钉 | 微信 |
|--------|-----------------|------|------|
| 注册门槛 | 企业自建，免费 | 企业自建，免费 | 需认证服务号或开放平台审核 |
| 域名要求 | 无（IP 亦可） | PC 需 HTTPS | 必须 ICP 备案 + HTTPS |
| 审核周期 | 无需审核 | 无需审核 | 1-7 个工作日 |
| H5 JSAPI | ✅ h5sdk + tt.requestAuthCode | ✅ dd.requestAuthCode | ❌ 用网页跳转授权 |
| PC 扫码 | ✅ OAuth2 网页授权 | ✅ OAuth2 网页授权 | ✅ 开放平台二维码（需审核） |
| 用户信息 | openId + 姓名 + 头像 | openId + 姓名 + 头像 | 仅 openid（隐私变更） |
| 频率限制 | 无明显限制 | 无明显限制 | 未审核 100次/天 |
| 代码复用度 | — | 90%+ | 40-50% |
| 集成难度 | 已完成 | 低（2-3h） | 中高（4-6h + 平台审核） |
| 推荐优先级 | — | ⭐⭐⭐ 高 | ⭐ 低（投入产出比不高） |

## 建议的集成顺序

1. **钉钉**：优先集成，代码与飞书高度相似，可复用大部分后端逻辑和前端 UI
2. **微信**：视实际需求决定，如果只需要 openid 做静默绑定且已有备案域名，可以考虑；否则投入产出比不高

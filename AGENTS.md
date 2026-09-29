# mole — monorepo 项目约定

由 moleAgent_Serv + moleAgent_client 于 2026-09 合并（subtree 全历史保留）。
模块内部架构与编码约定见各自的 CLAUDE.md（[server/CLAUDE.md](server/CLAUDE.md) / [client/CLAUDE.md](client/CLAUDE.md)），本文件只放 **monorepo 层**的约定。

## 项目信息

- 布局：三 Go 模块嵌套 + go.work（`use ./server ./client ./shared`）——**现有 import 路径零改写**，不做单一 go.mod
  - `server/`：module `moleAgent_Serv`（服务端，模块内细节见其 CLAUDE.md）
  - `client/`：module `moleAgent_client`（客户端；发布产物必须 `-tags p2p`，裸 `go build` 不含 P2P——发布一律走 make）
  - `shared/`：module `mole/shared`，**仅 go.work 内本地引用，不对外发布**；双端 go.mod 以 `require + replace => ../shared` 接线（workspace 与单模块两种形态都可构建）
- 远端：git.metme.top/viccom/mole；旧仓库 moleAgent_Serv / moleAgent_client 已归档只读（历史锚点，ls-remote/clone 可用，禁止再提交）
- 迁移方案与验收清单：[server/docs/plans/2026-09-29-monorepo-merge-migration.md](server/docs/plans/2026-09-29-monorepo-merge-migration.md)；实施裁决记录：[docs/decisions.md](docs/decisions.md)

## 常用命令

```bash
# 构建/测试（各模块内；新会话改代码前先跑通这两组）
(cd server && go build ./... && go vet ./... && go test ./... -count=1)
(cd client && go build ./... && go vet ./... && go test ./... -count=1)
# client 已知基线：根包 nodeid 2 例 Windows 既有失败，非回归

# 发布（先打 tag，再 publish；见下方发布纪律）
(cd server && make release)     # 产物落 _release/moles/
(cd client && make release)     # 产物落 _release/molec/，7 平台，-tags p2p
```

## tag 前缀发布纪律（红线）

- tag **只允许** `srv/vX.Y.Z`（服务端）/ `cli/vX.Y.Z`（客户端）前缀形态；**禁止裸 `v*` tag**（两旧仓库 tag 全重叠，裸 tag 必冲突——迁移时已全部前缀映射）
- VERSION 由 `git describe --match '<前缀>/*'` 剥前缀得到；`check-tag` 守卫不变：剥前缀后必须无 `-N-g`/`-dirty` 后缀
- `latest.json` 的 `version` 字段是干净的 `vX.Y.Z`（与合并前产物逐字节同构）——存量 v0.8.0 客户端自更新判定零变化；**注意 `cli/v0.2.0-test` 测试 tag 会被 `--match 'cli/*'` 匹配**
- **纯单端提交后发另一端会被 `check-tag` 拒绝**（describe 越过最近 tag 带上 `-N-g` 后缀）→ 需先补打另一端新 tag（即便该端代码无变化）。这是特性：防「server-only 提交触发全舰队客户端自更新」
- **每次 publish 前必须过 staging 自更新冒烟**（gen_manifest 事故红线：发布工具链改动后未实测不得上生产更新源）
- 发布顺序：**先 `git tag` → 再 `make publish`**（原因见 server/CLAUDE.md 发布纪律节：脏 version 会被 selfupdater 的 fallbackParse 解析成 pre-release，升级永不生效）

## _release 规则

- `_release/` 只有三个文件入仓：`e2e/e2e_run.sh`、`e2e/echo_server.py`、`smoke_release.sh`（白名单式 .gitignore 控制；新增入仓文件须同步改 .gitignore）
- 其余一切产物（moles/ molec/ e2e/bin/ e2e/wt-*/）与运行时数据（logs/ data/ admin/）不入库
- e2e harness 铺 WSL 用 `mole/_release/e2e` 为源；脚本在仓库内是 LF（.gitattributes 锁定），**勿在 Windows 侧用会引入 CRLF 的工具改写**
- 旧版源码参照（wt-cli-old/wt-serv-old）已移至仓库外 `E:\Go_codes\mole\_archive\`

## shared 抽取守则

- 任何共享化抽取的前置：**先出双端实现的逐条差异清单并逐条裁决**（字段、规则、错误文案、边界值），裁决记录进 docs/decisions.md
- 等价性由单测锁定（集合值/文案逐字断言）；抽取后双端全量测试绿才算完成
- **双端行为必须保持不变**：语义分叉不强行统一，shared 提供参数化规则核心，双端薄适配层保留各自现状（形态参考 shared/listenport、shared/tunnelvalidate）
- 协议常量与纯传输结构体单源在 `shared/proto`；**行为函数不抽**（如 readBoundedLine 双端错误路径行为有意不同，见 decisions.md）
- 第二拍（随传输加密方案 B）：`shared/psk` + `shared/noiseconn`
- 新增共享代码必须先出现在 shared/ 再被双端引用，禁止复制粘贴双份

## 其他

- 行尾：仓库 `.gitattributes` 全仓 LF（go:embed 资源与 bash 脚本对 CRLF 敏感）；Windows 工作区改动后若测试诡异，先查行尾
- `docs/decisions.md`：方案文档与实际偏差、跨端语义裁决、发布工具链风险的强制记录点（追加式，不删旧条）
- docs/plans 全量随 server/ 树带入；新计划文档放 `server/docs/plans/`（沿用旧惯例）或根 `docs/`（monorepo 级）

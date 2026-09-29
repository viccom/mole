# mole

moleAgent_Serv + moleAgent_client 合并 monorepo（2026-09 迁移，subtree merge 全历史保留）。

## 布局

```
server/   ← 原 moleAgent_Serv（module 名不变：moleAgent_Serv）
client/   ← 原 moleAgent_client（module 名不变：moleAgent_client）
shared/   ← 新模块 mole/shared（仅 go.work 内本地引用，不发布）
_release/ ← 发布产物目录 + e2e harness（仅三脚本入仓，产物 gitignore）
go.work   ← use ./server ./client ./shared
```

## 发布纪律（tag 前缀）

- tag 命名：`srv/vX.Y.Z`（服务端）/ `cli/vX.Y.Z`（客户端）——历史 tag 由迁移映射而来，**无裸 `v*` tag**
- `make release/publish` 的 VERSION 由 `git describe --match '<prefix>/*'` 剥前缀得到，`latest.json` 的 `version` 字段与旧产物逐字节同构
- 纯单端提交后发另一端：`check-tag` 会拒绝（需补打另一端新 tag）——这是防「server-only 提交触发全舰队客户端自更新」的特性
- **每次 publish 前必须过 staging 自更新冒烟**（gen_manifest 事故红线）

详见 `docs/plans/2026-09-29-monorepo-merge-migration.md`（随 server/ 树带入）。

## 旧仓库

`git.metme.top/viccom/moleAgent_Serv` / `moleAgent_client`：已归档只读（历史锚点，clone/ls-remote 仍可用）。

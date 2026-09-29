# AGENTS.md（占位——会话 2 完成合并版）

本文件为骨架占位。合并版项目约定（tag 前缀发布纪律、_release 规则、shared 抽取守则）在迁移会话 2 落地。

过渡期约定（迁移完成前有效）：

- 三 Go 模块嵌套布局：`server/`（moleAgent_Serv）、`client/`（moleAgent_client）、`shared/`（mole/shared）；**现有 import 路径零改写**，不做单一 go.mod
- tag 只允许 `srv/vX.Y.Z`、`cli/vX.Y.Z` 前缀形态；禁止裸 `v*` tag
- `_release/` 仅 `e2e/e2e_run.sh`、`e2e/echo_server.py`、`smoke_release.sh` 三脚本入仓
- 每次发布前 staging 自更新冒烟是硬闸（gen_manifest 前科红线）

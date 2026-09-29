# 实施裁决与风险记录

## 2026-09-29 monorepo 迁移会话 1

依据方案 `docs/plans/2026-09-29-monorepo-merge-migration.md`（随 server/ 树带入）实施时，
方案文本与实际情况的偏差裁决与风险记录：

| # | 日期 | 冲突/偏差双方 | 采纳裁决 | 风险 |
|---|---|---|---|---|
| 1 | 2026-09-29 | 方案步骤 6 的 .gitignore 黑名单规则 vs 「仅三脚本入仓」目标句（admin/data/logs/server.yaml 等盖不住，字面 `git add _release` 会全提交） | 白名单式 .gitignore（`_release/*` 全忽略 + 否定三脚本），目标句为准 | 低：将来新增需入仓的 harness 文件须同步改 .gitignore |
| 2 | 2026-09-29 | 方案 go.work 模板 `use ./server ./client ./shared`（单行多目录）vs go.work 语法（每条 use 一个目录） | 逐条 `use` 三行 | 无 |
| 3 | 2026-09-29 | 方案「core.autocrlf=true 无碍（blob 不改写）」 vs client `shellui` 的 go:embed 资源行尾敏感测试（旧位 PASS / 新位 FAIL，断言硬编码 `\n`） | 仓库根 `.gitattributes`：`* text=auto eol=lf` + index renormalize + reset --hard；blob 全程零改写（迁移前后哈希逐一对账一致） | 低：Windows 下个别依赖 CRLF 的工具链需自行转换；收益是 e2e 脚本入 WSL 不再需要 sed 去 CRLF |
| 4 | 2026-09-29 | 远程 Serv tag 集（9 个）vs 原 Serv 本地 tag 集（10 个）：`v0.7.0` 为本地-only annotated tag（master 可达，pin `dea4ba5`），从未推送 | 从本地仓库补映射 `srv/v0.7.0`；事故记录：首次补映射漏加 `--no-tags` 导致 10 个裸 tag 混入，已全部删除并复核（21 tag 哈希全对账一致、零裸 `v*`） | 低：旧仓库远程没有 v0.7.0，若将来有人直接从旧远程 clone 会缺该 tag（归档说明中注明） |
| 5 | 2026-09-29 | `wt-cli-old`/`wt-serv-old` 的 worktree 迁移：`git worktree move` 需改旧仓库 `.git/worktrees` 元数据 vs 「对旧仓库零提交零改写」红线 | 物理移出至 `E:\Go_codes\mole\_archive\` 并删除 .git 链接文件（纯源码快照，pin hash 见 _archive/README.md）；旧仓库侧 worktree 注册悬空（无害，可 `git worktree prune`） | 低：_archive 快照失去 git 身份，按 README 中 hash 可回溯 |
| 6 | 2026-09-29 | `mv _release` Permission denied（句柄占用） | robocopy /MOVE 迁移（101 文件 303.57MB 零失败）；原位置空目录壳被外部句柄锁定暂留，待释放后手动删 | 无：空壳零内容 |
| 7 | 2026-09-29 | push 前置「gitea 预建空仓 viccom/mole」未完成（API 建仓 401：git 凭据仅 git 端点有效） | 建仓与 push 留给用户执行 `git push -u origin master && git push origin 'refs/tags/srv/*' 'refs/tags/cli/*'`；本地交付全就绪 | 中：推送完成前新仓库仅存本地，旧仓库仍为唯一远程事实源（符合「验收未全过前新仓库可整体丢弃」设计） |
| 8 | 2026-09-29 | 方案验收 #4 的验证命令 `git log --follow -- server/...` 在 subtree merge 场景返回空（history simplification 在 add merge 处沿 TREESAME 的骨架线剪枝） | 改用 `--full-history` 形态验证（已通过：226/405 commit 精确对账、旧路径日志逐条一致）；`--follow` 不穿透目录级搬移 merge 是 git 已知局限 | 无：历史对象与可达性完好，仅命令形态修正 |

## 遗留跟踪

- push（裁决 #7）：待用户建仓后执行，随后补验收 #5-8、#11-15（会话 3 范围）
- `_release` 原位置空目录壳（裁决 #6）：句柄释放后删除
- `cli/v0.2.0-test` 测试 tag 已随迁移带入：会话 3 改造 Makefile `describe --match` 时注意其可能被匹配为「最近 tag」

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

- push（裁决 #7）：待用户建仓后执行（用户明示不着急）；命令：`git push -u origin master && git push origin 'refs/tags/srv/*' 'refs/tags/cli/*'`
- `_release` 原位置空目录壳（裁决 #6）：句柄释放后删除
- server storage `TestAccessTokenRepo_Update` 时间精度 flaky（同微秒 `After` 判假，重跑 3 次全过）：旧仓库带来的既有问题，非迁移/shared 引入；后续可加 `>=` 或 sleep 修
- tunnelvalidate 抽取（组件 2，裁决表见下节）：实施待做——限额中断后由主会话/子代理续
- staging 冒烟物料（staging_setup.sh / staging_server.py）：已用毕删除；`_release/molec/latest.json` 为 #8 干跑产物（v0.9.0-e2etest），gitignore 内不入库，下次真发布时会被覆盖

## 2026-09-29 15 项验收完成记录（本地，push 前）

| # | 验收项 | 结果 | 证据 |
|---|---|---|---|
| 1 | 双端 build/vet | ✅ | exit=0×4 |
| 2 | 双端 test 全绿 | ✅ | server 15 包；client 仅 nodeid 2 例 Windows 既有基线（旧仓库原位复跑同样失败） |
| 3 | go.work 整体编译 + 互不 import | ✅ | `go build ./server/... ./client/... ./shared/...` exit=0；import 语句检索为空（跨引用仅注释） |
| 4 | 历史可达 + tag 哈希 | ✅ | 405→413 commit 对账；srv/v0.7.1、srv/v0.7.0、cli/v0.8.0 及全量 21 tag 哈希与原仓库一致 |
| 5 | server make release | ✅ | 6 平台产物落 _release/moles/；版本自报 `v0.7.1-202-g5bdc8cb` 无 srv/ 残留 |
| 6 | client make release | ✅ | 7 平台落 _release/molec/（含 armv7）；自报 `v0.8.0-243-g5bdc8cb`；TAGS:=p2p 且二进制含 nat-exchange 符号 |
| 7 | check-tag 实测 | ✅ | 被拒×2（`-N-g` 后缀、`-dirty` 工作区脏）；临时 tag 后放行（测毕 tag 已删、commit 已回退） |
| 8 | latest.json 键集 | ✅ | 7 键含 `linux/arm`→armv7 产物映射；version `v0.9.0-e2etest` 剥前缀干净 |
| 9 | tag 映射完整性 | ✅ | 零裸 v*；srv 10/10、cli 11/11 |
| 10 | 旧仓库 ls-remote | ✅ | 双仓库 heads 可读（归档动作待全部收尾后用户在 gitea 执行） |
| 11 | staging 自更新冒烟 | ✅ | v0.8.0→v0.9.0-e2etest 全链路：判定/下载 13.6MB/sha256/替换/重启/自报新版本；staging 访问日志双重印证。**陷阱记录**：WSL 代理变量（https_proxy）令首轮判定取到真实生产 latest.json 假信号，客户端进程须 NO_PROXY=fs.px.metme.top |
| 12 | 17 场景 E2E | ✅ | `PASS=20 FAIL=0` 与基线逐字一致（WSL 重铺自 mole/_release/e2e） |
| 13 | 17 计数仓库可核 | ✅ | run1to6×6 + 显式场景 7-17 标记 11 行 |
| 14 | AGENTS.md 合并版 + docs/plans 随树 | ✅ | 三块约定落库；plans 全量在 server/docs/plans |
| 15 | 回滚演练 | ✅ | 临时 clone 自包含（无 alternates）完整后删除；旧仓库双端 HEAD/工作区原样 |

## 2026-09-29 shared 抽取第一拍：隧道校验差异裁决表（组件 2）

依据双端逐条差异清单（会话内产出，要点：3 个语义分叉 + 6 条文案差异 + 9 处「注释声称一致实有暗差」）。
裁决原则：**双端行为语义不变**（分叉参数化保留现状）；错误文案统一为单一版本（一端输出文本变化，逐条记录如下）。

### 语义分叉——shared 参数化保留双端现状

| 分叉 | server 现状 | client 现状 | 裁决 |
|---|---|---|---|
| 空 target 放行集合 | ser2mq/vpn-manager/ser2tcp/ser2udp/webssh 空 target 合法（+p2p 走 para 分支） | 仅 vpn-manager（+p2p）；四类本地隧道要求非空 target（但 dropInvalidTunnels 用占位串 `client-local-no-target` 架空、warnInvalidPushedTunnels 有同集合豁免） | shared `Validate` 接受 `AllowEmptyTarget` 集合参数，双端各传现状集合；client 的占位串/豁免路径不动 |
| target scheme 检查 | 无（`http://h:p` 可通过） | http/https 拒 `://` | shared 参数 `RejectSchemeInTarget`（server false / client true） |
| rate_limit 形态 | `*TunnelRateLimit`（结构化） | `json.RawMessage` 透传（校验时临时 unmarshal） | shared 定义 `RateLimit` struct + 上限具名常量单源（100000 / 10737418240，消 client 三处散落字面量）；client 适配层仅校验时转换、存储仍 RawMessage 透传（防回传清空服务端限速）；client 独有的 RawMessage 反序列化错误文案留在适配层 |

### 文案差异——统一版本（⚠️ 标注输出变化端）

| 触发 | server 原文 | client 原文 | 统一为 | 变化端 |
|---|---|---|---|---|
| 未知类型 | `unknown tunnel type %q` | `invalid tunnel type: %s` | server 版 | client |
| target host 空 | `tunnel target host is required` | `... , got %q` | client 版（带 got 利排障） | server |
| rate_limit 范围 | `max_conns must be 1-%d, got %d` / `max_bandwidth must be 1-%d bytes/sec, got %d` | `rate_limit out of range: ...`（单条合并） | server 版（逐字段定位） | client |
| rate_limit 全零 | `...use null to clear` | `...(omit or null to clear)` | server 版 | client |
| p2p JSON 非法 | `p2p para is not valid JSON` | `...: %w`（带 cause） | client 版 | server |

错误前缀维持各端现状：shared 返回裸错误，server 适配层 `%w` 包装 `ErrTunnelInvalid`，client 裸用。

### 结构与边界

- p2p Para 校验（room/modes/relay/mappings 全部规则与文案）双端逐字一致 → 整体抽入 shared
- 列表级去重两规则 + 文案主体一致 → 抽入 shared `ValidateList`；256 上限常量单源（`MaxRegisterTunnels`）；register 路径的条数检查文案两版不同（`tunnel list exceeds limit of %d` vs `tunnel count %d exceeds max %d (register rejected)`）→ **留在各端调用点**（属调用方逻辑非校验器）
- server 独有跨节点校验（validateCrossNodeTunnelNames / validateListenPortConflicts / validateP2PRoomPairing / validateNodeRateLimit）→ 不抽，留 server service 层
- client 独有 dropInvalidTunnels（逐条丢弃 + 占位串）→ 不抽，client config 层现状保持
- readBoundedLine 双端错误路径行为差（server 丢弃已读 vs client 交还部分数据）→ **第一拍不抽行为函数**，随方案 B 第二拍裁决
- TunnelType 常量单源在 shared/proto（组件 3 先行），tunnelvalidate 复用

### 镜像注释暗差修正清单（实现时顺带）

- server internal/tunnel/control.go:49 ControlCmd.Cmd 注释补全 9 命令字（漏 tunnel_push/p2p_signal_token）——组件 3 范围
- client tunnel.go 各「与服务端对齐」注释：抽取后改指 shared 单源，消除 6 处不准确的镜像声明（125-127/77-79/170-171/104-106）与 validate_para.go:42「逐字一致」声明
- server errors.go ErrTunnelInvalid 文案被 client 注释误引为 `invalid tunnel:`——顺带修正引用文本

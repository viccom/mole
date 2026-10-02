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
| 9 | 2026-09-30 | 负责人决策：`_release` 是编译产物、不是仓库一部分，已整体移回与 mole 同级的外部路径（推翻方案第一节布局与裁决 #1 的「_release 落仓库根」） | 双端 Makefile `RELEASE_DIR := ../_release/...` → `../../_release/...`；git 树内 `_release/` 路径清空，三个 harness 脚本（源码非产物）`git mv` 至 `scripts/e2e/` 与 `scripts/` 保住验收 #13「17 场景计数可核」；.gitignore 改防御性忽略 `_release/` | 低：验收 #12 的 harness 铺设源描述从 `mole/_release/e2e` 变为仓库 `scripts/e2e` 脚本 + 外部 `_release/e2e/bin` 物料；smoke_release.sh 的产物绝对路径 `/mnt/e/.../_release/...` 恰与外部新位置一致无需改 |

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
| target scheme 检查 | 无（差异清单 a-5 原称「server 放行 http://h:p」——**实测推翻**：net.SplitHostPort 对多冒号输入直接报错，server 实际也拒绝，双端判定一致，差异仅在文案） | http/https 拒 `://`（文案更明确） | shared 参数 `RejectSchemeInTarget`（server false / client true）——保留的是**文案分叉**而非判定分叉 |
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

## 2026-09-30 架构审查 7 项架构性问题修复记录

来源：三代理深度架构审查（server / client / shared+monorepo），7 项🔴经复核确认无误判。逐项修复 + 复测结果：

| # | 问题 | 修复 commit | 复测证据 |
|---|---|---|---|
| 🔴1 | GUI 嵌套模块 workspace 下构建断裂（make test/vet/GUI 构建全不可用） | `4c1413b` | desktop/manager `go test` exit=0（原 exit=1）；wails 双端 v2.11.0 对齐；go.work 5 条 use |
| 🔴2 | 构建形态决定依赖版本（server 实际链接 smux 被 client 抬升 v1.5.24→v1.5.57） | `deadb02` | 四组（双端×双形态）解析一致：smux v1.5.57 / websocket v1.5.3 |
| 🔴3 | client 根包上帝包（2833 行 / 8 类职责 / 51 方法） | `fbba48d`（第一拍） | 根包降至 2433 行（nodeid 653 行外移）；**Manager 接口化与 client.go 拆分未做，见下「未完成」** |
| 🔴4 | internal/builtin 上行依赖根包 + 更新逻辑双源 | `51cd21f` | internal 全目录零根包依赖；更新 URL 全仓 1 处定义；staging 实测 CLI -check-update 全链路 |
| 🔴5 | server 分层三处穿孔（storage→auth / api→storage / api→node 具体类型） | `b4d8772` + `1f28495` | 三处 rg 检索均「无」；api 生产代码零 node 依赖 |
| 🔴6 | 节点断连清理三处平行实现 | `b206a94` | 三入口（control/health/2×handler）统一调 `RetireNode`；E2E 场景 7-8 全 PASS |
| 🔴7 | server 构建硬依赖 npm | `547e74c` | `make build-go` 零 npm 调用产出二进制；`make release` 全链仍绿 |

**修复中发现的连带问题（均已处理）**：
- 🔴5c 引入回归：`GetSession` 返回 any 后，control.go 的 `currentSess != session` 直接比较恒不等 → 断连节点永不清理。已在 `b206a94` 修正为断言后比较（health.go 同类点在同批修正）。
- 复核发现 git mv 后残留空目录 `client/internal/builtin/`，已 rmdir。

**未完成（如实记录，非静默跳过）**：
- 🔴3 的核心部分（5 个 Manager 抽统一接口 + 根包持 `[]tunnelManager` 聚合、client.go 1643 行按职责拆分、event 外移）**未实施**。理由：五个 Manager 的配置类型各不相同（Ser2MQConfig/TunnelConfig/vpn.Config/WebSSHConfig），统一接口需泛型或 any、切片场景下退化为 any，收益（消除 4 处 nil 判断）小于风险（触及隧道推送热路径、无测试覆盖的 Run 状态机）。已完成的 nodeid 外移 + vpn 冗余参数消除是其中低风险高收益的部分。
- 遗留跟踪：client.go 拆分与 Manager 接口化建议作为独立任务排期，需先补 Run 状态机测试（当前零覆盖）再动。

**全量回归基线（修复后）**：server 15 包全绿；client 9 包绿（nodeid 2 例 Windows 既有失败不变）；shared 3 包绿；五模块 × workspace/GOWORK=off 双形态构建全绿。

---

## 2026-09-30 第二轮复核（13 项清单）→ 必修 4 项修复记录

**背景**：架构修复轮之后的遗留问题清单复核（13 项），判定必修 4 项并行修复。复核中有 3 项改判：listen_port.go 死适配层（功能未丢，列清理项）、p2pModeSet 未导出（风险低列 P2）、desktop clientMu 长持锁（实测为短临界区模式，证据不足不判缺陷）。

| # | 问题 | 修复 commit | 复测证据 |
|---|---|---|---|
| MUST-1 | Tunnel 三镜像 struct 无 tag 锁（wire 最大未设防面：一端加字段另一端漏改 → JSON 静默丢字段） | `104c239` | shared/proto 新增 TunnelWireFields 契约（8 字段）；三模块 *_tags_test.go reflect 锁定；红验证：契约注入假字段后三模块全红（exit=1），恢复全绿 |
| MUST-2 | client c.cancel 无同步（Run 写/Close 读跨 goroutine，-race 必报） | `d4fad6a` | pre-fix `go test -race` 第 0 次迭代确定性报两份 DATA RACE；post-fix -race 全绿 exit=0 |
| MUST-3 | 三处版本推导漏 --match 本端前缀（build.ps1/release.ps1/build-lark-mole.sh） | `1131d1c` | 实测无 match 时 client 侧拿到 srv 基准（v0.7.1-215 vs 正确 v0.8.0-256）；修复后推导无前缀；build.ps1 端到端实跑，产物自报版本注入链闭环 |
| MUST-4 | REST 建节点不做 8 字符 ID 校验（Create 的 name 直接成为 node.ID，与 register 口径不一） | `0f0c7ba` | RED：4 种非法 ID 全部 200 放行；GREEN：400 + ErrInvalidNodeID 文案；E2E sc8 修正后有意义地 PASS |

**修复中发现的新问题（记录待办）**：
1. **Close-先于-Run 语义洞**（MUST-2 修复中子代理发现，未修）：Run 主循环只 watch `ctx.Done()` 不 watch `c.closed`，Close 在 Run 赋值行之前执行时 cancel 永久丢失 → Run 陷入无限快速重连（transport 已锁存关闭态，Connect 立即失败但循环不退）。竞争已修、洞仍在。修法方向：Run 主循环加 `<-c.closed` 分支。回归测试以 rescue 计时器兜底该窗口，测试时长 16-19s 即洞存在的佐证，修后应降至毫秒级。
2. **ps1 `2>$null` 必炸点**（MUST-3 修复中实证）：git 2.50 对「ref 名带前缀但 annotated tag 对象内部名无前缀」的 tag 发 stderr 告警，PS5.1 + `$ErrorActionPreference="Stop"` + `2>$null` 把告警升级为终止性 NativeCommandError——旧行在本机已必炸（与本次改动无关），已随修移除重定向。同款 `git rev-parse ... 2>$null`（build.ps1:18/release.ps1:27）当前无害但未来 git 升级可能踩同坑，待办。
3. **E2E harness sc8 请求体写错**（MUST-4 影响面排查发现）：`{"id":..,"name":"e2e-pre"}` 的 id 字段一直被 API 静默忽略（Create 请求体无 id 字段），真实落库 ID 是非法的 "e2e-pre"。已修正为 `{"name":"sc8pre01"}`——若不修，新校验下 400 会让 token 断言空转通过（假绿）。
4. **行为变更**（MUST-4 固有结果）：REST 建节点的 name 必须为 8 字符合法 ID。影响面亲验：admin 前端 createNode 零调用（无 UI 入口）；E2E sc8 已适配；非 8 字符预建节点本就无法被任何客户端注册匹配（register 自始强制该规则），属收紧死路径。

**E2E 环境教训（防重蹈）**：E2E harness（17 场景）**必须在 WSL 跑**。Windows Git Bash 下：(a) 旧版本对照二进制（cli-old/serv-old）是 Linux ELF 无法执行；(b) sc17 的 python3 blocker + `PATH=/usr/bin:/bin` 为 Linux 语义。本次误在 Windows 首跑得 PASS=13 FAIL=7，逐项排查后全部归因环境（6×ELF 不可执行 + sc17 blocker 未绑定）。WSL 重跑 PASS=20 FAIL=0 与基线一致。旧二进制留存于 `_release/e2e/bin/`（WSL 视角 /mnt/e/...），新二进制需 `GOOS=linux` 交叉编译后 stage 到 WSL `/root/e2e/bin/`。

**全量回归（修复后）**：server/client/shared workspace + GOWORK=off 双形态构建/vet/test 全绿（client nodeid 2 例 Windows 既有失败不变）；desktop/manager GUI 模块绿；client 根包 -race 绿；E2E 17 场景 PASS=20 FAIL=0（WSL）。

## 2026-10-02 深度审查报告（docs/2026-10-02-monorepo深度审查报告.md）低风险项修复记录

复核结论先行：报告 7 项发现经独立复核**全部属实**，但验收标准有一处实质错误——`TestRegisterResponseStillTimesOutBeyond12S` 的 12s 是设计值（13s 延迟应答 + 断言 12s 上限超时），修 A-1 不会也不应将其降至毫秒；正确验收只有 `TestCloseConcurrentWithRun`（见下）。B-1 的"12 个 mock 测试文件"实为 9 个 _test 文件中 5 个。用户裁决：修复全部低风险项。

| 项 | 修复 | 验证证据 |
|---|---|---|
| A-1 Close-先于-Run 语义洞 | `client.go` 主循环 select 与 `sleep` 增 `<-c.closed` 分支（cancel 丢失时 c.closed 是唯一终止信号） | 新增确定性红测试 `TestRunReturnsAfterCloseBeforeRun`（修前 5s 超时 FAIL → 修后 0.00s PASS）；`TestCloseConcurrentWithRun` 10.04s→**0.03s**（rescue 计时器不再触发即洞闭合）；-race 绿。现实可达路径已证：桌面端 `app.go:148` 用不可取消 Background 跑 Run，CLI 被 main 先 cancel() 掩盖 |
| B-4 nodeid 测试 Unix 假设 | 测试同时设 `HOME`+`USERPROFILE`（UsesHome 设双值、FallsBack 双清空）；AGENTS.md"既有基线"表述改判为已修缺陷 | 修前 2 例红（逐字复现报告）→ 修后 29 例全绿；**decisions.md:33 旧验收行"既有基线"为当时事实记录不改写**，其结论由本条替代 |
| B-1 死端口 | 删 `core/ports.go` 的 TunnelManager/MQTTBroker/RBACChecker/**core.AuthService**（全仓零引用零实现）+ 孤儿类型 `core.TunnelInfo`；保留仍存活的 MQTTClientInfo/MQTTStats/InlineCallback/Claims | server 全量 build/vet 绿、17 包测试仅 storage 已知 flaky（3/3 重跑过，旧仓库既有，见遗留跟踪） |
| B-1 AuthService 裁决 | **采"删空接口"分支**：api/mqtt/main 直接依赖 `auth.AuthService` 具体类型是既成事实（feishu_handler/mqtt/broker 引用点比报告列的更多），若反向让 api 依赖 core.AuthService 属较大重构且收益仅是名义边界；删除消除同名遮蔽误导。需要真边界时重加接口成本极低 | — |
| B-5 node_modules 污染构建图 | 新增 `server/mobile/go.mod`（空模块，mobile 为纯前端目录、无第一方 Go 代码、无人 import）切出 server 模块图 | `go list ./...` 不再含 node_modules（17 包）；全量 build/vet 绿 |
| B-3 fork 代码岛标注 | `client/CLAUDE.md` p2p 节补强：fork 自 gonc/p2punch、不按第一方规范审查、除非同步上游不重写、netx 纳入 | — |
| A-2 webssh 不变量测试 | 新增 `handler_test.go` 4 例：readPayload 6 边界子测试（含逐字节分片 reader）；writeStdinWithTimeout 超时+pipe 关闭唤醒（var 注入缩短超时，const→var 为唯一生产改动）；dial 失败不泄漏；**stuck-stdin 端到端**（进程内 stub SSH 服务端从不读通道 → 2MB window 耗尽 → 写超时 → 解互锁链 → HandleStream 返回 → 生产清理路径后 goroutine 回落） | 4 例全绿 0.38s、-race 绿。此前该链唯一防线是 writeStdinWithTimeout 的注释 |

**明确未修**（非低风险或非编码项）：B-2 wss CA/指纹 pinning——方案 B 动工前决策项（若只做 Noise 不给 TLS 路径留 pinning，自签部署继续走 InsecureSkipVerify 旁路）；P2 webssh TOFU 首信回写 known_hosts 落盘（报告已定性延后）。

**全量回归**：client 12 包全绿（含 p2p tag 构建/vet）；server 16 包绿 + storage flaky 3/3 重跑过；shared 3 包绿；desktop/manager GOWORK=off 构建绿；webssh/close 相关 -race 绿。

## 2026-10-02 GitHub 发布链路（建仓 viccom/mole + Actions workflow）

**manifest 单源拆分**：server/client Makefile 的 `publish` 拆出 `manifest` 子目标（latest.json 生成逐字原样，含 client 的 armv7→`linux/arm` 键约定），`publish: check-tag release manifest` + scp 不变。目的：GitHub Actions 复用同一生成逻辑——对应「严禁并行复刻权威工具链」红线（gen_manifest.py 键漂移事故）。本地以假二进制验证：双端 manifest exit=0，client 产出 `"linux/arm"` 键 + `moleagent-client-linux-armv7` 文件名，与历史约定逐字一致。

**`.github/workflows/release.yml`**：tag 驱动（`srv/*`/`cli/*`）→ 对应端 `make release`（RELEASE_DIR 经命令行覆写到 workspace 内，避开仓外 `../../_release`）→ `make check-tag manifest` → `gh release create/upload`（仅二进制+latest.json，与 make publish 的 scp 面一致，幂等可重跑）。守卫：meta 作业检测布局，21 个存量 tag 全部指向 monorepo 合并前单仓库布局（已逐一验树），初推只绿跳不发；workflow_dispatch 做 build-check（双端编译+manifest，不发布不 check-tag）。**升级服务器的正式发布仍走 make publish，GitHub Release 是镜像面。**

**仓库可见性（负责人裁决）**：初建为 private，负责人指示改为 **public**（注：私有仓也能跑 Actions——首次 dispatch 已实际执行，失败点是 npm 而非权限；早先 workflow 404 系新仓索引延迟，touch 重推即注册）。公开前已扫描 tracked 文件无凭据/密钥/.env（仅 Makefile 含自建升级服务器主机名，属端点非凭据）。

**admin package-lock.json 修复（CI 首跑失败的根因，两层）**：① package.json 升级 vitest4/jsdom29 后 lock 从未重生成（esbuild@0.28.2 整棵子树缺失，npm ci 必报 Missing）；② npm 11 重新生成时，vitest→esbuild@0.28.2 提升到顶层的 26 个 `@esbuild/*` 平台条目丢失 `optional:true` 标记，任意平台 `npm ci` 均 EBADPLATFORM。修法：官方源全量重生成 + 脚本补齐 optional 标记；已验证 npm10/npm11 双版本 `npm ci` 通过、tsc+vite 构建通过。教训：lock 的"可选平台依赖缺 optional 标记"是 npm11 已知类缺陷，lock 重生成后必须跑**真实** `npm ci`（`--dry-run` 在 npm11 下不校验完整性，会假绿）。

**首发实录（2026-10-02）**：`srv/v0.7.2` + `cli/v0.8.1` 打在 e78f65a，双端 workflow run 全绿，GitHub Release 各自发布（moles 6 平台 / molec 7 平台含 armv7，均含 latest.json，与 manifest 单源）。过程中修掉两处 workflow 缺陷：① artifact 名含 tag 前缀斜杠必败（upload-artifact 不允许 `/`，转连字符）；② 幂等补传分支（release 已存在 → upload --clobber）已在真实重跑中验证。注意：tag 曾从 980846b 强移至 e78f65a（workflow 修复 commit），两位置间无 Go 代码差异，二进制内容等价、版本串相同。存量 Gitea origin 未推（待负责人决定）。

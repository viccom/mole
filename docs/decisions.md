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

## 2026-09-30 传输加密方案 B「PSK 协议内升级」实施（自适应回落版）

**背景**：方案文档（旧仓 moleAgent_Serv/docs/plans/2026-09-29-transport-encryption-two-paths.md）按负责人 2026-09-29 定稿的自适应回落版实施。实施前按新 monorepo 代码复审方案 B，修正 11 项锚点/结构问题（B.9 记录）后移植入库（`docs/plans/` 同名文件，master 7e03b00）。

**实施裁决与理由**（详见方案文档 B.10 实施记录）：
1. **认证行契约收敛为 `proto.NodeAuthLine` 单源 struct**（偏离文档复审版的"三处落点"）：实施中发现客户端 map 序列化值只能为字符串，与服务端 int 字段类型错配会直接破坏认证行解析；单源同时消除双端形态漂移面。proof-only 输出与旧形态逐字节一致（测试锁定）。
2. **noiseconn 单源 `shared/noisechan`**（monorepo 收益，替代双仓库时代的双份实现）：shared 引入首个第三方依赖 flynn/noise v1.1.0（go.mod+go.sum 锁版本，无 vendor）。XXpsk2 经库 `PresharedKeyPlacement=2` 原生支持。
3. **残余风险知情采纳**（既有决策重申）：过渡期（require=false）MITM 剥除认证行 enc 字段可致静默明文（E2E sc22 实证演示）；收口 `require=true` 后消除。收口判据：服务端日志全量 `enc=true` 且无 `Plaintext control connection allowed` WARN。
4. **握手失败绝不回落**为硬安全边界（代码注释 + E2E sc23 断言注册成功=0、回落 WARN=0 双重锁定）。

**分支与提交**：`feat/channel-encryption-psk`（worktree mole-wt-psk）4 提交：3aa38ce noisechan 地基 → 33dadd9 proto 契约 → 9521dba 双端协商 → bd49314 E2E sc18-24。文档 B.10 记录在 master。

**验证**：双端 vet/build/test -race/GOWORK=off 全绿（client nodeid 2 例 Windows 既有基线失败不变）；GUI 两模块绿；client -tags p2p 绿；E2E WSL 24 场景 PASS=27 FAIL=0（sc1-17 无回归）。未覆盖如实记录：ws/kcp 传输升级链路、tcpdump 级密文验证（以日志/行为断言代替）。

**部署边界**：上线顺序自由、回滚零接触（服务端关 enabled 即全舰队自动回落）；`MA_CHANNEL_ENC_ENABLED/REQUIRE` env 可免改配置文件切换。

## 2026-10-01 通道加密实施审查复审 + 5 项修复

**背景**：方案 B 实施后深度审查（3 路子代理）+ 主会话逐条复验（临时红测试实证后即删），确认 4 项实缺陷 + Low 若干；修复清单经负责人批准逐项执行（b7100c9/e44bce4/6e83b51/79dae84/144c3b7）。

| # | 缺陷（复验方式） | 修复 |
|---|---|---|
| M1 | require 拒绝谓词按客户端意愿（enc:1 短路）判定，require+enabled=false（SetChannelEncryption 直调可绕配置校验）或 psk≠32（**经非 32 字节 TokenHash 记录可达——修正首轮审查"不可达"误判**，node_access_auth 对畸形 hash 返回 psk=nil 不阻断认证）时 enc:1 客户端获静默明文 ok（红测试实证 cmd=ok） | 谓词改按供给能力：`canEncrypt := wantEnc && enabled && len(psk)==32` 先行；wantEnc=true 的拒绝文案带供给不闭环提示，关键词保持稳定；+2 负向测试（nilPSK 认证桩变体） |
| M2 | useTLS 判定与 WS dialer 不同口径：wss:// 前缀 + tls=false（Validate 只拦反向组合，可达）→ 已加密连接发 enc 位 + 误导性 plaintext fallback WARN（污染收口对账判据） | `Config.effectiveTransportTLS()`（UseTLS ∥ ws+wss:// 前缀，与 dialer 同口径），装配点改用；6 组合单测 |
| M3 | noisechan `timeout<=0` 静默无 deadline（护栏测试实证阻塞挂住） | 非正超时直接 fail()；防挂死护栏测试入库 |
| H1 | desktop go.sum 缺 flynn/noise：GOWORK=off 构建实测 exit=1（workspace 模式掩盖；manager go.sum 同缺但导入链不触达侥幸能编）——五模块 GOWORK=off 门（4c1413b）回归 | 双 GUI 模块 tidy；验证矩阵自此含 GUI GOWORK=off 腿（本次五模块全绿） |
| Low | 握手最小帧注释 32B 失实（官方向量 48B）、fail() 内层 %v 丢哨兵（现无消费方，加固）、on/off 字面量双处、B.10 测试计数（8/6 实为 7→9/5） | 随手修（79dae84 + B.10 修正段） |

**复验对首轮审查的两处修正**：psk≠32 组合判「可达」而非「不可达」（数据异常路径真实存在）；%v 丢哨兵判「latent 加固」而非缺陷（全仓无 errors.Is 消费方，注释明说靠文案前缀）。

**验证**：双端 vet/build/test 全绿（client 仅 nodeid 2 例 Windows 既有基线）、-race 绿、-tags p2p 绿、**五模块 GOWORK=off 全绿（含修复的 GUI 两模块）**、WSL E2E 1-24 重跑 **PASS=27 FAIL=0**（修复后二进制，sc21/sc24 双方向实证 M1 无回归）。

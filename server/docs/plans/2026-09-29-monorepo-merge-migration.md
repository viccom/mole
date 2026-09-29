# 双仓库合并 monorepo 迁移方案（moleAgent_Serv + moleAgent_client → mole）

> **状态**：**已实施完成（2026-09-29，三段单日完成）**——15 项验收本地全部通过、shared 第一拍三件（listenport/proto/tunnelvalidate）全部单源化。实施与方案的偏差裁决与验收证据见仓库根 `docs/decisions.md`。push 待 gitea 建仓后执行；旧仓库归档在 push 之后。以下正文为原始方案（未改写，留档）。
> **前置**：双仓库 master 干净已推送（Serv `dbdc498` / Client `cca1e13`）、生产稳定、方案 B（PSK 协议内升级）**未实施**——合并先行，B 作为新布局的首个特性落地（B 的对称代码因此单源化）。
> **目标**：单仓库 `mole/`（`server/` + `client/` + `shared/` 三 Go 模块 + `go.work`），**历史完整保留、现有 import 路径零改写、二进制名与发布 URL 不变、生产零影响**。
> **已核实的关键事实**（本节所有锚点均已查证）：
> - 模块名：`moleAgent_Serv` / `moleAgent_client`（保持不变 → import 零改写的根据）
> - 两端 Makefile 同构：`VERSION := $(shell git describe --tags --always --dirty)`（Serv :27 / Client :28），`check-tag` 拒绝非干净 tag（Serv :108-112 / Client :170-173），`publish` 生成 latest.json 并上传
> - **两仓库 tag 几乎全重叠**（v0.1.0–v0.7.1 双方都有）→ 普通 `fetch --tags` 必然冲突，**tag 前缀映射是硬前提**（见第三节）
> - `RELEASE_DIR := ../_release/moles|molc`（相对各仓库根）：新布局下 Makefile 仍在 `server/`、`client/` 根，`_release` 落新仓库根 → **路径恰好零改动**
> - 自更新链路只消费 `latest.json` 的 `version` 字段（旧客户端 selfupdate 不读 git tag）→ tag 前缀对存量 v0.8.0 客户端零影响

---

## 一、目标布局与原则

```
E:\Go_codes\mole\mole\            ← 新仓库（git.metme.top/viccom/mole.git）
├── server/        ← 原 moleAgent_Serv 全量（go.mod/module 名原样）
├── client/        ← 原 moleAgent_client 全量（同上）
├── shared/        ← 新模块（module mole/shared，仅 go.work 内本地引用，不发布）
├── _release/      ← 自两仓库外侧迁入（gitignore 二进制；保留 e2e 脚本与发布工具）
├── go.work        ← use ./server ./client ./shared
├── AGENTS.md      ← 合并版项目约定
└── README.md
```

**原则**：① **迁移全程对旧仓库零提交零改写**（新仓库只 fetch 读取；指路信息走 gitea 描述字段）；② 嵌套三模块（不做单一 go.mod——那要改数百文件 import，爆炸半径不必要）；③ 旧仓库**归档只读、不删除**（生产部署与历史文档引用仍在），归档时点 = 15 项验收全部通过之后；④ 二进制名（moleagent-serv/moleagent-client）、更新源 URL、`latest.json` 语义一概不变；⑤ 旧工作目录 `E:\Go_codes\mole\{moleAgent_Serv,moleAgent_client}` 迁移完成前保留不动（回滚锚点）。

**迁移窗口期纪律**：会话 1 开始后旧仓库冻结新开发（一切改动进新仓库）。若期间必须对旧仓库做紧急修复（如生产 hotfix），修复合入旧仓库后在新仓库执行 `git fetch serv && git subtree pull --prefix=server serv master`（client 同理）重新同步——subtree 原生支持增量同步，这是设计冗余而非例外路径。

**明确不做**（本次范围外）：单一 go.mod 合并、二进制/命令改名、发布 URL 变更、shared 模块对外发布。

## 二、subtree merge 实施步骤（历史保留）

环境：Git Bash（git subtree 随 Git for Windows 自带）；`core.autocrlf` 保持本机现状（true），blob 内容不被改写。

```bash
# 0. 前置检查（双仓库各自）
git -C moleAgent_Serv status --short && git -C moleAgent_Serv log --oneline -1   # 干净，HEAD=dbdc498
git -C moleAgent_client status --short && git -C moleAgent_client log --oneline -1  # 干净，HEAD=cca1e13
git -C moleAgent_Serv push origin master && git -C moleAgent_client push origin master  # 远程同步

# 1. 新仓库骨架（首个提交是 subtree add 的前提）
mkdir mole && cd mole && git init
#   写入 .gitignore / README.md / AGENTS.md（占位）——.gitignore 须含 _release 产物规则（见步骤 6）
git add -A && git commit -m "chore: monorepo 骨架（server/client/shared 三模块布局）"

# 2. 挂远端并取对象——关键：禁止裸 tag 拉取（两仓库 tag 全重叠，必冲突）
git remote add serv https://git.metme.top/viccom/moleAgent_Serv.git
git config remote.serv.tagOpt --no-tags          # 永不自动拉裸 tag
git fetch serv                                    # 分支 → refs/remotes/serv/*
git fetch serv '+refs/tags/*:refs/tags/srv/*'     # tag 一律映射为 srv/ 前缀

# 3. 导入服务端（完整历史，不 squash）
git subtree add --prefix=server serv/master
# 验证：git log --follow --oneline -- server/internal/tunnel/control.go | head  # 应直达原仓库提交

# 4. 导入客户端（同法）
git remote add client https://git.metme.top/viccom/moleAgent_client.git
git config remote.client.tagOpt --no-tags
git fetch client
git fetch client '+refs/tags/*:refs/tags/cli/*'
git subtree add --prefix=client client/master

# 5. shared 模块 + go.work
mkdir shared && cd shared && go mod init mole/shared && cd ..
printf 'go 1.24\n\nuse ./server ./client ./shared\n' > go.work   # go 版本按两端 go.mod 实际值
git add -A && git commit -m "chore: shared 模块骨架与 go.work"

# 6. _release 迁入仓库根
#   将 E:\Go_codes\mole\_release 移至 mole/_release；.gitignore 规则：
#     _release/**/*.exe、_release/molec/moleagent-client-*、_release/moles/*、_release/e2e/bin/、_release/e2e/wt-*/
#   纳入版本库的只有：e2e_run.sh、echo_server.py、smoke_release.sh（harness 脚本入仓——顺带解决
#   「17 场景计数在仓库不可核」的审查遗留 G3）
#   wt-cli-old / wt-serv-old 旧源码 worktree 不入仓（移出为 E:\Go_codes\mole\_archive\）
git add _release && git commit -m "chore: _release harness 入仓（二进制 gitignore）"

# 7. 构建绿验证（合并完成的硬门槛）
(cd server && go build ./... && go vet ./... && go test ./... -count=1)
(cd client && go build ./... && go vet ./... && go test ./... -count=1)   # 基线：nodeid 2 例 Windows 既有失败

# 8. 远端与收尾
git remote add origin https://git.metme.top/viccom/mole.git   # 先在 gitea 建空仓
git push -u origin master
git push origin 'refs/tags/srv/*' 'refs/tags/cli/*'
#   旧仓库在 gitea 设为 archived（只读）——**迁移全程对旧仓库零提交零改写**
#   （fetch 是读操作；指路信息写 gitea 仓库描述字段/归档说明，不改旧仓库文件）
#   归档时点 = 第五节 15 项验收全部通过之后；归档只读但 clone/ls-remote 仍可用
```

**步骤红线**：第 2/4 步的 `tagOpt --no-tags` + 前缀映射**绝不可省**——否则 v0.5.0–v0.7.1 同名 tag 直接冲突覆盖，历史语义损坏。

## 三、tag 前缀方案（自更新安全的核心，按红线对待）

### 3.1 规则

| 项 | 规则 |
|---|---|
| tag 命名 | `srv/vX.Y.Z`（服务端）、`cli/vX.Y.Z`（客户端）；历史 tag 由步骤 2/4 映射而来（`srv/v0.7.1`、`cli/v0.8.0` 等） |
| VERSION 计算 | 两端 Makefile 同构修改：`VERSION := $(shell git describe --tags --always --dirty --match 'srv/*' 2>/dev/null \| sed 's\|^srv/\|\|' \|\| echo dev)`（客户端用 `cli/*`）——**只数本组件 tag 可达以来的提交** |
| `check-tag` | 逻辑不变（剥前缀后的值仍须无 `-N-g`/`-dirty` 后缀） |
| `latest.json` version | 干净的 `vX.Y.Z`（剥前缀后的值）——**与现行产物逐字节同构**，存量 v0.8.0 客户端升级判定零变化 |
| 服务端发布 | 同构（`srv/*`；服务端 latest.json 与上传路径照旧） |

### 3.2 发布纪律（前缀方案的必然后果，写进 AGENTS.md）

- **纯服务端提交 + 客户端发布**：`describe --match 'cli/*'` 会出现后缀（HEAD 已越过最近 cli tag）→ `check-tag` 拒绝 → 需先打一个新 `cli/vX.Y.Z`（即便客户端代码无变化，也只打 tag 不发布，或随之发布同版本二进制）。**这是特性不是缺陷**：它防止"server-only 提交触发全舰队客户端自更新"。
- 反向（客户端提交 + 服务端发布）同理。
- **每次 publish 前必须过 staging 自更新冒烟**（第五节验收 #11；gen_manifest 事故红线——发布工具链改动后未实测不得上生产更新源）。

### 3.3 存量兼容论证（为何对现网零影响）

- 现网 9 节点 v0.8.0 客户端：只读 `https://fs.px.metme.top/app/molec/latest.json` 的 `version`/`assets` 字段做升级判定，**不接触 git tag**；manifest 由改动后的 Makefile 生成，`version` 字段值与格式不变。
- 生产服务端二进制与部署路径：不变（发不发新版由运维决定，与仓库合并解耦）。

## 四、共享组件抽取（分两拍）

### 第一拍（随本次合并，会话 2）

只抽**已两端对齐且有测试锁定**的三件（行为等价风险最低）：

| 组件 | 来源 | 去向 |
|---|---|---|
| 保留端口表 | `server/internal/core/listen_port.go` + `client/tunnel.go` 手工副本（审查 G-4 漂移面） | `shared/listenport`（**消灭手工副本**，双端改为 import；等价性由单测锁定集合值） |
| 隧道校验规则 | `server/internal/core/tunnel_validate.go` 与 `client/tunnel.go` 的 `Validate/validateTunnelList` 镜像 | `shared/tunnelvalidate`（**前置步骤：先出两份实现的逐条 diff 清单**——字段与错误文案差异逐条裁决后合并；双端各自保留薄适配层做类型映射） |
| 协议常量 | 认证行/控制命令字段名（`proof`/`enc` 等，方案 B 前置） | `shared/proto`（常量与结构体定义单源） |

抽取流程（每件）：差异清单 → 移入 shared + 单测 → 双端改 import、删本侧副本 → 双端全量测试绿 → commit（含等价性证据）。

### 第二拍（随方案 B 实施时）

`shared/psk`（sha256(token)/HMAC 推导单源）+ `shared/noiseconn`（连接包装单源）——B 的双端对称代码从两份变一份，这是合并先行的核心收益。

## 五、E2E 验收清单（合并"完成"的定义）

**构建与单测**
- [ ] 1. `server/`、`client/` 各自 `go build ./...`、`go vet ./...` exit=0
- [ ] 2. `go test ./...` 双端全绿（基线：客户端 nodeid 2 例 Windows 既有失败不变）
- [ ] 3. `go.work` 生效：根目录 `go build ./...` 可整体编译；server/client 互不 import（结构检查）
- [ ] 4. 历史可达：`git log --follow` 从 `server/`、`client/` 任一文件可追溯到原仓库提交；`srv/*`、`cli/*` 前缀 tag 与原 tag 的指向哈希一致（抽样 v0.7.1 / v0.8.0）

**发布工具链（gen_manifest 前科，红线区）**
- [ ] 5. `server/ make release`：产物落 `_release/moles/`，版本自报剥前缀正确（如 `v0.8.0 (hash)`，无 `srv/` 前缀残留）
- [ ] 6. `client/ make release`：产物落 `_release/molec/`，7 平台齐全，版本自报同上
- [ ] 7. `check-tag` 行为实测：制造一个 server-only 提交后跑 `client/ make publish` → 必须被拒（`*-*-g*` 分支命中）；补打 `cli/vX.Y.Z` 后通过
- [ ] 8. `publish` 干跑（注释上传行）生成的 `latest.json`：键集与现行完全一致（`linux/amd64、linux/arm64、linux/arm、darwin/amd64、darwin/arm64、windows/amd64、windows/arm64`——注意 `linux/arm` 键，armv7 事故红线）、`version` 字段干净无前缀
- [ ] 9. tag 映射完整性：新仓库无任何无前缀 `v*` tag；`git tag -l 'srv/*' | wc -l` = 原 Serv tag 数，`cli/*` 同理
- [ ] 10. 旧仓库归档只读后 `git ls-remote` 仍可读（生产部署文档引用不失效）

**staging 自更新冒烟**
- [ ] 11. staging 起更新源（本机 http 服务 + 新 Makefile 产出的 latest.json/产物），用现网同版 v0.8.0 客户端二进制指向它：判定有更新 → 下载 → sha256 校验 → 替换重启 → 自报新版本；armv7 产物键命中 `linux/arm`

**E2E**
- [ ] 12. 17 场景全量复跑（harness 从 `mole/_release/e2e` 重新铺 WSL）：PASS=20 FAIL=0 基线不破
- [ ] 13. harness 脚本入仓后 `git grep -c '场景 1-17'` 可核（消灭 G3 的"17 计数仓库不可核"）

**流程与回滚**
- [ ] 14. 新仓库 AGENTS.md（合并版约定：tag 前缀发布纪律、_release 规则、shared 抽取守则）；docs/plans 全量随树带入
- [ ] 15. 回滚演练：新仓库整体删除，旧仓库与生产不受任何影响（结构性验证一次即弃）

## 六、风险表

| 风险 | 级别 | 对策 |
|---|---|---|
| 同名 tag 冲突（v0.5.0–v0.7.1 双方都有） | 🔴 若违反步骤即损坏 | 步骤 2/4 的 `tagOpt --no-tags` + 前缀映射为**强制红线**；验收 #9 兜底 |
| 发布工具链隐性回归（gen_manifest 同类） | 🔴 前科领域 | 验收 #7/#8/#11 三道闸；publish 前冒烟为永久纪律 |
| 共享抽取行为漂移（两份校验实现有暗差） | 🟡 | 抽取前强制差异清单逐条裁决；等价性单测锁定；双端全量回归 |
| `git subtree` 在超大历史上的性能 | 🟢 | 两仓库体量中等（千级提交），预期分钟级；不 squash 保历史 |
| 双模块依赖图合并的间接影响 | 🟢 | 嵌套模块隔离，依赖集不合并（各自 go.mod 原样） |
| 团队肌肉记忆（旧路径引用：脚本/文档/记忆） | 🟡 | 收尾步集中更新；旧仓库 README 指路 |

## 七、实施顺序与工作量

| 会话 | 内容 | 产出 |
|---|---|---|
| 1 | 第二节全部（骨架→导入→go.work→_release→构建绿→推送）+ 验收 #1-4、#9-10 | 新仓库可用、历史/tag 完整 |
| 2 | 第一节收尾（AGENTS.md）+ 第四节第一拍抽取 + 验收 #13 | shared 模块落地、G-4 消灭 |
| 3 | 第三节 Makefile/check-tag 改造 + 验收 #5-8、#11、#12、#15 | 发布工具链就绪、E2E 基线复现 |

前置依赖：gitea 上预建空仓 `viccom/mole`。完成后即可启动方案 B（在新布局上）。

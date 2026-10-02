.PHONY: help build build-go build-frontend release release-go clean build-admin build-mobile copy-admin copy-mobile publish manifest check-tag

.DEFAULT_GOAL := help

help: ## 显示帮助信息
	@echo "moleAgent_Serv $(VERSION)"
	@echo ""
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-z].*:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /: /' | column -t -s ':' | sed 's/^/  /'

# 构建变量
BINARY_NAME    := moleagent-serv
CMD_PATH       := ./cmd/moleagent-serv
RELEASE_DIR    := ../../_release/moles
FRONTEND_DIR   := ./admin
FRONTEND_DIST  := $(FRONTEND_DIR)/dist
MOBILE_DIR     := ./mobile
MOBILE_DIST    := $(MOBILE_DIR)/dist
RELEASE_ADMIN_DIR  := $(RELEASE_DIR)/admin
RELEASE_ADMIN_DIST := $(RELEASE_ADMIN_DIR)/dist
RELEASE_MOBILE_DIR := $(RELEASE_DIR)/mobile
RELEASE_MOBILE_DIST := $(RELEASE_MOBILE_DIR)/dist

# 版本信息（从本组件前缀 tag srv/* 获取并剥前缀，无 tag 时用 dev）
# monorepo 双前缀 tag（srv/cli）必须 --match 本组件，否则 VERSION 会数到另一端的 tag
VERSION  := $(shell git describe --tags --always --dirty --match 'srv/*' 2>/dev/null | sed 's|^srv/||')
VERSION  := $(if $(VERSION),$(VERSION),dev)
BUILD    := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# 编译标志
LDFLAGS  := -s -w \
            -X moleAgent_Serv/internal/version.Version=$(VERSION) \
            -X moleAgent_Serv/internal/version.GitHash=$(BUILD) \
            -X moleAgent_Serv/internal/version.BuildDate=$(DATE)

# 交叉编译目标
TARGETS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64

# 构建前端管理页面
build-admin: ## 构建 admin 前端
	@echo ">> Building admin frontend..."
	@cd $(FRONTEND_DIR) && npm run build
	@echo ">> Done: $(FRONTEND_DIST)"

# 构建手机端前端
build-mobile: ## 构建手机端前端
	@echo ">> Building mobile frontend..."
	@cd $(MOBILE_DIR) && npm run build
	@echo ">> Done: $(MOBILE_DIST)"

# 拷贝前端构建产物到发行目录
copy-admin: ## 拷贝 admin 前端到发行目录
	@echo ">> Copying admin frontend assets..."
	@mkdir -p $(RELEASE_ADMIN_DIR)
	@rm -rf $(RELEASE_ADMIN_DIST)
	@cp -R $(FRONTEND_DIST) $(RELEASE_ADMIN_DIR)
	@echo ">> Done: $(RELEASE_ADMIN_DIST)"

copy-mobile: ## 拷贝手机端前端到发行目录
	@echo ">> Copying mobile frontend assets..."
	@mkdir -p $(RELEASE_MOBILE_DIR)
	@rm -rf $(RELEASE_MOBILE_DIST)
	@cp -R $(MOBILE_DIST) $(RELEASE_MOBILE_DIR)
	@echo ">> Done: $(RELEASE_MOBILE_DIST)"

# 前端构建链（需要 npm）——`build`/`release` 依赖它，`build-go`/`release-go` 不依赖
build-frontend: build-admin build-mobile copy-admin copy-mobile ## 构建前端产物并拷贝到发行目录

# 纯 Go 编译当前平台（不依赖 npm/前端工具链）——后端迭代与 CI 用这个
build-go: ## 仅编译当前平台二进制（不构建前端）
	@echo ">> Building $(BINARY_NAME) ($(VERSION))..."
	@mkdir -p $(RELEASE_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(RELEASE_DIR)/$(BINARY_NAME) $(CMD_PATH)
	@echo ">> Done: $(RELEASE_DIR)/$(BINARY_NAME)"

# 纯 Go 交叉编译所有平台（不依赖 npm/前端工具链）
release-go: ## 仅交叉编译所有平台二进制（不构建前端）
	@mkdir -p $(RELEASE_DIR)
	@for target in $(TARGETS); do \
		GOOS=$${target%/*}; \
		GOARCH=$${target##*/}; \
		GOARM=""; \
		EXT=""; \
		if [ "$$GOARCH" = "armv7" ]; then GOARCH="arm"; GOARM="7"; fi; \
		if [ "$$GOOS" = "windows" ]; then EXT=".exe"; fi; \
		ARCH_TAG=$${target##*/}; \
		OUT=$(RELEASE_DIR)/$(BINARY_NAME)-$$GOOS-$$ARCH_TAG$$EXT; \
		echo ">> Building $$OUT ..."; \
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH GOARM=$$GOARM go build -trimpath -ldflags "$(LDFLAGS)" -o $$OUT $(CMD_PATH) || exit 1; \
	done
	@echo ">> All platforms built in $(RELEASE_DIR)/"

# 默认：编译当前平台（含前端）
build: ## 构建前端 + 编译当前平台二进制
	@$(MAKE) --no-print-directory build-frontend
	@$(MAKE) --no-print-directory build-go

# 交叉编译所有平台（含前端）
release: ## 交叉编译所有平台二进制（含前端）
	@$(MAKE) --no-print-directory build-frontend
	@$(MAKE) --no-print-directory release-go

clean: ## 清理编译产物
	@rm -f $(RELEASE_DIR)/$(BINARY_NAME) $(RELEASE_DIR)/$(BINARY_NAME)-*
	@rm -rf $(RELEASE_ADMIN_DIR) $(RELEASE_MOBILE_DIR)
	@echo ">> Cleaned $(BINARY_NAME) artifacts"

# 生成 latest.json 并发布到升级服务器
UPLOAD_DIR  := px:/lhcos-data/appupdater/moles
UPDATE_BASE := https://fs.px.metme.top/app/moles

# VERSION 必须是 HEAD 上的干净 tag（git describe 无 -N-g<hash> 后缀、无 -dirty）。
# 否则 latest.json 的 version 例如 "v0.5.0-26-g28e7e03" 会被 selfupdater 的
# fallbackParse 解析为 0.5.0 + pre-release，SemVer 判定其低于 0.5.0 —— 老服务端
# 会认为「无更新」，升级永远不生效。
check-tag:
	@case "$(VERSION)" in \
		*-dirty) echo "!! publish 中止：工作区有未提交改动（VERSION=$(VERSION)），请先提交"; exit 1;; \
		*-*-g*) echo "!! publish 中止：HEAD 不在 tag 上（VERSION=$(VERSION)），请先 git tag"; exit 1;; \
	esac

manifest: ## 生成 latest.json（复用 publish 的生成逻辑，不含上传；CI/本地发布变体共用）
	@echo ">> Generating latest.json ..."
	@echo '{"version":"$(VERSION)","date":"$(DATE)","assets":{' > $(RELEASE_DIR)/latest.json
	@first=true; \
	for target in $(TARGETS); do \
		GOOS=$${target%/*}; \
		GOARCH=$${target##*/}; \
		EXT=""; \
		if [ "$$GOOS" = "windows" ]; then EXT=".exe"; fi; \
		FILE=$(BINARY_NAME)-$$GOOS-$$GOARCH$$EXT; \
		PATHFILE=$(RELEASE_DIR)/$$FILE; \
		if [ ! -f "$$PATHFILE" ]; then continue; fi; \
		SHA256=$$(sha256sum $$PATHFILE | cut -d' ' -f1); \
		SIZE=$$(stat -c%s $$PATHFILE); \
		if [ "$$first" = true ]; then first=false; else echo ',' >> $(RELEASE_DIR)/latest.json; fi; \
		printf '"%s/%s":{"url":"$(UPDATE_BASE)/%s","sha256":"%s","size":%d}' \
			$$GOOS $$GOARCH $$FILE $$SHA256 $$SIZE >> $(RELEASE_DIR)/latest.json; \
	done; \
	echo '}}' >> $(RELEASE_DIR)/latest.json
	@echo ">> latest.json:"
	@cat $(RELEASE_DIR)/latest.json | python3 -m json.tool

publish: check-tag release manifest ## 编译所有平台、生成 latest.json 并发布到升级服务器
	@echo ">> Uploading to $(UPLOAD_DIR) ..."
	@scp -r $(RELEASE_DIR)/$(BINARY_NAME)-* $(RELEASE_DIR)/latest.json $(UPLOAD_DIR)/
	@echo ">> Published $(VERSION) to $(UPLOAD_DIR)/"

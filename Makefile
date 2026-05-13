.PHONY: help build release clean build-admin build-mobile copy-admin copy-mobile publish

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
RELEASE_DIR    := ../_release/moles
FRONTEND_DIR   := ./admin
FRONTEND_DIST  := $(FRONTEND_DIR)/dist
MOBILE_DIR     := ./mobile
MOBILE_DIST    := $(MOBILE_DIR)/dist
RELEASE_ADMIN_DIR  := $(RELEASE_DIR)/admin
RELEASE_ADMIN_DIST := $(RELEASE_ADMIN_DIR)/dist
RELEASE_MOBILE_DIR := $(RELEASE_DIR)/mobile
RELEASE_MOBILE_DIST := $(RELEASE_MOBILE_DIR)/dist

# 版本信息（从 git tag 获取，无 tag 时用 dev）
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
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

# 默认：编译当前平台
build: build-admin build-mobile copy-admin copy-mobile ## 构建前端 + 编译当前平台二进制
	@echo ">> Building $(BINARY_NAME) ($(VERSION))..."
	@mkdir -p $(RELEASE_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(RELEASE_DIR)/$(BINARY_NAME) $(CMD_PATH)
	@echo ">> Done: $(RELEASE_DIR)/$(BINARY_NAME)"

# 交叉编译所有平台
release: build-admin build-mobile copy-admin copy-mobile ## 交叉编译所有平台二进制
	@mkdir -p $(RELEASE_DIR)
	@for target in $(TARGETS); do \
		GOOS=$${target%/*}; \
		GOARCH=$${target##*/}; \
		EXT=""; \
		if [ "$$GOOS" = "windows" ]; then EXT=".exe"; fi; \
		OUT=$(RELEASE_DIR)/$(BINARY_NAME)-$$GOOS-$$GOARCH$$EXT; \
		echo ">> Building $$OUT ..."; \
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH go build -trimpath -ldflags "$(LDFLAGS)" -o $$OUT $(CMD_PATH); \
	done
	@echo ">> All platforms built in $(RELEASE_DIR)/"

clean: ## 清理编译产物
	@rm -f $(RELEASE_DIR)/$(BINARY_NAME) $(RELEASE_DIR)/$(BINARY_NAME)-*
	@rm -rf $(RELEASE_ADMIN_DIR) $(RELEASE_MOBILE_DIR)
	@echo ">> Cleaned $(BINARY_NAME) artifacts"

# 生成 latest.json 并发布到升级服务器
UPLOAD_DIR  := px:/lhcos-data/appupdater/moles
UPDATE_BASE := https://fs.px.metme.top/app/moles

publish: release ## 编译所有平台并发布到升级服务器
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
	@echo ">> Uploading to $(UPLOAD_DIR) ..."
	@scp -r $(RELEASE_DIR)/$(BINARY_NAME)-* $(RELEASE_DIR)/latest.json $(UPLOAD_DIR)/
	@echo ">> Published $(VERSION) to $(UPLOAD_DIR)/"

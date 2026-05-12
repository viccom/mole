.PHONY: build release clean build-admin build-mobile copy-admin copy-mobile

# 构建变量
BINARY_NAME    := moleagent-serv
CMD_PATH       := ./cmd/moleagent-serv
RELEASE_DIR    := ../_release
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
build-admin:
	@echo ">> Building admin frontend..."
	@cd $(FRONTEND_DIR) && npm run build
	@echo ">> Done: $(FRONTEND_DIST)"

# 构建手机端前端
build-mobile:
	@echo ">> Building mobile frontend..."
	@cd $(MOBILE_DIR) && npm run build
	@echo ">> Done: $(MOBILE_DIST)"

# 拷贝前端构建产物到发行目录
copy-admin:
	@echo ">> Copying admin frontend assets..."
	@mkdir -p $(RELEASE_ADMIN_DIR)
	@rm -rf $(RELEASE_ADMIN_DIST)
	@cp -R $(FRONTEND_DIST) $(RELEASE_ADMIN_DIR)
	@echo ">> Done: $(RELEASE_ADMIN_DIST)"

copy-mobile:
	@echo ">> Copying mobile frontend assets..."
	@mkdir -p $(RELEASE_MOBILE_DIR)
	@rm -rf $(RELEASE_MOBILE_DIST)
	@cp -R $(MOBILE_DIST) $(RELEASE_MOBILE_DIR)
	@echo ">> Done: $(RELEASE_MOBILE_DIST)"

# 默认：编译当前平台
build: build-admin build-mobile copy-admin copy-mobile
	@echo ">> Building $(BINARY_NAME) ($(VERSION))..."
	@mkdir -p $(RELEASE_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(RELEASE_DIR)/$(BINARY_NAME) $(CMD_PATH)
	@echo ">> Done: $(RELEASE_DIR)/$(BINARY_NAME)"

# 交叉编译所有平台
release: build-admin build-mobile copy-admin copy-mobile
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

clean:
	@rm -f $(RELEASE_DIR)/$(BINARY_NAME) $(RELEASE_DIR)/$(BINARY_NAME)-*
	@rm -rf $(RELEASE_ADMIN_DIR) $(RELEASE_MOBILE_DIR)
	@echo ">> Cleaned $(BINARY_NAME) artifacts"

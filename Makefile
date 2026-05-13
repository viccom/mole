.PHONY: build release clean ensure-wails build-desktop build-manager build-gui build-all

# This Makefile assumes a POSIX shell environment such as Git Bash/MSYS on Windows.
# For native PowerShell builds, use scripts/build.ps1 and scripts/release.ps1.

# 构建变量
BINARY_NAME    := moleagent-client
CMD_PATH       := ./cmd/moleagent-client
RELEASE_DIR    := ../_release
DESKTOP_DIR    := ./cmd/moleagent-desktop
DESKTOP_NAME   := moleAgent-desktop
MANAGER_DIR    := ./cmd/moleagent-manager
MANAGER_NAME   := moleAgent-manager
WAILS_INSTALL  := github.com/wailsapp/wails/v2/cmd/wails@latest
HOST_EXE       := $(if $(filter Windows_NT,$(OS)),.exe,)

# 版本信息（从 git tag 获取，无 tag 时用 dev）
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD    := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE     := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

# 编译标志
LDFLAGS  := -s -w \
            -X moleAgent_client/internal/version.Version=$(VERSION) \
            -X moleAgent_client/internal/version.GitHash=$(BUILD) \
            -X moleAgent_client/internal/version.BuildDate=$(DATE)

# 交叉编译目标（armv7 → GOARCH=arm, GOARM=7）
TARGETS := \
	linux/amd64 \
	linux/arm64 \
	linux/armv7 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64

ensure-wails:
	@WAILS_BIN=$$(command -v wails 2>/dev/null); \
	if [ -z "$$WAILS_BIN" ]; then \
		GOBIN=$$(go env GOBIN); \
		if [ -z "$$GOBIN" ]; then GOBIN="$$(go env GOPATH)/bin"; fi; \
		if [ -x "$$GOBIN/wails" ]; then WAILS_BIN="$$GOBIN/wails"; \
		elif [ -x "$$GOBIN/wails.exe" ]; then WAILS_BIN="$$GOBIN/wails.exe"; fi; \
	fi; \
	if [ -z "$$WAILS_BIN" ]; then \
		echo ">> Wails not found, installing $(WAILS_INSTALL) ..."; \
		go install $(WAILS_INSTALL); \
		GOBIN=$$(go env GOBIN); \
		if [ -z "$$GOBIN" ]; then GOBIN="$$(go env GOPATH)/bin"; fi; \
		if [ -x "$$GOBIN/wails" ]; then WAILS_BIN="$$GOBIN/wails"; \
		elif [ -x "$$GOBIN/wails.exe" ]; then WAILS_BIN="$$GOBIN/wails.exe"; fi; \
	fi; \
	if [ -z "$$WAILS_BIN" ]; then \
		echo "!! Failed to locate wails after installation. Please add Go bin to PATH."; \
		exit 1; \
	fi; \
	echo ">> Using Wails: $$WAILS_BIN"

# 默认：编译当前平台
build:
	@echo ">> Building $(BINARY_NAME) ($(VERSION))..."
	@mkdir -p $(RELEASE_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(RELEASE_DIR)/$(BINARY_NAME) $(CMD_PATH)
	@echo ">> Done: $(RELEASE_DIR)/$(BINARY_NAME)"

build-desktop: ensure-wails
	@echo ">> Building $(DESKTOP_NAME) ($(VERSION))..."
	@mkdir -p $(RELEASE_DIR)
	@WAILS_BIN=$$(command -v wails 2>/dev/null); \
	if [ -z "$$WAILS_BIN" ]; then \
		GOBIN=$$(go env GOBIN); \
		if [ -z "$$GOBIN" ]; then GOBIN="$$(go env GOPATH)/bin"; fi; \
		if [ -x "$$GOBIN/wails" ]; then WAILS_BIN="$$GOBIN/wails"; \
		elif [ -x "$$GOBIN/wails.exe" ]; then WAILS_BIN="$$GOBIN/wails.exe"; fi; \
	fi; \
	(cd $(DESKTOP_DIR) && "$$WAILS_BIN" build -clean -o $(DESKTOP_NAME)$(HOST_EXE) -ldflags "$(LDFLAGS)"); \
	cp -f $(DESKTOP_DIR)/build/bin/$(DESKTOP_NAME)$(HOST_EXE) $(RELEASE_DIR)/$(DESKTOP_NAME)$(HOST_EXE)
	@echo ">> Done: $(RELEASE_DIR)/$(DESKTOP_NAME)$(HOST_EXE)"

build-manager: ensure-wails
	@echo ">> Building $(MANAGER_NAME) ($(VERSION))..."
	@mkdir -p $(RELEASE_DIR)
	@WAILS_BIN=$$(command -v wails 2>/dev/null); \
	if [ -z "$$WAILS_BIN" ]; then \
		GOBIN=$$(go env GOBIN); \
		if [ -z "$$GOBIN" ]; then GOBIN="$$(go env GOPATH)/bin"; fi; \
		if [ -x "$$GOBIN/wails" ]; then WAILS_BIN="$$GOBIN/wails"; \
		elif [ -x "$$GOBIN/wails.exe" ]; then WAILS_BIN="$$GOBIN/wails.exe"; fi; \
	fi; \
	(cd $(MANAGER_DIR) && "$$WAILS_BIN" build -clean -o $(MANAGER_NAME)$(HOST_EXE) -ldflags "$(LDFLAGS)"); \
	cp -f $(MANAGER_DIR)/build/bin/$(MANAGER_NAME)$(HOST_EXE) $(RELEASE_DIR)/$(MANAGER_NAME)$(HOST_EXE)
	@echo ">> Done: $(RELEASE_DIR)/$(MANAGER_NAME)$(HOST_EXE)"

build-gui: build-desktop build-manager

build-all: build build-gui

# 交叉编译所有平台
release:
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
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH GOARM=$$GOARM go build -trimpath -ldflags "$(LDFLAGS)" -o $$OUT $(CMD_PATH); \
	done
	@echo ">> All platforms built in $(RELEASE_DIR)/"

clean:
	@rm -f $(RELEASE_DIR)/$(BINARY_NAME) $(RELEASE_DIR)/$(BINARY_NAME)-*
	@rm -f $(RELEASE_DIR)/$(DESKTOP_NAME)$(HOST_EXE) $(RELEASE_DIR)/$(MANAGER_NAME)$(HOST_EXE)
	@echo ">> Cleaned $(BINARY_NAME) artifacts"

# 生成 latest.json 并发布到升级服务器
UPLOAD_DIR  := px:/lhcos-data/appupdater/molec
UPDATE_BASE := https://fs.px.metme.top/app/molec

publish: release
	@echo ">> Generating latest.json ..."
	@echo '{"version":"$(VERSION)","date":"$(DATE)","assets":{' > $(RELEASE_DIR)/latest.json
	@first=true; \
	for target in $(TARGETS); do \
		GOOS=$${target%/*}; \
		GOARCH=$${target##*/}; \
		GOARM=""; \
		EXT=""; \
		if [ "$$GOARCH" = "armv7" ]; then GOARCH="arm"; GOARM="7"; fi; \
		if [ "$$GOOS" = "windows" ]; then EXT=".exe"; fi; \
		ARCH_TAG=$${target##*/}; \
		FILE=$(BINARY_NAME)-$$GOOS-$$ARCH_TAG$$EXT; \
		PATHFILE=$(RELEASE_DIR)/$$FILE; \
		if [ ! -f "$$PATHFILE" ]; then continue; fi; \
		SHA256=$$(sha256sum $$PATHFILE | cut -d' ' -f1); \
		SIZE=$$(stat -c%s $$PATHFILE); \
		if [ "$$first" = true ]; then first=false; else echo ',' >> $(RELEASE_DIR)/latest.json; fi; \
		printf '"%s/%s":{"url":"$(UPDATE_BASE)/%s","sha256":"%s","size":%d}' \
			$$GOOS $$ARCH_TAG $$FILE $$SHA256 $$SIZE >> $(RELEASE_DIR)/latest.json; \
	done; \
	echo '}}' >> $(RELEASE_DIR)/latest.json
	@echo ">> latest.json:"
	@cat $(RELEASE_DIR)/latest.json | python3 -m json.tool
	@echo ">> Uploading to $(UPLOAD_DIR) ..."
	@scp -r $(RELEASE_DIR)/$(BINARY_NAME)-* $(RELEASE_DIR)/latest.json $(UPLOAD_DIR)/
	@echo ">> Published $(VERSION) to $(UPLOAD_DIR)/"

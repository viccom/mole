.PHONY: build release clean

# This Makefile assumes a POSIX shell environment such as Git Bash/MSYS on Windows.
# For native PowerShell builds, use scripts/build.ps1 and scripts/release.ps1.

# 构建变量
BINARY_NAME    := moleagent-client
CMD_PATH       := ./cmd/moleagent-client
RELEASE_DIR    := ../_release

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

# 默认：编译当前平台
build:
	@echo ">> Building $(BINARY_NAME) ($(VERSION))..."
	@mkdir -p $(RELEASE_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(RELEASE_DIR)/$(BINARY_NAME) $(CMD_PATH)
	@echo ">> Done: $(RELEASE_DIR)/$(BINARY_NAME)"

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
	@echo ">> Cleaned $(BINARY_NAME) artifacts"

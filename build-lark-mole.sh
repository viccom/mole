#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# build-lark-mole.sh — 飞书版 moleAgent-Serv 发行编译脚本
# 分支: feature/feishu-auth | 目标: linux/amd64
# 产物: /root/gitme/lark-mole/
# ============================================================

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
OUTPUT_DIR="/root/gitme/lark-mole"
GOOS="linux"
GOARCH="amd64"
BINARY="moleagent-serv"

echo "=========================================="
echo "  Lark-Mole Release Build"
echo "  Repo:   $REPO_DIR"
echo "  Output: $OUTPUT_DIR"
echo "  Target: $GOOS/$GOARCH"
echo "=========================================="

cd "$REPO_DIR"

# --- 版本信息 ---
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS="-s -w -X moleAgent_Serv/internal/version.Version=${VERSION} -X moleAgent_Serv/internal/version.GitHash=${BUILD} -X moleAgent_Serv/internal/version.BuildDate=${DATE}"

echo ""
echo "[1/5] Building admin frontend..."
cd "$REPO_DIR/admin"
npm ci --silent 2>/dev/null || npm install --silent
npm run build

echo ""
echo "[2/5] Building mobile frontend..."
cd "$REPO_DIR/mobile"
npm ci --silent 2>/dev/null || npm install --silent
npm run build

echo ""
echo "[3/5] Compiling Go binary ($GOOS/$GOARCH)..."
cd "$REPO_DIR"
mkdir -p "$OUTPUT_DIR"
CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH go build \
  -trimpath -ldflags "$LDFLAGS" \
  -o "$OUTPUT_DIR/$BINARY" \
  ./cmd/moleagent-serv

echo ""
echo "[4/5] Copying frontend assets..."
rm -rf "$OUTPUT_DIR/admin" "$OUTPUT_DIR/mobile"
mkdir -p "$OUTPUT_DIR/admin" "$OUTPUT_DIR/mobile"
cp -R "$REPO_DIR/admin/dist/." "$OUTPUT_DIR/admin/dist/"
cp -R "$REPO_DIR/mobile/dist/." "$OUTPUT_DIR/mobile/dist/"

echo ""
echo "[5/5] Copying configs..."
mkdir -p "$OUTPUT_DIR/configs"
cp "$REPO_DIR/configs/config.example.yaml" "$OUTPUT_DIR/configs/"

echo ""
echo "=========================================="
echo "  Build complete!"
echo "  Version:  $VERSION ($BUILD)"
echo "  Date:     $DATE"
echo ""
ls -lh "$OUTPUT_DIR/$BINARY"
du -sh "$OUTPUT_DIR/admin/dist"
du -sh "$OUTPUT_DIR/mobile/dist"
echo ""
echo "  Output: $OUTPUT_DIR/"
ls "$OUTPUT_DIR/"
echo "=========================================="

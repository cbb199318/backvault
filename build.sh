#!/bin/bash
# 加密备份 - 一键构建：前端 → Go 后端(linux/amd64) → 组装 fpk 包目录 → fnpack 打包
set -euo pipefail
cd "$(dirname "$0")"

APPDIR="encbackup"
FNPACK="../tools/fnpack"

echo "== 1/4 前端构建 (Vue3 + Vite) =="
(cd frontend && npm install --silent && npm run build)

echo "== 2/4 后端构建 (Go → linux/amd64) =="
mkdir -p "${APPDIR}/app/server/public" "${APPDIR}/app/server/engines"
(cd backend && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o ../${APPDIR}/app/server/encbackup-server .)

echo "== 3/5 组装静态资源 =="
rm -rf "${APPDIR}/app/server/public"
cp -R frontend/dist "${APPDIR}/app/server/public"
chmod +x "${APPDIR}/cmd/main" "${APPDIR}/app/server/encbackup-server"

echo "== 4/5 引擎二进制 (restic + rclone, linux/amd64) =="
BIN_SRC="${ENGBIN:-/tmp/encbin}"
for f in restic rclone; do
  if [ ! -x "${BIN_SRC}/${f}" ]; then
    echo "缺少 ${BIN_SRC}/${f}，请先下载 linux/amd64 二进制"; exit 1
  fi
  cp "${BIN_SRC}/${f}" "${APPDIR}/app/server/engines/${f}"
  chmod +x "${APPDIR}/app/server/engines/${f}"
done
ls -lh "${APPDIR}/app/server/engines/"

echo "== 5/5 打包 =="
if [ "${1:-fpk}" = "docker" ]; then
  echo "   → Docker 镜像"
  docker build -t backvault:latest .
  echo ""
  echo "镜像构建完成：backvault:latest"
  echo "运行示例："
  echo "  docker run -d --name backvault -p 5175:5175 \\"
  echo "    -v \$(pwd)/data:/data -v /你的备份目录:/src:ro \\"
  echo "    -e AUTH_USER=admin -e AUTH_PASS=改成强密码 -e USER_ROOT=/src \\"
  echo "    backvault:latest"
  exit 0
fi

echo "   → fnpack"
"${FNPACK}" build --directory "${APPDIR}"

ls -lh *.fpk

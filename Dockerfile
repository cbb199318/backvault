# 加密备份 - Docker 镜像（自包含：Go 后端 + restic/rclone 引擎 + Web UI）
# 无需宿主机安装任何额外工具。
#
# 构建（在本目录下执行，会先组装产物再打镜像）：
#   ./build.sh docker
#
# 运行示例：
#   docker run -d --name backvault \
#     -p 5175:5175 \
#     -v $(pwd)/data:/data \
#     -v /你的备份源目录:/src:ro \
#     -e AUTH_USER=admin -e AUTH_PASS=请改成强密码 \
#     -e USER_ROOT=/src \
#     backvault:latest
#
# 说明：
#   - /data 存放 config.json、rclone.conf、日志、缓存（升级镜像不丢数据）
#   - USER_ROOT 是 Web 界面「我的文件」的根目录，指向挂载进来的备份源
#   - AUTH_PASS 非空时启用 HTTP Basic Auth 登录（裸端口暴露必须设置）
#   - 引擎二进制当前为 linux/amd64；arm64 需替换 engines 后重新构建

FROM alpine:3.20

RUN sed -i 's#https://dl-cdn.alpinelinux.org#https://mirrors.ustc.edu.cn#g' /etc/apk/repositories \
    && apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 1000 backup

WORKDIR /app
COPY encbackup/app/server/encbackup-server ./encbackup-server
COPY encbackup/app/server/engines/ ./engines/
COPY encbackup/app/server/public/ ./public/
RUN chmod +x ./encbackup-server ./engines/*

ENV TRIM_PKGVAR=/data \
    HOST=0.0.0.0 \
    PORT=5175

VOLUME /data
EXPOSE 5175

USER backup
ENTRYPOINT ["/app/encbackup-server"]

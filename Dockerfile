# syntax=docker/dockerfile:1

# 使用官方 Golang 镜像作为构建环境
FROM golang:1.23 AS builder

# 设置工作目录
WORKDIR /app

# 复制 Go 模块文件并下载依赖（如果适用）
COPY go.mod go.sum ./
RUN go mod download

# 复制项目源代码
COPY . .

# 将配置文件复制到容器内的正确位置
COPY config/app.yaml /app/config/app.yaml

# 构建应用程序
RUN go build -o shourlink .

# 使用更小的基础镜像来运行应用
FROM alpine:latest

# 设置工作目录
WORKDIR /root/

# 从构建阶段复制编译好的二进制文件和配置文件
COPY --from=builder /app/shourlink .
COPY --from=builder /app/config/app.yaml /app/config/app.yaml

# 暴露应用程序的端口（根据需要修改）
EXPOSE 80

# 启动命令
CMD ["./shourlink"]
# 第一阶段：构建阶段
FROM golang:1.22-alpine AS builder

WORKDIR /build

COPY main.go .

# 显式设置 module 名称，防止 go mod tidy 出问题
RUN go mod init autodownloader || true
RUN go mod tidy

# 静态编译，适配alpine（musl libc）
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o autodownloader main.go

# 第二阶段：运行阶段
FROM alpine:latest

WORKDIR /app

# 复制可执行文件
COPY --from=builder /build/autodownloader .

# 创建挂载点，比如 /data 给 url.txt 用（可选）
RUN mkdir -p /data

# 默认执行
ENTRYPOINT ["./autodownloader"]

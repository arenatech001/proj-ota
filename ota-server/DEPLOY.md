# OTA Update Server 部署指南

## 快速开始

### 1. 安装依赖

```bash
npm install
```

### 2. 配置环境变量

复制 `.env.example` 为 `.env` 并根据实际情况修改：

```bash
cp .env.example .env
```

主要配置项：
- `PORT`: 服务器端口（默认: 3000）
- `HOST`: 监听地址（默认: 0.0.0.0）
- `BASE_URL`: 服务器基础 URL（用于生成配置文件中的下载地址）
- `APPS_DIR`: 应用目录（默认: ./apps）
- `RESTART_CMD`: 重启命令（可选）

### 3. 启动服务器

```bash
# 开发环境
npm start

# 或使用 PM2（推荐生产环境）
pm2 start server.js --name ota-server

# 或使用 systemd
sudo systemctl start ota-server
```

## 更新版本

使用 `update-version.py` 脚本更新版本：

```bash
# 基本用法
python3 update-version.py <app_name> <version> --file <file_spec>

# 示例
python3 update-version.py myapp 1.0.0 \
  --file ./myapp:myapp:/usr/bin/myapp:false

# 多文件
python3 update-version.py myapp 1.0.0 \
  --file ./app1:app1:/usr/bin/app1:false \
  --file ./app2:app2:/usr/bin/app2:true

# 使用 JSON 配置文件
python3 update-version.py myapp 1.0.0 --config files.json

# 查看帮助
python3 update-version.py --help
```

脚本会自动：
1. 复制文件到 `apps/<app_name>/files/` 目录
2. 计算文件的 SHA256 校验和
3. 生成并更新 `apps/<app_name>/version.yaml` 配置文件

## 部署到云端

### 使用 PM2（推荐）

```bash
# 安装 PM2
npm install -g pm2

# 启动服务
pm2 start server.js --name ota-server

# 设置开机自启
pm2 startup
pm2 save

# 查看状态
pm2 status

# 查看日志
pm2 logs ota-server
```

### 使用 systemd

创建 `/etc/systemd/system/ota-server.service`:

```ini
[Unit]
Description=OTA Update Server
After=network.target

[Service]
Type=simple
User=www-data
WorkingDirectory=/opt/ota-server
Environment="NODE_ENV=production"
Environment="PORT=3000"
Environment="BASE_URL=https://your-domain.com"
Environment="APPS_DIR=/opt/ota-server/apps"
ExecStart=/usr/bin/node /opt/ota-server/server.js
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

启动服务：

```bash
sudo systemctl daemon-reload
sudo systemctl enable ota-server
sudo systemctl start ota-server
sudo systemctl status ota-server
```

### 使用 Docker

创建 `Dockerfile`:

```dockerfile
FROM node:18-alpine

WORKDIR /app

COPY package*.json ./
RUN npm install --production

COPY server.js update-version.py ./
RUN mkdir -p apps

EXPOSE 3000

ENV PORT=3000
ENV HOST=0.0.0.0
ENV APPS_DIR=/app/apps

CMD ["node", "server.js"]
```

构建和运行：

```bash
docker build -t ota-server .
docker run -d \
  -p 3000:3000 \
  -v $(pwd)/apps:/app/apps \
  -e BASE_URL=https://your-domain.com \
  --name ota-server \
  ota-server
```

### 使用 Nginx 反向代理

Nginx 配置示例：

```nginx
server {
    listen 80;
    server_name your-domain.com;

    location /ota {
        proxy_pass http://localhost:3000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_cache_bypass $http_upgrade;
    }
}
```

## 使用 HTTPS

如果需要 HTTPS，可以使用 Nginx 作为反向代理并配置 SSL 证书，或者使用 Node.js 的 HTTPS 模块（需要修改 `server.js`）。

## 监控和日志

### 健康检查

服务器提供健康检查端点：

```bash
curl http://localhost:3000/health
```

### 查看信息

```bash
curl http://localhost:3000/info
```

### 日志

服务器日志输出到标准输出，可以使用以下方式收集：

- PM2: `pm2 logs ota-server`
- systemd: `journalctl -u ota-server -f`
- Docker: `docker logs ota-server`

## 安全建议

1. **使用 HTTPS**: 在生产环境中始终使用 HTTPS
2. **访问控制**: 考虑添加认证机制（可以修改 `server.js`）
3. **文件权限**: 确保二进制文件目录有适当的权限
4. **防火墙**: 只开放必要的端口
5. **定期更新**: 保持 Node.js 和依赖包的最新版本

## 故障排查

### 服务器无法启动

1. 检查端口是否被占用：`lsof -i :3000`
2. 检查环境变量配置
3. 检查文件权限

### 无法下载二进制文件

1. 检查 `APPS_DIR` 目录是否存在
2. 检查应用目录是否存在（`apps/<app_name>/files/`）
3. 检查二进制文件是否存在
4. 检查文件权限

### 配置文件错误

1. 检查 `APPS_DIR` 目录是否存在
2. 检查配置文件是否存在（`apps/<app_name>/version.yaml`）
3. 检查 YAML 格式是否正确
4. 使用 `update-version.py` 重新生成配置

## 示例工作流

```bash
# 1. 构建新的二进制文件
go build -o myapp main.go

# 2. 更新版本
python3 update-version.py myapp 1.0.1 --file ./myapp:myapp:/usr/bin/myapp:false

# 3. 验证配置
cat version.yaml

# 4. 重启服务器（如果需要）
pm2 restart ota-server

# 5. 测试
curl http://localhost:3000/version.yaml
```


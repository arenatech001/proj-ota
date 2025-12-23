# OTA Update Server

一个用于部署和管理二进制文件 OTA（Over-The-Air）更新的服务器，支持多应用、多文件的版本控制。

## 功能特性

- ✅ **多应用支持**: 支持多个应用的独立版本控制
- ✅ **多文件支持**: 每个应用可以同时更新多个文件
- ✅ **HTTP 服务器**: 提供配置文件和二进制文件下载服务
- ✅ **自动版本管理**: 通过脚本自动更新版本和文件信息
- ✅ **SHA256 校验**: 自动计算和验证文件校验和
- ✅ **健康检查**: 提供健康检查端点用于监控
- ✅ **CORS 支持**: 支持跨域请求
- ✅ **生产就绪**: 支持环境变量配置、错误处理、日志记录、优雅关闭
- ✅ **云端部署**: 支持 PM2、systemd、Docker 等多种部署方式

## 快速开始

### 1. 安装依赖

```bash
npm install
```

### 2. 启动服务器

```bash
npm start
```

服务器将在 `http://localhost:3000` 启动。

### 3. 更新应用版本

```bash
# 更新应用 myapp 到版本 1.0.0
python3 update-version.py myapp 1.0.0 \
  --file ./app:app:/usr/bin/app:false \
  --file ./lib:lib:/usr/lib/lib.so:false
```

**注意**: Python 3 是必需的。如果系统没有安装，请先安装 Python 3。

## 环境变量配置

可以通过环境变量配置服务器：

```bash
export PORT=3000                    # 服务器端口（默认: 3000）
export HOST=0.0.0.0                 # 监听地址（默认: 0.0.0.0）
export BASE_URL=https://your-domain.com  # 服务器基础 URL
export APPS_DIR=./apps              # 应用目录（默认: ./apps）
export RESTART_CMD="systemctl restart myservice"  # 全局重启命令（可选）
```

## API 端点

| 端点 | 说明 |
|------|------|
| `GET /ota/<app_name>/version.yaml` | 获取应用配置文件 |
| `GET /ota/<app_name>/files/<filename>` | 下载应用文件 |
| `GET /ota/<app_name>/info` | 获取应用信息 |
| `GET /ota/<app_name>/agents` | 查看应用的所有 agent 状态 |
| `GET /info` | 列出所有应用 |
| `GET /health` | 健康检查 |


## 目录结构

```
ota-server/
├── server.js              # 主服务器文件
├── update-version.py      # 版本更新脚本（Python）
├── package.json           # Node.js 项目配置
├── apps/                  # 应用目录
│   ├── app1/              # app1 的应用目录
│   │   ├── files/         # app1 的文件目录
│   │   │   ├── file1
│   │   │   └── file2
│   │   └── version.yaml   # app1 的配置文件
│   └── app2/              # app2 的应用目录
│       ├── files/         # app2 的文件目录
│       │   └── file1
│       └── version.yaml   # app2 的配置文件
├── README.md              # 本文件
└── DEPLOY.md              # 部署文档
```

## 使用示例

### 更新单个应用版本

```bash
# 基本用法：更新 myapp 到版本 1.0.0
python3 update-version.py myapp 1.0.0 \
  --file ./myapp:myapp:/usr/bin/myapp:false

# 更新多个文件
python3 update-version.py myapp 1.0.0 \
  --file ./app:app:/usr/bin/app:false \
  --file ./lib:lib:/usr/lib/lib.so:false \
  --file ./config:config:/etc/app/config.json:false
```

### 使用 JSON 配置文件

创建 `files.json`:

```json
{
  "files": [
    {
      "path": "./app",
      "name": "app",
      "target": "/usr/bin/app"
    },
    {
      "path": "./lib.so",
      "name": "lib",
      "target": "/usr/lib/lib.so"
    }
  ],
   "restart_cmd": "xxx"
}
```

更新版本：

```bash
python3 update-version.py myapp 1.0.0 --config files.json
```

### 查看应用信息

```bash
# 查看所有应用
curl http://localhost:3000/info

# 查看特定应用信息
curl http://localhost:3000/ota/myapp/info

# 获取应用配置
curl http://localhost:3000/ota/myapp/version.yaml
```

## 配置文件格式

服务器生成的配置文件格式：

```yaml
version: "1.0.0"
files:
  - name: "app"
    url: "http://localhost:3000/ota/myapp/files/app"
    sha256: "abc123def456..."
    target: "/usr/bin/app"
    version: "1.0.0"
    restart: false
  - name: "lib"
    url: "http://localhost:3000/ota/myapp/files/lib.so"
    sha256: "def456ghi789..."
    target: "/usr/lib/lib.so"
    version: "1.0.0"
    restart: false
restart_cmd: "systemctl restart myapp"
```

### 字段说明

- `version`: 整体版本号
- `files`: 文件列表（必需，至少一个文件）
  - `name`: 文件名称/标识（必需）
  - `url`: 文件下载 URL（必需）
  - `sha256`: 文件 SHA256 校验和（必需）
  - `target`: 目标文件路径（必需）
  - `version`: 文件版本号（可选，默认使用整体版本）
  - `restart`: 是否在更新后重启（可选，默认 false）
- `restart_cmd`: 全局重启命令（可选，在所有文件更新完成后执行）

## 客户端使用

OTA agent 客户端使用应用专属的配置 URL：

```bash
# myapp 的 OTA agent
./ota-agent \
  -config-url="http://server.com/ota/myapp/version.yaml" \
  -version-file="/var/lib/ota-agent/myapp/version" \
  -agent-id="server-001" \
  -check-interval=5m \
  -daemon=true
```

**Agent ID**: 建议为每个 agent 实例设置唯一的标识符（如服务器主机名、设备 ID 等），这样可以在服务器端准确跟踪每个 agent 的状态。如果不提供，服务器将使用 IP 地址作为标识符。

详细说明请参考 [../ota-agent/README.md](../ota-agent/README.md)

## 工作流程

1. **构建二进制文件**: 编译或构建你的应用程序
2. **更新版本**: 使用 `update-version.py` 脚本更新版本
   - 脚本会自动计算文件的 SHA256 校验和
   - 自动生成配置文件
   - 将文件复制到应用专属目录
3. **部署服务器**: 将服务器部署到云端
4. **客户端更新**: OTA agent 客户端自动检测并下载更新

## 部署

详细的部署说明请参考 [DEPLOY.md](./DEPLOY.md)

### 使用 PM2（推荐）

```bash
pm2 start server.js --name ota-server
pm2 save
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
Environment="PORT=3000"
Environment="BASE_URL=https://your-domain.com"
Environment="APPS_DIR=/opt/ota-server/apps"
ExecStart=/usr/bin/node /opt/ota-server/server.js
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

### 使用 Docker

创建 `Dockerfile`:

```dockerfile
FROM node:18-alpine

WORKDIR /app

COPY package*.json ./
RUN npm install --production

COPY server.js update-version.py ./
RUN apk add --no-cache python3 py3-pip
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

## 开发

### 开发模式

```bash
# 启动开发服务器（使用默认配置）
npm run dev
```

### 测试更新脚本

```bash
# 创建测试二进制文件
echo '#!/bin/bash\necho "Test v1.0.0"' > test-binary
chmod +x test-binary

# 更新版本
python3 update-version.py test-app 1.0.0 \
  --file ./test-binary:test:/usr/bin/test:false
```

## 多应用支持

OTA Update Server 支持多个应用的独立版本控制。每个应用有自己独立的配置文件和文件存储。

### URL 格式

**多应用格式**:
- **配置文件**: `http://localhost:3000/ota/<app_name>/version.yaml`
- **文件**: `http://localhost:3000/ota/<app_name>/files/<filename>`
- **应用信息**: `http://localhost:3000/ota/<app_name>/info`

### 多应用使用示例

#### 1. 更新应用版本

```bash
cd ota-server

# 更新 app1 到版本 1.0.0
python3 update-version.py app1 1.0.0 \
  --file ./app1:app1:/usr/bin/app1:false \
  --file ./lib1:lib1:/usr/lib/lib1.so:false

# 更新 app2 到版本 2.0.0
python3 update-version.py app2 2.0.0 \
  --file ./app2:app2:/usr/bin/app2:true
```

#### 2. 客户端使用

每个应用使用自己的配置 URL：

```bash
# app1 的 OTA agent
./ota-agent \
  -config-url="http://server.com/ota/app1/version.yaml" \
  -version-file="/var/lib/ota-agent/app1/version" \
  -agent-id="server-001" \
  -check-interval=5m \
  -daemon=true

# app2 的 OTA agent
./ota-agent \
  -config-url="http://server.com/ota/app2/version.yaml" \
  -version-file="/var/lib/ota-agent/app2/version" \
  -agent-id="server-002" \
  -check-interval=5m \
  -daemon=true
```

#### 3. 查看应用信息

```bash
# 查看所有应用
curl http://localhost:3000/info

# 查看特定应用信息
curl http://localhost:3000/ota/app1/info

# 查看应用的所有 agent 状态
curl http://localhost:3000/ota/app1/agents
```

### 多应用配置文件示例

#### app1 配置

```yaml
version: "1.0.0"
files:
  - name: "app1"
    url: "http://localhost:3000/ota/app1/files/app1"
    sha256: "abc123..."
    target: "/usr/bin/app1"
    version: "1.0.0"
    restart: false
  - name: "lib1"
    url: "http://localhost:3000/ota/app1/files/lib1.so"
    sha256: "def456..."
    target: "/usr/lib/lib1.so"
    version: "1.0.0"
    restart: false
restart_cmd: "systemctl restart app1"
```

#### app2 配置

```yaml
version: "2.0.0"
files:
  - name: "app2"
    url: "http://localhost:3000/ota/app2/files/app2"
    sha256: "ghi789..."
    target: "/usr/bin/app2"
    version: "2.0.0"
    restart: true
restart_cmd: "systemctl restart app2"
```

### 多应用部署建议

#### 每个应用独立的 OTA Agent

为每个应用运行独立的 OTA agent 实例：

```ini
# /etc/systemd/system/ota-agent-app1.service
[Unit]
Description=OTA Agent for App1
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/ota-agent \
  -config-url="http://server.com/ota/app1/version.yaml" \
  -version-file="/var/lib/ota-agent/app1/version" \
  -agent-id="server-001" \
  -check-interval=5m \
  -daemon=true
Restart=always

[Install]
WantedBy=multi-user.target
```

#### 版本文件组织

建议为每个应用使用独立的版本文件目录：

```
/var/lib/ota-agent/
├── app1/
│   └── version
├── app2/
│   └── version
└── app3/
    └── version
```

### 多应用支持的优势

1. **隔离性**: 每个应用的更新互不影响
2. **灵活性**: 不同应用可以使用不同的更新策略
3. **可扩展性**: 易于添加新应用
4. **管理性**: 可以独立管理每个应用的版本

## 监控和日志

### 健康检查

```bash
curl http://localhost:3000/health
```

### 查看服务器信息

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
3. 检查文件是否存在
4. 检查文件权限

### 配置文件错误

1. 检查 `APPS_DIR` 目录是否存在
2. 检查配置文件是否存在（`apps/<app_name>/version.yaml`）
3. 检查 YAML 格式是否正确
4. 使用 `update-version.py` 重新生成配置

## 许可证

MIT

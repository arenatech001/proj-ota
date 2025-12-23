# OTA Agent 客户端

Go 语言编写的 OTA（Over-The-Air）更新客户端代理。

## 功能特性

- ✅ **多文件支持**: 支持同时更新多个文件
- ✅ **守护进程模式**: 默认以守护进程模式运行，定期检查更新
- ✅ **原子替换**: 使用原子操作替换文件，确保更新安全
- ✅ **SHA256 校验**: 自动验证文件完整性
- ✅ **自动回滚**: 更新失败时自动回滚到备份版本
- ✅ **结构化日志**: 提供详细的日志输出
- ✅ **重试机制**: 网络请求支持自动重试
- ✅ **进度显示**: 下载文件时显示进度

## 编译

```bash
go build -o ota-agent main.go
```

## 使用方法

### 守护进程模式（默认）

```bash
./ota-agent \
  -config-url="http://server.com/ota/app1/version.yaml" \
  -version-file="/var/lib/ota-agent/version" \
  -agent-id="server-001" \
  -check-interval=5m \
  -daemon=true
```

### 单次运行模式

```bash
./ota-agent \
  -config-url="http://server.com/ota/app1/version.yaml" \
  -version-file="/var/lib/ota-agent/version" \
  -agent-id="server-001" \
  -daemon=false
```

## 命令行参数

- `-config-url`: 配置文件 URL（必需）
- `-version-file`: 本地版本文件路径（默认: `version`）
- `-agent-id`: Agent 标识符（可选，通过 X-Agent-ID header 发送给服务器）
- `-timeout`: HTTP 请求超时时间（默认: 30s）
- `-max-retries`: HTTP 请求最大重试次数（默认: 3）
- `-check-interval`: 守护进程模式下的检查间隔（默认: 5m）
- `-daemon`: 是否以守护进程运行（默认: true）

## 配置文件格式

客户端从服务器获取 YAML 格式的配置文件：

```yaml
version: "1.0.0"
files:
  - name: "app1"
    url: "http://server.com/ota/app1/files/app1"
    sha256: "abc123..."
    target: "/usr/bin/app1"
    version: "1.0.0"
    restart: false
  - name: "lib1"
    url: "http://server.com/ota/app1/files/lib1.so"
    sha256: "def456..."
    target: "/usr/lib/lib1.so"
    version: "1.0.0"
    restart: false
restart_cmd: "systemctl restart app1"
```

## 工作流程

1. **获取配置**: 从服务器获取版本配置文件
2. **版本比较**: 比较本地版本和远程版本
3. **文件更新**: 对每个需要更新的文件：
   - 下载到临时位置
   - 验证 SHA256 校验和
   - 原子替换目标文件
   - 更新文件版本记录
4. **全局重启**: 所有文件更新完成后执行重启命令
5. **版本记录**: 更新主版本文件

## 部署

### 作为 systemd 服务

创建 `/etc/systemd/system/ota-agent.service`:

```ini
[Unit]
Description=OTA Agent Daemon
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
RestartSec=10

[Install]
WantedBy=multi-user.target
```

启动服务：

```bash
sudo systemctl daemon-reload
sudo systemctl enable ota-agent
sudo systemctl start ota-agent
```

## 日志

守护进程模式下，日志输出到标准输出。建议重定向到日志文件：

```bash
./ota-agent -config-url="..." > /var/log/ota-agent.log 2>&1
```

或使用 systemd 的 journalctl：

```bash
journalctl -u ota-agent -f
```

## 许可证

MIT


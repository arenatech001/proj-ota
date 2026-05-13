# OTA Agent 客户端

Go 语言编写的 OTA（Over-The-Air）更新客户端代理。

## 功能特性

- ✅ **多文件支持**: 支持同时更新多个文件
- ✅ **守护进程模式**: 默认以守护进程模式运行，定期检查更新
- ✅ **单一本地配置**: 仅通过 `-config` 指定 YAML，包含 OTA 拉取、守护、多进程、Web 管理、日志上传等全部行为
- ✅ **原子替换**: 使用原子操作替换文件，确保更新安全
- ✅ **SHA256 校验**: 自动验证文件完整性
- ✅ **自动回滚**: 更新失败时自动回滚到备份版本
- ✅ **结构化日志**: 提供详细的日志输出
- ✅ **重试机制**: 网络请求支持自动重试
- ✅ **进度显示**: 下载文件时显示进度

## 编译

```bash
go build -o ota-agent .
```

## 本地配置文件（唯一入口）

除 **`-config`** 外**无其它命令行参数**。未指定 `-config` 时，默认读取与二进制同目录的 **`ota-agent.yaml`**。

### 完整示例

```yaml
# === OTA 拉取与守护 ===
config_url: "http://server.com/ota/app1/version.yaml"
version_file: "version"          # 相对路径相对 ota-agent 可执行文件目录；也可写绝对路径
agent_id: "server-001"
daemon: true                      # 可省略，默认 true；单次检查写 false
check_interval: 5m
http_timeout: 30s
download_timeout: 30m
max_retries: 3

# === 多进程监护（不经 shell；可为空列表表示不监护子进程）===
processes:
  - id: myapp
    executable: /usr/bin/myapp
    args: ["-c", "/etc/myapp.json"]
    enabled: true
    work_dir: ""

# === Web 管理（admin_listen 为空则关闭）===
admin_listen: "127.0.0.1:9001"
admin_username: admin
admin_password: "请修改为强口令"

network:
  wifi_mode: sta
  wifi_ssid: ""
  wifi_psk: ""
  wifi_iface: ""

# === 远程日志上传（enabled 可省略，默认 false）===
log_upload:
  enabled: false
  base_url: "https://ota.example.com"
  location: "site-a"
  scan_dir: "/var/log"
  glob: "*.tar.gz"
  server_glob: ""
  poll_interval: 1m
  upload_timeout: 30m
  max_upload_bytes: 524288000
  report_retries: 3
```

### 命令行

| 参数 | 说明 |
|------|------|
| `-config` | 本地 YAML 路径；**省略时** 使用 `<exe-dir>/ota-agent.yaml` |

**子命令**（须作为**第一个**参数，与运行 agent 的 `-config` 互不混写）：

| 子命令 | 说明 |
|--------|------|
| `install-systemd` | 在 Linux（systemd）上以 root 写入 `/etc/systemd/system/<unit>.service`（默认 `ota-agent`），`ExecStart` 为当前二进制绝对路径 + `-config=<绝对路径>`，执行 `systemctl daemon-reload` 与 `enable --now`。可选：`-config`、`-unit`、`-description`、`-user`（空则省略 `User=`）。 |
| `uninstall-systemd` | 停用并删除上述 unit（可选 `-unit`，默认 `ota-agent`），再 `daemon-reload`。 |

示例：

```bash
sudo ./ota-agent install-systemd -config=/etc/ota-agent/app1.yaml
sudo ./ota-agent uninstall-systemd
```

```bash
./ota-agent -config=/etc/ota-agent/app1.yaml
```

与二进制同目录放置 `ota-agent.yaml` 时可直接：

```bash
./ota-agent
```

### 单次检查（非守护）

在 YAML 中设置 `daemon: false`，仍会执行一次 OTA 检查后退出。

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

1. **读取本地 `ota-agent.yaml`**（`-config` 指定路径）
2. **获取远程 version.yaml**：`GET config_url`，请求头携带 **`X-Agent-ID`**（若配置）、**`X-Local-Version`**（当前本地版本）、**`X-Hostname`**（`os.Hostname()`，非空时），便于服务端按设备区分或统计。
3. **版本比较**: 比较本地 `version_file` 与远程版本
4. **文件更新**: 下载、校验、原子替换
5. **全局重启**: 远程 `restart_cmd`（若存在）在更新完成后由 `runCommand` 执行一次；**日常保活**由本地 `processes` 列表由 ota-agent 进程管理器负责（与远程 `restart_cmd` 独立）
6. **版本记录**: 写入 `version_file`

## 进程监控与保活

在 **daemon: true** 时：

- **多进程**：`processes` 中 `enabled: true` 的条目由 ota-agent 使用 **可执行路径 + argv** 拉起并自动重启（不经 shell）。
- **空列表**：不监护任何子进程（仅做 OTA 轮询与可选 Web/日志任务）。
- **优雅关闭**：收到信号时对子进程发 SIGTERM，随后停止管理 HTTP 等。

### 与远程 `restart_cmd` 的关系

- 远程 `version.yaml` 中的 `restart_cmd` 仅在**本轮文件全部更新成功**后执行**一次**（一次性 shell 命令），**不由** ota-agent 长期监护。
- 需要长期保活的业务进程应写在本地 YAML 的 **`processes`** 中。

### 示例：远程带 restart_cmd

远程配置：

```yaml
version: "1.0.1"
files:
  - name: "app1"
    url: "http://server.com/ota/app1/files/app1"
    sha256: "..."
    target: "/usr/bin/app1"
restart_cmd: "systemctl restart myapp"
```

更新完成后会执行上述 `restart_cmd`；同时本地 `processes` 仍可配置对 `/usr/bin/myapp` 的长期监护（若需要）。

### 容错

- 获取远程配置失败时，守护循环会在下个 `check_interval` 重试；已启动的 `processes` 不受影响。
- 若 `processes` 为空，则仅运行 OTA 与可选功能，不启动子进程。

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
ExecStart=/usr/local/bin/ota-agent -config=/etc/ota-agent/app1.yaml
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
./ota-agent -config=/etc/ota-agent/app1.yaml > /var/log/ota-agent.log 2>&1
```

或使用 systemd 的 journalctl：

```bash
journalctl -u ota-agent -f
```

## Web 管理界面（可选）

在**同一份** `ota-agent.yaml` 中设置 `admin_listen`（如 `127.0.0.1:9001`）且配置 `admin_username` / `admin_password` 后，浏览器访问该地址即可登录管理（进程表、网络、WiFi 看门狗等）。**不提供** Web 改密；修改 YAML 后需**重启** ota-agent。

- **WiFi**：`tools/wifi-watchdog.sh`；依赖 **NetworkManager**；建议 **root**。命令行参数为 **`--mode` / `--ssid` / `--psk` / `--iface`**（STA 与热点共用 SSID/PSK）。保存配置且 `network` 中 WiFi 相关字段变化时，agent 在 Linux 上会执行脚本的 **`install-timer`** 重写 systemd unit 并 **`daemon-reload`**，使定时看门狗与当前模式一致。
- **主机名**：Linux 下 `PUT /api/network/hostname` 会调用 `hostnamectl set-hostname`，并在存在 **`/boot/firmware/user-data`** 时改写其中的 **`hostname:`**（树莓派 cloud-init）。**更新主机名须重启 Linux 后方可完全生效**；响应中带 `reboot_required: true` 与说明 `message`。`GET /api/network/status` 在存在该文件时会带 `raspberry_user_data` / `raspberry_user_data_path`。

### 与 systemd `install-timer` 的关系

脚本提供 `install-timer`；**ota-agent 在写入 YAML 且 WiFi 相关字段相对上次保存有变化时自动调用**（需 root）。若与 Web「执行一次看门狗」并行，可能短时间重复 `nmcli`，属预期。

### 故障恢复

- **WiFi / 网络**：在 Web 或 YAML 中修正 `network` 并保存；若未跑 root 导致 unit 未更新，可手动执行脚本 `install-timer` 或重启 agent。
- **忘记管理密码**：编辑 `admin_password` 后重启。

## 许可证

MIT


# 远程日志上传（OTA Server + OTA Agent）

本方案**不修改** `proj-a01/cmd/client`：假定现场 **client 已按既有逻辑** 将日志打成归档（如 `.tar.gz`）并落在本机某目录。由 **ota-agent** 按 **日期区间 + glob** 选取文件并上传到 **ota-server**。

## 环境变量（ota-server）

| 变量 | 说明 | 默认 |
|------|------|------|
| `LOG_JOBS_ADMIN_TOKEN` | 管理 `POST/GET /logs/jobs` 的 Bearer Token | 与 `RECORDS_PASSWORD` 相同 |
| `LOG_UPLOAD_DIR` | 接收到的文件落盘目录 | `<ota-server>/log_uploads` |
| `LOG_UPLOAD_MAX_BYTES` | 单次上传最大字节 | `524288000`（500MiB） |

Web 页 `/logs/jobs.html` 使用与游戏记录相同的 `/game/auth` 密码登录后，将密码作为 `Authorization: Bearer` 调用 API（需与 `LOG_JOBS_ADMIN_TOKEN` 或 `RECORDS_PASSWORD` 一致）。

## Nginx 反代建议（2.3）

若经 Nginx 转发到 Node：

```nginx
client_max_body_size 500m;
proxy_read_timeout 3600s;
proxy_request_buffering off;
```

## 任务与磁盘文件的匹配规则（4.2）

- 运维在页面填写的 **date_start / date_end** 为 **YYYY-MM-DD**（按 **Agent 机器本地时区** 理解区间端点）。
- **唯一目录 `-log-scan-dir`**：client 与 server 日志均在**同一目录**下扫描。
- **Client 侧归档**：按 `-log-glob`（如 `*.tar.gz`）；**mtime 落在区间内** 的文件中取 **mtime 最新** 的一个。
- **Server 侧日志**（可选）：若 `-log-server-glob` **非空**（如 `*.log`），在**同一** `-log-scan-dir` 下按该 glob 匹配 **所有** mtime 落在区间内的普通文件（按路径排序）。**留空** 表示不采集 server 文本日志。
- **合并上传**：多文件时打临时 `tar.gz`（`client/…`、`server/01-…` 等）；单文件则直接上传原文件。
- 若现场归档命名/时间与 mtime 不一致，应调整 **glob**、**扫描目录** 或部署策略使 mtime 落入区间。

**说明**：ota-agent 仅在 **守护进程模式**（`-daemon=true`，默认）下才可能启动日志轮询；且必须显式传入 **`-log-upload=true`**（默认关闭）。单次运行模式不拉取日志任务。

## ota-agent 参数（3.x）

| 参数 | 环境变量 | 说明 |
|------|----------|------|
| `-log-upload` | — | **`false`（默认）**：不启动日志提取/上传轮询；设为 `true` 才启用 |
| `-log-base-url` | `OTA_LOG_BASE_URL` | OTA 服务根 URL，如 `https://ota.example.com`；启用 `-log-upload` 时必填 |
| `-location` | `OTA_LOCATION` | **地点**（`X-Location`），与页面任务 **location** 一致；**若设置则覆盖** `-log-location` |
| `-log-location` | `OTA_LOG_LOCATION` | 同上，当未设置 `-location` 时使用 |
| `-agent-id` | （已有） | 与任务 **agent_id** 一致 |
| `-log-scan-dir` | `OTA_LOG_SCAN_DIR` | **唯一**日志根目录（client 归档 + 可选 server 日志），默认 `/var/log` |
| `-log-glob` | `OTA_LOG_GLOB` | client：`*.tar.gz` 等 |
| `-log-server-glob` | `OTA_LOG_SERVER_GLOB` | 可选；**空** 不采 server；设为 `*.log` 时与 client 同目录下额外收集 server 日志 |
| `-log-poll-interval` | — | 默认 `1m` |
| `-log-upload-timeout` | — | 默认 `30m` |
| `-log-max-upload-bytes` | — | 默认 500MiB |
| `-log-report-retries` | — | 上报失败重试次数 |

## API 摘要

- `GET /logs/agent/next`：头 `X-Location`、`X-Agent-ID`（或 query `location`、`agent_id`）。认领一条 `pending` → `processing`。
- `POST /logs/jobs/:id/upload?token=...&location=...&agent_id=...`：body 为 **原始文件字节**（`application/octet-stream`），大小受 `LOG_UPLOAD_MAX_BYTES` 限制。
- `POST /logs/jobs/:id/report`：JSON `{"status":"failed","error":"..."}`，头 `Authorization: Bearer <upload_token>`、`X-Location`、`X-Agent-ID`。

## 验收步骤（联调 5.x）

1. 启动 ota-server，浏览器打开 `/logs/jobs.html`，登录并创建任务（**location、agent_id、日期区间** 与 agent 一致）。
2. 在 agent 机器 `-log-scan-dir` 放入符合 glob 且 mtime 在区间内的文件。
3. 启动 ota-agent（带 **`-log-upload=true`**、`-log-base-url`、`-log-location`（或 `-location`）、`-agent-id`），观察任务变 `success`，服务器 `LOG_UPLOAD_DIR` 下出现文件。
4. **错误路径**：错误 location、无匹配文件（应 `failed` 与 `error_summary`）、超大文件（413）。

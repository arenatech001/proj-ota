# OTA Agent 项目

这是一个完整的 OTA（Over-The-Air）更新解决方案，包含客户端代理和更新服务器。

## 项目结构

```
ota-agent/
├── ota-agent/            # OTA Agent 客户端（Go）
│   ├── main.go          # 主程序
│   ├── go.mod           # Go 模块文件
│   └── go.sum           # Go 依赖校验
├── ota-server/          # OTA 更新服务器（Node.js）
│   ├── server.js        # 主服务器
│   ├── update-version.py # 版本更新脚本（Python）
│   ├── binaries/        # 二进制文件存储目录
│   ├── configs/          # 配置文件目录
│   ├── package.json    # Node.js 项目配置
│   ├── README.md        # 服务器文档
│   └── DEPLOY.md        # 部署文档
```

## 组件说明

### OTA Agent 客户端 (`ota-agent/`)

Go 语言编写的客户端代理，用于：
- 从服务器获取版本配置
- 下载并验证二进制文件
- 原子替换目标文件
- 自动重启服务

**编译和使用：**

所有行为（OTA 轮询、多进程、Web 管理、日志上传等）由**一份本地 YAML** 描述；命令行**仅** `-config` 指向该文件（省略时默认使用与 `ota-agent` 二进制同目录下的 `ota-agent.yaml`）。

```bash
# 编译
cd ota-agent
go build -o ota-agent main.go

# 准备 ota-agent.yaml（示例路径与字段名见 ota-agent/README.md）
./ota-agent -config=/path/to/ota-agent.yaml

# 或与二进制同目录放置 ota-agent.yaml 后直接
./ota-agent
```

### OTA Update Server (`ota-server/`)

Node.js 编写的更新服务器，用于：
- 提供配置文件下载
- 提供二进制文件下载
- 管理版本信息
- 自动计算校验和

**快速开始：**

```bash
cd ota-server
npm install
npm start
```

详细说明请参考：
- [ota-agent/README.md](./ota-agent/README.md) - 客户端使用说明
- [ota-server/README.md](./ota-server/README.md) - 服务器使用说明（包含多应用支持指南）

## 工作流程

1. **构建应用**: 编译你的应用程序生成二进制文件
2. **部署服务器**: 将 `ota-server` 部署到云端
3. **更新版本**: 使用 `update-version.py` 脚本更新版本
4. **客户端更新**: OTA agent 客户端自动检测并下载更新

## 测试

### 端到端测试

#### 1. 启动测试服务器

```bash
cd ota-server
npm install
npm run dev
# 或
BASE_URL=http://localhost:3000 node server.js
```

服务器将在 `http://localhost:3000` 启动，提供以下端点：
- `http://localhost:3000/ota/<app_name>/version.yaml` - 应用配置文件
- `http://localhost:3000/ota/<app_name>/binary/<filename>` - 应用二进制文件下载
- `http://localhost:3000/ota/<app_name>/info` - 应用信息
- `http://localhost:3000/health` - 健康检查
- `http://localhost:3000/info` - 服务器信息（列出所有应用）

#### 2. 准备测试环境

```bash
# 编译 OTA agent
cd ota-agent
go build -o ota-agent main.go

# 创建测试目录与 OTA 版本文件目录（version_file 相对路径相对于本目录下的 ota-agent 二进制）
mkdir -p test-env/bin test-env/var/lib/ota-agent

# 创建初始版本
echo '#!/bin/bash
echo "Initial version 0.9.0"
' > test-env/bin/test-service
chmod +x test-env/bin/test-service

echo "0.9.0" > test-env/var/lib/ota-agent/version

# 写入守护进程用配置（相对路径相对于 ota-agent 所在目录）
cat > test-env/ota-agent.yaml <<'EOF'
config_url: "http://localhost:3000/ota/test-app/version.yaml"
version_file: "test-env/var/lib/ota-agent/version"
check_interval: 30s
daemon: true
EOF

# 单次运行用配置（仅检查一次更新后退出）
cat > test-env/ota-agent-once.yaml <<'EOF'
config_url: "http://localhost:3000/ota/test-app/version.yaml"
version_file: "test-env/var/lib/ota-agent/version"
check_interval: 30s
daemon: false
EOF
```

#### 3. 准备测试二进制文件

```bash
# 创建测试二进制文件
echo '#!/bin/bash
echo "Test binary version 1.0.0"
echo "This is a test binary for OTA agent"
' > test-binary
chmod +x test-binary

# 更新服务器版本（多应用、多文件）
cd ota-server
python3 update-version.py test-app 1.0.0 \
  --file ../test-binary:test-service:./test-env/bin/test-service:false
```

#### 4. 运行更新测试

```bash
# 守护进程模式（轮询检查更新）
./ota-agent -config=./test-env/ota-agent.yaml

# 单次运行模式（检查一次后退出）
./ota-agent -config=./test-env/ota-agent-once.yaml
```

#### 5. 验证更新结果

```bash
# 查看版本文件
cat test-env/var/lib/ota-agent/version
cat test-env/var/lib/ota-agent/version.test-service

# 运行更新后的二进制文件
./test-env/bin/test-service

# 再次单次运行（应跳过更新，因版本相同）
./ota-agent -config=./test-env/ota-agent-once.yaml
```

### 测试验证点

✅ **更新测试**：成功从旧版本更新到新版本
- 正确下载配置文件
- 正确下载二进制文件
- 校验和验证通过
- 原子替换成功
- 重启命令执行成功
- 版本文件更新成功

✅ **跳过更新测试**：版本相同时正确跳过更新
- 检测到版本相同
- 不执行下载和替换操作

✅ **功能验证**：
- 结构化日志输出正常
- 下载进度显示正常
- 权限检查正常工作
- 配置文件验证正常

## 许可证

MIT

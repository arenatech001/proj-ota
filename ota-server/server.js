const http = require('http');
const https = require('https');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

// 从环境变量读取配置，提供默认值
const PORT = process.env.PORT || 3000;
const HOST = process.env.HOST || '0.0.0.0';
const BASE_URL = process.env.BASE_URL || `http://localhost:${PORT}`;
const APPS_DIR = process.env.APPS_DIR || path.join(__dirname, 'apps');
const RESTART_CMD = process.env.RESTART_CMD || '';
const LOG_DIR = process.env.LOG_DIR || path.join(__dirname, 'logs');
const LOG_FILE = process.env.LOG_FILE || path.join(LOG_DIR, 'server.log');

// 确保应用目录存在
if (!fs.existsSync(APPS_DIR)) {
  fs.mkdirSync(APPS_DIR, { recursive: true });
}

// 确保日志目录存在
if (!fs.existsSync(LOG_DIR)) {
  fs.mkdirSync(LOG_DIR, { recursive: true });
}

// Agent 状态存储
// 结构: { appName: { agentId: { id, ip, lastSeen, requestCount, currentVersion, localVersion, lastAction, userAgent } } }
const agentStatus = new Map();

// 获取客户端 IP 地址
function getClientIP(req) {
  // 支持代理场景
  const forwarded = req.headers['x-forwarded-for'];
  if (forwarded) {
    return forwarded.split(',')[0].trim();
  }
  const realIP = req.headers['x-real-ip'];
  if (realIP) {
    return realIP;
  }
  return req.connection?.remoteAddress || req.socket?.remoteAddress || 'unknown';
}

// 记录 agent 状态
function recordAgentStatus(appName, req, action, version = null) {
  const ip = getClientIP(req);
  const userAgent = req.headers['user-agent'] || 'unknown';
  // 优先使用 agent 主动提供的 ID，否则使用 IP 作为 fallback
  const agentId = req.headers['x-agent-id'] || ip;
  const localVersion = req.headers['x-local-version'] || '';
  const now = new Date().toISOString();
  
  if (!agentStatus.has(appName)) {
    agentStatus.set(appName, new Map());
  }
  
  const appAgents = agentStatus.get(appName);
  if (!appAgents.has(agentId)) {
    appAgents.set(agentId, {
      id: agentId,
      localVersion: localVersion,
      ip: ip,
      lastSeen: now,
      requestCount: 0,
      currentVersion: null,
      lastAction: action,
      userAgent: userAgent
    });
  }
  
  const agent = appAgents.get(agentId);
  agent.lastSeen = now;
  agent.requestCount++;
  agent.lastAction = action;
  if (version) {
    agent.currentVersion = version;
  }
  // 更新 localVersion（如果 agent 提供了）
  if (localVersion) {
    agent.localVersion = localVersion;
  }
  // 如果 agent 提供了 ID，更新 IP（可能 IP 会变化）
  if (req.headers['x-agent-id']) {
    agent.ip = ip;
  }
}

// 清理长时间未活跃的 agent（超过 1 小时）
function cleanupInactiveAgents() {
  const oneHourAgo = Date.now() - 60 * 60 * 1000;
  
  for (const [appName, appAgents] of agentStatus.entries()) {
    for (const [agentId, agent] of appAgents.entries()) {
      const lastSeenTime = new Date(agent.lastSeen).getTime();
      if (lastSeenTime < oneHourAgo) {
        appAgents.delete(agentId);
      }
    }
    // 如果应用没有活跃的 agent，删除应用记录
    if (appAgents.size === 0) {
      agentStatus.delete(appName);
    }
  }
}

// 定期清理（每 10 分钟）
setInterval(cleanupInactiveAgents, 10 * 60 * 1000);

// 日志函数
function log(level, message, ...args) {
  const timestamp = new Date().toISOString();
  // 格式化消息，处理参数替换
  let formattedMessage = message;
  if (args.length > 0) {
    // 简单的参数替换（支持 %s, %d, %j 等）
    let argIndex = 0;
    formattedMessage = message.replace(/%[sdj%]/g, (match) => {
      if (match === '%%') return '%';
      if (argIndex >= args.length) return match;
      const arg = args[argIndex++];
      if (match === '%j') return JSON.stringify(arg);
      if (match === '%d') return Number(arg);
      return String(arg);
    });
    // 如果还有剩余参数，追加到末尾
    if (argIndex < args.length) {
      formattedMessage += ' ' + args.slice(argIndex).map(a => 
        typeof a === 'object' ? JSON.stringify(a) : String(a)
      ).join(' ');
    }
  }
  const logMessage = `[${timestamp}] [${level}] ${formattedMessage}\n`;
  
  // 输出到控制台
  console.log(`[${timestamp}] [${level}]`, message, ...args);
  
  // 写入文件（异步，不阻塞）
  fs.appendFile(LOG_FILE, logMessage, (err) => {
    if (err) {
      // 如果写入失败，只输出到控制台，避免无限循环
      console.error('Failed to write log to file:', err.message);
    }
  });
}

function info(message, ...args) {
  log('INFO', message, ...args);
}

function error(message, ...args) {
  log('ERROR', message, ...args);
}

function warn(message, ...args) {
  log('WARN', message, ...args);
}

// 计算文件的 SHA256
function calculateSHA256(filePath) {
  try {
    const fileBuffer = fs.readFileSync(filePath);
    const hashSum = crypto.createHash('sha256');
    hashSum.update(fileBuffer);
    return hashSum.digest('hex');
  } catch (err) {
    error('Failed to calculate SHA256 for %s: %s', filePath, err.message);
    throw err;
  }
}

// 获取应用的目录
function getAppDir(appName) {
  return path.join(APPS_DIR, appName);
}

// 获取应用的配置文件路径
function getAppConfigFile(appName) {
  return path.join(getAppDir(appName), 'version.yaml');
}

// 获取应用的文件目录
function getAppBinaryDir(appName) {
  return path.join(getAppDir(appName), 'files');
}

// 读取应用配置
function readAppConfig(appName) {
  try {
    const configFile = getAppConfigFile(appName);
    if (!fs.existsSync(configFile)) {
      return null;
    }
    const content = fs.readFileSync(configFile, 'utf8');
    // 简单的 YAML 解析（仅用于读取）
    const config = {};
    content.split('\n').forEach(line => {
      const match = line.match(/^(\w+):\s*["']?([^"']+)["']?$/);
      if (match) {
        config[match[1]] = match[2];
      }
    });
    return config;
  } catch (err) {
    error('Failed to read config for app %s: %s', appName, err.message);
    return null;
  }
}

// 获取应用的最新二进制文件
function getLatestAppBinary(appName) {
  try {
    const appBinaryDir = getAppBinaryDir(appName);
    if (!fs.existsSync(appBinaryDir)) {
      return null;
    }
    const files = fs.readdirSync(appBinaryDir)
      .filter(f => !f.startsWith('.'))
      .map(f => ({
        name: f,
        path: path.join(appBinaryDir, f),
        stat: fs.statSync(path.join(appBinaryDir, f))
      }))
      .filter(f => f.stat.isFile())
      .sort((a, b) => b.stat.mtime.getTime() - a.stat.mtime.getTime());
    
    return files.length > 0 ? files[0].path : null;
  } catch (err) {
    error('Failed to list binaries for app %s: %s', appName, err.message);
    return null;
  }
}


// 生成配置文件内容 - 多文件格式
function generateConfig(files, version, appName, options = {}) {
  const baseUrl = options.baseUrl || BASE_URL;
  const restartCmd = options.restartCmd !== undefined ? options.restartCmd : RESTART_CMD;
  
  // files 必须是文件数组
  if (!Array.isArray(files)) {
    throw new Error('Files must be an array');
  }
  
  if (files.length === 0) {
    throw new Error('Files array cannot be empty');
  }
  
  if (!appName) {
    throw new Error('App name is required');
  }
  
  const fileList = [];
  for (const file of files) {
    if (!file.path || !fs.existsSync(file.path)) {
      throw new Error('Binary file not found: ' + (file.path || 'unknown'));
    }
    const sha256 = calculateSHA256(file.path);
    const fileName = path.basename(file.path);
    fileList.push({
      name: file.name || fileName,
      url: `${baseUrl}/ota/${appName}/files/${fileName}`,
      sha256: sha256,
      target: file.target,
      version: file.version || version,
      restart: file.restart || false
    });
  }
  
  const config = {
    version: version,
    files: fileList,
    restart_cmd: restartCmd || undefined
  };
  
  // 生成 YAML
  let yaml = `version: "${config.version}"
files:
`;
  
  for (const file of fileList) {
    yaml += `  - name: "${file.name}"
    url: "${file.url}"
    sha256: "${file.sha256}"
    target: "${file.target}"
`;
    if (file.version && file.version !== version) {
      yaml += `    version: "${file.version}"
`;
    }
    if (file.restart) {
      yaml += `    restart: true
`;
    }
  }
  
  if (config.restart_cmd) {
    yaml += `restart_cmd: '${config.restart_cmd}'
`;
  }
  
  return { yaml, config };
}

// 创建服务器
function createServer() {
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, BASE_URL);
    
    info('%s %s', req.method, req.url);
    
    // CORS 支持
    res.setHeader('Access-Control-Allow-Origin', '*');
    res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
    res.setHeader('Access-Control-Allow-Headers', 'Content-Type');
    
    if (req.method === 'OPTIONS') {
      res.writeHead(200);
      res.end();
      return;
    }
    
    // 健康检查端点
    if (url.pathname === '/health' || url.pathname === '/ping') {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ status: 'ok', timestamp: new Date().toISOString() }));
      return;
    }
    
    // 游戏记录端点: /game/record
    if (url.pathname === '/game/record') {
      // 处理 POST 请求（JSON body）
      if (req.method === 'POST') {
        let body = '';
        req.on('data', chunk => {
          body += chunk.toString();
        });
        req.on('end', () => {
          try {
            const params = JSON.parse(body);
            const timestamp = params.timestamp;
            const type = params.type;
            const duration = params.duration;
            
            // 打印参数日志
            info('RECORDED - timestamp: %s, type: %s, duration: %s', timestamp, type, duration);
            
            res.writeHead(200, { 'Content-Type': 'application/json' });
            res.end(JSON.stringify({ 
              status: 'ok', 
              message: 'Record received',
              timestamp: new Date().toISOString()
            }));
          } catch (err) {
            error('Error parsing JSON body: %s', err.message);
            res.writeHead(400, { 'Content-Type': 'application/json' });
            res.end(JSON.stringify({ error: 'Invalid JSON' }));
          }
        });
        return;
      }
      
      // 处理 GET 请求（查询参数）
      if (req.method === 'GET') {
        const timestamp = url.searchParams.get('timestamp');
        const type = url.searchParams.get('type');
        const duration = url.searchParams.get('duration');
        
        // 打印参数日志
        info('RECORDED - timestamp: %s, type: %s, duration: %s', timestamp, type, duration);
        
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ 
          status: 'ok', 
          message: 'Record received',
          timestamp: new Date().toISOString()
        }));
        return;
      }
      
      // 不支持的请求方法
      res.writeHead(405, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: 'Method not allowed' }));
      return;
    }
    
    // 多应用配置文件端点: /ota/<app_name>/version.yaml
    const otaMatch = url.pathname.match(/^\/ota\/([^\/]+)\/version\.yaml$/);
    if (otaMatch) {
      const appName = otaMatch[1];
      try {
        const configFile = getAppConfigFile(appName);
        if (!fs.existsSync(configFile)) {
          res.writeHead(404, { 'Content-Type': 'text/plain' });
          res.end(`Config file not found for app: ${appName}`);
          return;
        }
        const content = fs.readFileSync(configFile, 'utf8');
        
        // 尝试从配置文件中提取版本号
        let version = null;
        const versionMatch = content.match(/^version:\s*["']?([^"'\n]+)["']?/m);
        if (versionMatch) {
          version = versionMatch[1];
        }
        
        // 记录 agent 状态
        recordAgentStatus(appName, req, 'config_check', version);
        
        res.writeHead(200, { 
          'Content-Type': 'application/x-yaml',
          'Cache-Control': 'no-cache'
        });
        res.end(content);
      } catch (err) {
        error('Error serving config for app %s: %s', appName, err.message);
        res.writeHead(500, { 'Content-Type': 'text/plain' });
        res.end('Internal Server Error');
      }
      return;
    }
    
    // 多应用文件下载端点: /ota/<app_name>/files/<filename>
    const binaryMatch = url.pathname.match(/^\/ota\/([^\/]+)\/files\/(.+)$/);
    if (binaryMatch) {
      const appName = binaryMatch[1];
      const fileName = binaryMatch[2];
      try {
        const appBinaryDir = getAppBinaryDir(appName);
        const binaryPath = path.join(appBinaryDir, fileName);
        
        if (!fs.existsSync(binaryPath)) {
          res.writeHead(404, { 'Content-Type': 'text/plain' });
          res.end(`Binary file not found: ${appName}/${fileName}`);
          return;
        }
        
        // 记录 agent 状态（文件下载）
        recordAgentStatus(appName, req, 'file_download');
        
        const stat = fs.statSync(binaryPath);
        const fileSize = stat.size;
        const actualFileName = path.basename(binaryPath);
        
        res.writeHead(200, {
          'Content-Type': 'application/octet-stream',
          'Content-Length': fileSize,
          'Content-Disposition': `attachment; filename="${actualFileName}"`,
          'Cache-Control': 'no-cache'
        });
        
        const fileStream = fs.createReadStream(binaryPath);
        fileStream.on('error', (err) => {
          error('Error streaming binary for app %s: %s', appName, err.message);
          if (!res.headersSent) {
            res.writeHead(500, { 'Content-Type': 'text/plain' });
            res.end('Internal Server Error');
          }
        });
        fileStream.pipe(res);
      } catch (err) {
        error('Error serving binary for app %s: %s', appName, err.message);
        res.writeHead(500, { 'Content-Type': 'text/plain' });
        res.end('Internal Server Error');
      }
      return;
    }
    
    
    // Agent 状态端点: /ota/<app_name>/agents
    const agentsMatch = url.pathname.match(/^\/ota\/([^\/]+)\/agents$/);
    if (agentsMatch) {
      const appName = agentsMatch[1];
      try {
        const appAgents = agentStatus.get(appName);
        const agents = appAgents ? Array.from(appAgents.values()) : [];
        
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          app: appName,
          agents: agents,
          total: agents.length,
          timestamp: new Date().toISOString()
        }, null, 2));
      } catch (err) {
        error('Error serving agents for app %s: %s', appName, err.message);
        res.writeHead(500, { 'Content-Type': 'text/plain' });
        res.end('Internal Server Error');
      }
      return;
    }
    
    // 应用信息端点: /ota/<app_name>/info
    const appInfoMatch = url.pathname.match(/^\/ota\/([^\/]+)\/info$/);
    if (appInfoMatch) {
      const appName = appInfoMatch[1];
      try {
        const config = readAppConfig(appName);
        const binaryPath = getLatestAppBinary(appName);
        const binaryInfo = binaryPath ? {
          path: binaryPath,
          name: path.basename(binaryPath),
          size: fs.statSync(binaryPath).size,
          mtime: fs.statSync(binaryPath).mtime
        } : null;
        
        // 获取 agent 统计信息
        const appAgents = agentStatus.get(appName);
        const agentCount = appAgents ? appAgents.size : 0;
        
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          app: appName,
          service: 'OTA Update Server',
          version: '1.0.0',
          config: config,
          binary: binaryInfo,
          agents: {
            count: agentCount,
            endpoint: `/ota/${appName}/agents`
          },
          endpoints: {
            config: `/ota/${appName}/version.yaml`,
            files: `/ota/${appName}/files/<filename>`,
            info: `/ota/${appName}/info`,
            agents: `/ota/${appName}/agents`
          }
        }, null, 2));
      } catch (err) {
        error('Error serving info for app %s: %s', appName, err.message);
        res.writeHead(500, { 'Content-Type': 'text/plain' });
        res.end('Internal Server Error');
      }
      return;
    }
    
    // 信息端点
    if (url.pathname === '/info' || url.pathname === '/') {
      try {
        // 列出所有应用
        const apps = [];
        if (fs.existsSync(APPS_DIR)) {
          const dirs = fs.readdirSync(APPS_DIR);
          for (const dir of dirs) {
            const appDir = path.join(APPS_DIR, dir);
            if (fs.statSync(appDir).isDirectory()) {
              const configFile = getAppConfigFile(dir);
              if (fs.existsSync(configFile)) {
                apps.push({
                  name: dir,
                  config: `/ota/${dir}/version.yaml`,
                  info: `/ota/${dir}/info`
                });
              }
            }
          }
        }
        
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({
          service: 'OTA Update Server',
          version: '1.0.0',
          apps: apps,
          endpoints: {
            config: '/ota/<app_name>/version.yaml',
            files: '/ota/<app_name>/files/<filename>',
            info: '/ota/<app_name>/info',
            health: '/health'
          }
        }, null, 2));
      } catch (err) {
        error('Error serving info: %s', err.message);
        res.writeHead(500, { 'Content-Type': 'text/plain' });
        res.end('Internal Server Error');
      }
      return;
    }
    
    // 404
    res.writeHead(404, { 'Content-Type': 'text/plain' });
    res.end('Not Found');
  });
  
  return server;
}

// 启动服务器
function start() {
  const server = createServer();
  
  server.on('error', (err) => {
    error('Server error: %s', err.message);
    process.exit(1);
  });
  
  server.listen(PORT, HOST, () => {
    info('🚀 OTA Update Server started');
    info('📍 Listening on %s:%d', HOST, PORT);
    info('🌐 Base URL: %s', BASE_URL);
    info('📁 Apps directory: %s', APPS_DIR);
    info('📝 Log file: %s', LOG_FILE);
    info('');
    info('Endpoints:');
    info('  GET /ota/<app_name>/version.yaml  - Application configuration');
    info('  GET /ota/<app_name>/files/<file>  - Application file download');
    info('  GET /ota/<app_name>/info           - Application information');
    info('  GET /ota/<app_name>/agents         - Agent status for application');
    info('  GET /health                        - Health check');
    info('  GET /info                          - Server information (list all apps)');
    info('  GET/POST /game/record              - Game record endpoint');
    info('');
  });
  
  // 优雅关闭
  process.on('SIGTERM', () => {
    info('SIGTERM received, shutting down gracefully...');
    server.close(() => {
      info('Server closed');
      process.exit(0);
    });
  });
  
  process.on('SIGINT', () => {
    info('SIGINT received, shutting down gracefully...');
    server.close(() => {
      info('Server closed');
      process.exit(0);
    });
  });
}

// 如果直接运行此文件，启动服务器
if (require.main === module) {
  start();
}

module.exports = { start, generateConfig, calculateSHA256 };


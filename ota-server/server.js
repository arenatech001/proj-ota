const http = require('http');
const https = require('https');
const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const sqlite3 = require('sqlite3').verbose();

// 从环境变量读取配置，提供默认值
const PORT = process.env.PORT || 3001;
const HOST = process.env.HOST || '0.0.0.0';
const BASE_URL = process.env.BASE_URL || `http://localhost:${PORT}`;
const APPS_DIR = process.env.APPS_DIR || path.join(__dirname, 'apps');
const RESTART_CMD = process.env.RESTART_CMD || '';
const LOG_DIR = process.env.LOG_DIR || path.join(__dirname, 'logs');
const LOG_FILE = process.env.LOG_FILE || path.join(LOG_DIR, 'server.log');
const DB_PATH = process.env.DB_PATH || path.join(__dirname, 'game_records.db');
const RECORDS_PASSWORD = process.env.RECORDS_PASSWORD || 'admin123456';

// 确保应用目录存在
if (!fs.existsSync(APPS_DIR)) {
  fs.mkdirSync(APPS_DIR, { recursive: true });
}

// 确保日志目录存在
if (!fs.existsSync(LOG_DIR)) {
  fs.mkdirSync(LOG_DIR, { recursive: true });
}

// 初始化 SQLite 数据库
let db = null;
function initDatabase() {
  return new Promise((resolve, reject) => {
    db = new sqlite3.Database(DB_PATH, (err) => {
      if (err) {
        error('Failed to open database: %s', err.message);
        reject(err);
        return;
      }
      info('Database connected: %s', DB_PATH);
      
      // 创建游戏记录表
      db.run(`CREATE TABLE IF NOT EXISTS game_records (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        timestamp TEXT NOT NULL,
        type TEXT,
        duration INTEGER,
        location TEXT,
        created_at TEXT DEFAULT CURRENT_TIMESTAMP
      )`, (err) => {
        if (err) {
          error('Failed to create table: %s', err.message);
          reject(err);
          return;
        }
        // 已有库升级：补充 location 列（新库已在 CREATE 中包含）
        db.run('ALTER TABLE game_records ADD COLUMN location TEXT', (alterErr) => {
          if (alterErr && !String(alterErr.message).includes('duplicate column')) {
            error('Failed to add location column: %s', alterErr.message);
            reject(alterErr);
            return;
          }
          info('Database table initialized');
          resolve();
        });
      });
    });
  });
}

// 插入游戏记录
function insertGameRecord(timestamp, type, duration, location) {
  return new Promise((resolve, reject) => {
    const loc = location === undefined || location === null || location === '' ? null : String(location);
    const stmt = db.prepare('INSERT INTO game_records (timestamp, type, duration, location) VALUES (?, ?, ?, ?)');
    stmt.run([timestamp, type, duration, loc], function(err) {
      if (err) {
        error('Failed to insert record: %s', err.message);
        reject(err);
        return;
      }
      resolve(this.lastID);
    });
    stmt.finalize();
  });
}

// 查询游戏记录（日期区间：dateStart、dateEnd 均为 YYYY-MM-DD，闭区间）
function queryGameRecords(dateStart, dateEnd, type, duration, location) {
  return new Promise((resolve, reject) => {
    let query = 'SELECT * FROM game_records WHERE 1=1';
    const params = [];
    
    if (dateStart || dateEnd) {
      const start = dateStart || dateEnd;
      const end = dateEnd || dateStart;
      const startObj = new Date(start + 'T00:00:00');
      const endObj = new Date(end + 'T00:00:00');
      const startTimestamp = Math.floor(startObj.getTime() / 1000);
      const endTimestamp = Math.floor(endObj.getTime() / 1000) + 86400; // 结束日期的次日 00:00
      
      info('Query date range: %s ~ %s, startTimestamp=%d, endTimestamp=%d', start, end, startTimestamp, endTimestamp);
      
      query += ' AND CAST(timestamp AS INTEGER) >= ? AND CAST(timestamp AS INTEGER) < ?';
      params.push(startTimestamp, endTimestamp);
    }
    
    if (type) {
      query += ' AND type = ?';
      params.push(type);
    }
    
    if (duration !== null && duration !== undefined && duration !== '') {
      query += ' AND duration = ?';
      params.push(parseInt(duration));
    }
    
    if (location) {
      query += ' AND location = ?';
      params.push(location);
    }
    
    query += ' ORDER BY CAST(timestamp AS INTEGER) DESC';
    
    info('Query SQL: %s, params: %j', query, params);
    
    db.all(query, params, (err, rows) => {
      if (err) {
        error('Failed to query records: %s', err.message);
        reject(err);
        return;
      }
      info('Query returned %d records', rows.length);
      resolve(rows);
    });
  });
}

// 获取所有唯一的 type 值
function getDistinctTypes() {
  return new Promise((resolve, reject) => {
    db.all('SELECT DISTINCT type FROM game_records WHERE type IS NOT NULL ORDER BY type', (err, rows) => {
      if (err) {
        error('Failed to get distinct types: %s', err.message);
        reject(err);
        return;
      }
      resolve(rows.map(row => row.type));
    });
  });
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
            const location = params.location;
            
            // 打印参数日志
            info('RECORDED - timestamp: %s, type: %s, duration: %s, location: %s', timestamp, type, duration, location);
            
            // 存储到数据库
            if (!db) {
              error('Database not initialized');
              res.writeHead(500, { 'Content-Type': 'application/json' });
              res.end(JSON.stringify({ error: 'Database not initialized' }));
              return;
            }
            
            insertGameRecord(timestamp, type, duration, location)
              .then((recordId) => {
                info('Record saved to database with ID: %d', recordId);
                res.writeHead(200, { 'Content-Type': 'application/json' });
                res.end(JSON.stringify({ 
                  status: 'ok', 
                  message: 'Record saved',
                  id: recordId,
                  timestamp: new Date().toISOString()
                }));
              })
              .catch((err) => {
                error('Failed to save record: %s', err.message);
                res.writeHead(500, { 'Content-Type': 'application/json' });
                res.end(JSON.stringify({ error: 'Failed to save record', message: err.message }));
              });
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
        const location = url.searchParams.get('location');
        
        // 打印参数日志
        info('RECORDED - timestamp: %s, type: %s, duration: %s, location: %s', timestamp, type, duration, location);
        
        // 存储到数据库
        if (!db) {
          error('Database not initialized');
          res.writeHead(500, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ error: 'Database not initialized' }));
          return;
        }
        
        insertGameRecord(timestamp, type, duration, location)
          .then((recordId) => {
            info('Record saved to database with ID: %d', recordId);
            res.writeHead(200, { 'Content-Type': 'application/json' });
            res.end(JSON.stringify({ 
              status: 'ok', 
              message: 'Record saved',
              id: recordId,
              timestamp: new Date().toISOString()
            }));
          })
          .catch((err) => {
            error('Failed to save record: %s', err.message);
            res.writeHead(500, { 'Content-Type': 'application/json' });
            res.end(JSON.stringify({ error: 'Failed to save record', message: err.message }));
          });
        return;
      }
      
      // 不支持的请求方法
      res.writeHead(405, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: 'Method not allowed' }));
      return;
    }
    
    // 游戏记录查询页面: /game/records.html
    if (url.pathname === '/game/records.html') {
      if (req.method === 'GET') {
        // 返回查询页面 HTML
        const html = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>游戏记录查询</title>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            padding: 20px;
        }
        .container {
            max-width: 1200px;
            margin: 0 auto;
            background: white;
            border-radius: 12px;
            box-shadow: 0 10px 40px rgba(0,0,0,0.2);
            overflow: hidden;
        }
        .header {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            padding: 30px;
            text-align: center;
        }
        .header h1 {
            font-size: 2em;
            margin-bottom: 10px;
        }
        .filters {
            padding: 30px;
            background: #f8f9fa;
            border-bottom: 1px solid #e0e0e0;
        }
        .filter-group {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
            gap: 20px;
            margin-bottom: 20px;
        }
        .filter-item {
            display: flex;
            flex-direction: column;
        }
        .filter-item label {
            font-weight: 600;
            margin-bottom: 8px;
            color: #333;
        }
        .filter-item input,
        .filter-item select {
            padding: 12px;
            border: 2px solid #e0e0e0;
            border-radius: 6px;
            font-size: 14px;
            transition: border-color 0.3s;
        }
        .filter-item input:focus,
        .filter-item select:focus {
            outline: none;
            border-color: #667eea;
        }
        .btn-group {
            display: flex;
            gap: 10px;
            margin-top: 10px;
        }
        button {
            padding: 12px 24px;
            border: none;
            border-radius: 6px;
            font-size: 16px;
            font-weight: 600;
            cursor: pointer;
            transition: all 0.3s;
        }
        .btn-primary {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
        }
        .btn-primary:hover {
            transform: translateY(-2px);
            box-shadow: 0 5px 15px rgba(102, 126, 234, 0.4);
        }
        .btn-secondary {
            background: #6c757d;
            color: white;
        }
        .btn-secondary:hover {
            background: #5a6268;
        }
        .results {
            padding: 30px;
        }
        .stats {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 20px;
            margin-bottom: 30px;
        }
        .stat-card {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            padding: 20px;
            border-radius: 8px;
            text-align: center;
        }
        .stat-card .value {
            font-size: 2em;
            font-weight: bold;
            margin-bottom: 5px;
        }
        .stat-card .label {
            font-size: 0.9em;
            opacity: 0.9;
        }
        .table-container {
            overflow-x: auto;
        }
        table {
            width: 100%;
            border-collapse: collapse;
            margin-top: 20px;
        }
        thead {
            background: #f8f9fa;
        }
        th, td {
            padding: 12px;
            text-align: left;
            border-bottom: 1px solid #e0e0e0;
        }
        th {
            font-weight: 600;
            color: #333;
        }
        tbody tr:hover {
            background: #f8f9fa;
        }
        .loading {
            text-align: center;
            padding: 40px;
            color: #666;
        }
        .error {
            background: #f8d7da;
            color: #721c24;
            padding: 15px;
            border-radius: 6px;
            margin: 20px 0;
        }
        .empty {
            text-align: center;
            padding: 40px;
            color: #999;
        }
        .login-container {
            max-width: 400px;
            margin: 100px auto;
            background: white;
            border-radius: 12px;
            box-shadow: 0 10px 40px rgba(0,0,0,0.2);
            padding: 40px;
        }
        .login-header {
            text-align: center;
            margin-bottom: 30px;
        }
        .login-header h1 {
            font-size: 2em;
            margin-bottom: 10px;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            background-clip: text;
        }
        .login-form {
            display: flex;
            flex-direction: column;
            gap: 20px;
        }
        .login-form input {
            padding: 12px;
            border: 2px solid #e0e0e0;
            border-radius: 6px;
            font-size: 16px;
            transition: border-color 0.3s;
        }
        .login-form input:focus {
            outline: none;
            border-color: #667eea;
        }
        .login-error {
            background: #f8d7da;
            color: #721c24;
            padding: 12px;
            border-radius: 6px;
            font-size: 14px;
            display: none;
        }
        .main-content {
            display: none;
        }
        .main-content.active {
            display: block;
        }
    </style>
</head>
<body>
    <!-- 登录界面 -->
    <div id="loginContainer" class="login-container">
        <div class="login-header">
            <h1>🔐 身份验证</h1>
            <p>请输入密码以访问游戏记录查询</p>
        </div>
        <div class="login-form">
            <input type="password" id="passwordInput" placeholder="请输入密码" autocomplete="off">
            <div id="loginError" class="login-error"></div>
            <button class="btn-primary" onclick="handleLogin()">登录</button>
        </div>
    </div>
    
    <!-- 主内容界面 -->
    <div id="mainContent" class="main-content">
    <div class="container">
        <div class="header">
            <h1>🎮 游戏记录查询</h1>
            <p>查询和筛选游戏记录数据</p>
        </div>
        
        <div class="filters">
            <div class="filter-group">
                <div class="filter-item">
                    <label for="dateStart">开始日期</label>
                    <input type="date" id="dateStart" name="dateStart">
                </div>
                <div class="filter-item">
                    <label for="dateEnd">结束日期</label>
                    <input type="date" id="dateEnd" name="dateEnd">
                </div>
                <div class="filter-item">
                    <label for="type">类型</label>
                    <select id="type" name="type">
                        <option value="">全部</option>
                    </select>
                </div>
                <div class="filter-item">
                    <label for="duration">时长（秒）</label>
                    <input type="number" id="duration" name="duration" placeholder="留空表示全部">
                </div>
                <div class="filter-item">
                    <label for="location">地点</label>
                    <select id="location" name="location">
                        <option value="">全部</option>
                    </select>
                </div>
            </div>
            <div class="btn-group">
                <button class="btn-primary" onclick="queryRecords()">查询</button>
                <button class="btn-secondary" onclick="resetFilters()">重置</button>
            </div>
        </div>
        
        <div class="results">
            <div id="stats" class="stats" style="display: none;"></div>
            <div id="loading" class="loading" style="display: none;">加载中...</div>
            <div id="error" class="error" style="display: none;"></div>
            <div id="table-container" class="table-container"></div>
        </div>
    </div>
    
    <script>
        // 从全量记录填充类型、地点下拉选项
        async function loadFilterOptions() {
            try {
                const response = await fetch('/game/records');
                const data = await response.json();
                if (data.records) {
                    const types = [...new Set(data.records.map(r => r.type).filter(t => t))].sort();
                    const typeSelect = document.getElementById('type');
                    while (typeSelect.options.length > 1) typeSelect.remove(1);
                    types.forEach(type => {
                        const option = document.createElement('option');
                        option.value = type;
                        option.textContent = type;
                        typeSelect.appendChild(option);
                    });
                    const locs = [...new Set(data.records.map(r => r.location).filter(l => l))].sort();
                    const locSelect = document.getElementById('location');
                    while (locSelect.options.length > 1) locSelect.remove(1);
                    locs.forEach(loc => {
                        const option = document.createElement('option');
                        option.value = loc;
                        option.textContent = loc;
                        locSelect.appendChild(option);
                    });
                }
            } catch (err) {
                console.error('Failed to load filter options:', err);
            }
        }
        
        // 查询记录
        async function queryRecords() {
            const dateStart = document.getElementById('dateStart').value;
            const dateEnd = document.getElementById('dateEnd').value;
            const type = document.getElementById('type').value;
            const duration = document.getElementById('duration').value;
            const location = document.getElementById('location').value;
            
            const params = new URLSearchParams();
            if (dateStart) params.append('dateStart', dateStart);
            if (dateEnd) params.append('dateEnd', dateEnd);
            if (type) params.append('type', type);
            if (duration) params.append('duration', duration);
            if (location) params.append('location', location);
            
            const loading = document.getElementById('loading');
            const error = document.getElementById('error');
            const stats = document.getElementById('stats');
            const tableContainer = document.getElementById('table-container');
            
            loading.style.display = 'block';
            error.style.display = 'none';
            stats.style.display = 'none';
            tableContainer.innerHTML = '';
            
            try {
                const response = await fetch('/game/records?' + params.toString());
                const data = await response.json();
                
                loading.style.display = 'none';
                
                if (data.error) {
                    error.textContent = '错误: ' + data.error;
                    error.style.display = 'block';
                    return;
                }
                
                if (data.records && data.records.length > 0) {
                    // 显示统计信息
                    const totalDuration = Math.round(data.records.reduce((sum, r) => sum + (r.duration || 0), 0) / 1000); // 转换为秒 整数
                    const avgDuration = Math.round(totalDuration / data.records.length);
                    const typeCount = new Set(data.records.map(r => r.type).filter(t => t)).size;
                    
                    stats.innerHTML = \`
                        <div class="stat-card">
                            <div class="value">\${data.count}</div>
                            <div class="label">总记录数</div>
                        </div>
                        <div class="stat-card">
                            <div class="value">\${typeCount}</div>
                            <div class="label">类型数</div>
                        </div>
                        <div class="stat-card">
                            <div class="value">\${avgDuration}</div>
                            <div class="label">平均时长（秒）</div>
                        </div>
                        <div class="stat-card">
                            <div class="value">\${totalDuration}</div>
                            <div class="label">总时长（秒）</div>
                        </div>
                    \`;
                    stats.style.display = 'grid';
                    
                    // 显示表格（created_at 为 SQLite UTC 时间，转为本地时间显示）
                    function formatCreatedAt(createdAt) {
                        if (!createdAt) return '-';
                        // SQLite CURRENT_TIMESTAMP 存的是 UTC，格式 "YYYY-MM-DD HH:MM:SS"，无 Z 后缀
                        var s = String(createdAt).trim().replace(' ', 'T');
                        if (!/Z|[+-]\\d{2}:?\\d{2}$/.test(s)) s += 'Z';
                        try {
                            return new Date(s).toLocaleString('zh-CN', { hour12: false });
                        } catch (e) {
                            return createdAt;
                        }
                    }
                    let tableHTML = '<table><thead><tr><th>ID</th><th>时间</th><th>类型</th><th>地点</th><th>时长（秒）</th></tr></thead><tbody>';
                    data.records.forEach(record => {
                        tableHTML += \`<tr>
                            <td>\${record.id}</td>
                            <td>\${formatCreatedAt(record.created_at)}</td>
                            <td>\${record.type || '-'}</td>
                            <td>\${record.location || '-'}</td>
                            <td>\${record.duration != null ? Math.round(record.duration / 1000) : '-'}</td>
                        </tr>\`;
                    });
                    tableHTML += '</tbody></table>';
                    tableContainer.innerHTML = tableHTML;
                } else {
                    tableContainer.innerHTML = '<div class="empty">没有找到匹配的记录</div>';
                }
            } catch (err) {
                loading.style.display = 'none';
                error.textContent = '查询失败: ' + err.message;
                error.style.display = 'block';
            }
        }
        
        // 重置过滤器
        function resetFilters() {
            document.getElementById('dateStart').value = '';
            document.getElementById('dateEnd').value = '';
            document.getElementById('type').value = '';
            document.getElementById('duration').value = '';
            document.getElementById('location').value = '';
            document.getElementById('stats').style.display = 'none';
            document.getElementById('table-container').innerHTML = '';
            document.getElementById('error').style.display = 'none';
        }
        
        function checkAuth() {
            return sessionStorage.getItem('records_authenticated') === 'true';
        }
        async function handleLogin() {
            var pwd = document.getElementById('passwordInput').value;
            var errEl = document.getElementById('loginError');
            if (!pwd) { errEl.textContent = '请输入密码'; errEl.style.display = 'block'; return; }
            try {
                var r = await fetch('/game/auth', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password: pwd }) });
                var data = await r.json();
                if (data.status === 'ok') {
                    sessionStorage.setItem('records_authenticated', 'true');
                    document.getElementById('loginContainer').style.display = 'none';
                    document.getElementById('mainContent').classList.add('active');
                    initPage();
                } else {
                    errEl.textContent = data.error || '密码错误';
                    errEl.style.display = 'block';
                }
            } catch (e) {
                errEl.textContent = '登录失败: ' + e.message;
                errEl.style.display = 'block';
            }
        }
        function initPage() {
            loadFilterOptions();
            var today = new Date().toISOString().split('T')[0];
            document.getElementById('dateStart').value = today;
            document.getElementById('dateEnd').value = today;
            queryRecords();
        }
        
        // 页面加载时初始化
        window.onload = function() {
            if (checkAuth()) {
                document.getElementById('loginContainer').style.display = 'none';
                document.getElementById('mainContent').classList.add('active');
                initPage();
            } else {
                document.getElementById('passwordInput').addEventListener('keypress', function(e) { if (e.key === 'Enter') handleLogin(); });
            }
        };
    </script>
</body>
</html>`;
        
        res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
        res.end(html);
        return;
      }
    }
    
    // 密码验证接口: /game/auth
    if (url.pathname === '/game/auth') {
      if (req.method === 'POST') {
        let body = '';
        req.on('data', chunk => {
          body += chunk.toString();
        });
        req.on('end', () => {
          try {
            const params = JSON.parse(body);
            const password = params.password;
            
            if (password && password === RECORDS_PASSWORD) {
              res.writeHead(200, { 'Content-Type': 'application/json' });
              res.end(JSON.stringify({ 
                status: 'ok', 
                message: 'Authentication successful'
              }));
            } else {
              res.writeHead(401, { 'Content-Type': 'application/json' });
              res.end(JSON.stringify({ 
                status: 'error', 
                error: 'Invalid password'
              }));
            }
          } catch (err) {
            error('Error parsing auth request: %s', err.message);
            res.writeHead(400, { 'Content-Type': 'application/json' });
            res.end(JSON.stringify({ error: 'Invalid JSON' }));
          }
        });
        return;
      }
      
      res.writeHead(405, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: 'Method not allowed' }));
      return;
    }
    
    // 游戏记录查询 API 端点: /game/records
    if (url.pathname === '/game/records') {
      if (req.method === 'GET') {
        const dateStart = url.searchParams.get('dateStart');
        const dateEnd = url.searchParams.get('dateEnd');
        const type = url.searchParams.get('type');
        const duration = url.searchParams.get('duration');
        const location = url.searchParams.get('location');
        
        if (!db) {
          error('Database not initialized');
          res.writeHead(500, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ error: 'Database not initialized' }));
          return;
        }
        
        queryGameRecords(dateStart, dateEnd, type, duration, location)
          .then((records) => {
            res.writeHead(200, { 'Content-Type': 'application/json' });
            res.end(JSON.stringify({
              status: 'ok',
              count: records.length,
              records: records
            }, null, 2));
          })
          .catch((err) => {
            error('Failed to query records: %s', err.message);
            res.writeHead(500, { 'Content-Type': 'application/json' });
            res.end(JSON.stringify({ error: 'Failed to query records', message: err.message }));
          });
        return;
      }
      
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
  // 初始化数据库
  initDatabase()
    .then(() => {
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
        info('💾 Database: %s', DB_PATH);
        info('');
        info('Endpoints:');
        info('  GET /ota/<app_name>/version.yaml  - Application configuration');
        info('  GET /ota/<app_name>/files/<file>  - Application file download');
        info('  GET /ota/<app_name>/info           - Application information');
        info('  GET /ota/<app_name>/agents         - Agent status for application');
        info('  GET /health                        - Health check');
        info('  GET /info                          - Server information (list all apps)');
        info('  GET/POST /game/record              - Game record endpoint');
        info('  GET /game/records                  - Query game records API');
        info('  GET /game/records.html             - Game records query page');
        info('');
      });
      
      // 优雅关闭
      process.on('SIGTERM', () => {
        info('SIGTERM received, shutting down gracefully...');
        server.close(() => {
          if (db) {
            db.close((err) => {
              if (err) {
                error('Error closing database: %s', err.message);
              } else {
                info('Database closed');
              }
              info('Server closed');
              process.exit(0);
            });
          } else {
            info('Server closed');
            process.exit(0);
          }
        });
      });
      
      process.on('SIGINT', () => {
        info('SIGINT received, shutting down gracefully...');
        server.close(() => {
          if (db) {
            db.close((err) => {
              if (err) {
                error('Error closing database: %s', err.message);
              } else {
                info('Database closed');
              }
              info('Server closed');
              process.exit(0);
            });
          } else {
            info('Server closed');
            process.exit(0);
          }
        });
      });
    })
    .catch((err) => {
      error('Failed to initialize database: %s', err.message);
      process.exit(1);
    });
}
  

// 如果直接运行此文件，启动服务器
if (require.main === module) {
  start();
}

module.exports = { start, generateConfig, calculateSHA256 };


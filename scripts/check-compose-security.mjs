import { execFileSync } from 'node:child_process'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

function fail(message) {
  throw new Error(`Compose 安全检查失败：${message}`)
}

const cleanEnv = { ...process.env }
for (const name of Object.keys(cleanEnv)) {
  if (name.startsWith('AIOPS_')) delete cleanEnv[name]
}

const tempDir = mkdtempSync(join(tmpdir(), 'kubepilot-compose-check-'))
const emptyEnv = join(tempDir, '.env')
writeFileSync(emptyEnv, '', 'utf8')

let config
try {
  const output = execFileSync(
    'docker',
    ['compose', '--env-file', emptyEnv, 'config', '--format', 'json'],
    { cwd: process.cwd(), env: cleanEnv, encoding: 'utf8' },
  )
  config = JSON.parse(output)
} finally {
  rmSync(tempDir, { recursive: true, force: true })
}

for (const serviceName of ['mysql', 'redis', 'core', 'console']) {
  if (!config.services?.[serviceName]) {
    fail(`缺少 ${serviceName} 服务`)
  }
}

for (const serviceName of ['backend', 'frontend']) {
  if (config.services?.[serviceName]) {
    fail(`不应继续使用旧服务名 ${serviceName}`)
  }
}

for (const serviceName of ['mysql', 'redis', 'core']) {
  const ports = config.services?.[serviceName]?.ports ?? []
  if (ports.length > 0) {
    fail(`${serviceName} 不应发布宿主机端口`)
  }
}

const consoleService = config.services.console
if (consoleService.image !== 'kubepilot-console:v0.1') {
  fail('console 必须使用 kubepilot-console:v0.1 镜像')
}

const consolePorts = consoleService.ports ?? []
if (consolePorts.length !== 1) fail('console 应只发布一个访问端口')

const consolePort = consolePorts[0]
if (consolePort.host_ip !== '127.0.0.1') {
  fail('console 默认必须只绑定 127.0.0.1')
}
if (String(consolePort.published) !== '5174' || consolePort.target !== 80) {
  fail('console 默认端口映射必须为 127.0.0.1:5174:80')
}

const coreService = config.services.core
if (coreService.image !== 'kubepilot-core:v0.1') {
  fail('core 必须使用 kubepilot-core:v0.1 镜像')
}
if (coreService.environment?.AIOPS_SECRET_KEY) {
  fail('core 不应内置固定 AIOPS_SECRET_KEY')
}
if (coreService.environment?.AIOPS_SECRET_KEY_FILE !== '/data/secret-key') {
  fail('core 必须从 /data/secret-key 加载自动生成的密钥')
}
if (!(coreService.volumes ?? []).some((volume) => volume.target === '/data')) {
  fail('core 必须挂载持久化 /data 卷')
}
if (!coreService.environment.AIOPS_DATABASE_DSN.includes('@tcp(mysql:3306)/')) {
  fail('core 必须通过 Compose 服务名连接 MySQL')
}
if (coreService.environment.AIOPS_REDIS_ADDR !== 'redis:6379') {
  fail('core 必须通过 Compose 服务名连接 Redis')
}

console.log('Compose 安全默认配置检查通过。')

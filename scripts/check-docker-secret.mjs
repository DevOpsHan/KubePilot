import { execFileSync, spawnSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'

const image = process.argv[2] ?? 'kubepilot-core:local'
const volume = `kubepilot-secret-check-${randomUUID()}`
const container = `${volume}-container`

function docker(args, options = {}) {
  return execFileSync('docker', args, { encoding: 'utf8', ...options }).trim()
}

function startContainer() {
  const result = spawnSync(
    'docker',
    ['run', '--rm', '--name', container, '--network', 'none', '--volume', `${volume}:/data`, image, 'true'],
    { encoding: 'utf8', timeout: 15_000 },
  )
  if (result.status !== 0) {
    throw new Error(`KubePilot Core 镜像入口执行失败：${result.stderr || result.stdout}`)
  }
}

function secretMetadata() {
  return docker([
    'run', '--rm', '--network', 'none', '--entrypoint', 'sh',
    '--volume', `${volume}:/data`, image, '-c',
    "test -f /data/secret-key && printf '%s|' \"$(wc -c < /data/secret-key | tr -d ' ')\" && printf '%s|' \"$(stat -c '%a' /data/secret-key)\" && sha256sum /data/secret-key | cut -d ' ' -f 1",
  ])
}

docker(['volume', 'create', volume])
try {
  startContainer()
  const first = secretMetadata()
  const [length, mode, firstHash] = first.split('|')
  if (length !== '32') throw new Error(`自动密钥长度应为 32 字节，实际为 ${length}`)
  if (mode !== '600') throw new Error(`自动密钥权限应为 600，实际为 ${mode}`)

  startContainer()
  const [, , secondHash] = secretMetadata().split('|')
  if (firstHash !== secondHash) throw new Error('容器重建后自动密钥发生变化')

  console.log('Docker 自动密钥持久化检查通过。')
} finally {
  spawnSync('docker', ['rm', '--force', container], { encoding: 'utf8' })
  docker(['volume', 'rm', '--force', volume])
}

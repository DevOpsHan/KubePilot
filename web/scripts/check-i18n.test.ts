import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, expect, it } from 'vitest'
import * as checker from './check-i18n.mjs'

const temporaryDirectories: string[] = []

function createProject(files: Record<string, string>) {
  const webRoot = mkdtempSync(path.join(tmpdir(), 'aiops-i18n-'))
  temporaryDirectories.push(webRoot)

  for (const [relativePath, source] of Object.entries(files)) {
    const absolutePath = path.join(webRoot, relativePath)
    mkdirSync(path.dirname(absolutePath), { recursive: true })
    writeFileSync(absolutePath, source, 'utf8')
  }

  return webRoot
}

afterEach(() => {
  for (const directory of temporaryDirectories.splice(0)) {
    rmSync(directory, { recursive: true, force: true })
  }
})

describe('findRuntimeChinese', () => {
  it('忽略中文注释', () => {
    expect(checker.findRuntimeChinese('sample.tsx', '// 中文注释\nexport const value = 1')).toEqual([])
  })

  it('报告 JSX 可见中文', () => {
    expect(checker.findRuntimeChinese('sample.tsx', 'export const View = () => <button>保存</button>'))
      .toEqual([expect.objectContaining({ file: 'sample.tsx', text: '保存' })])
  })

  it('报告运行时字符串中的中文', () => {
    expect(checker.findRuntimeChinese('sample.ts', "export const message = '加载失败'"))
      .toEqual([expect.objectContaining({ file: 'sample.ts', text: '加载失败' })])
  })

  it('报告无替换模板字符串中的中文', () => {
    expect(checker.findRuntimeChinese('sample.ts', 'export const message = `保存成功`'))
      .toEqual([expect.objectContaining({ text: '保存成功' })])
  })

  it('报告模板字符串头部、中部和尾部的中文', () => {
    const findings = checker.findRuntimeChinese(
      'sample.ts',
      'export const message = `开始${first}中间${second}结束`',
    )

    expect(findings.map(({ text }) => text)).toEqual(['开始', '中间', '结束'])
  })

  it('报告 JSX 属性中的中文', () => {
    expect(checker.findRuntimeChinese('sample.tsx', 'export const View = () => <button aria-label="保存" />'))
      .toEqual([expect.objectContaining({ text: '保存' })])
  })

  it('报告准确行号', () => {
    const findings = checker.findRuntimeChinese(
      'sample.ts',
      "export const value = 1\n\nexport const message = '加载失败'",
    )

    expect(findings).toEqual([
      expect.objectContaining({ file: 'sample.ts', line: 3, text: '加载失败' }),
    ])
  })

  it('将 Windows 文件路径归一化为正斜杠', () => {
    expect(checker.findRuntimeChinese('src\\pages\\Sample.tsx', '<div>保存</div>'))
      .toEqual([expect.objectContaining({ file: 'src/pages/Sample.tsx' })])
  })

  it('保守报告类型字面量和模块路径中的中文', () => {
    const findings = checker.findRuntimeChinese(
      'sample.ts',
      "import './中文模块'\ntype State = '已完成'",
    )

    expect(findings.map(({ text }) => text)).toEqual(['./中文模块', '已完成'])
  })

  it('按源码处理 JSX 实体，不主动解码为中文', () => {
    expect(checker.findRuntimeChinese('sample.tsx', '<span>&#x4FDD;&#x5B58;</span>')).toEqual([])
  })
})

describe('collectSourceFiles', () => {
  it('递归扫描整个 src 并只排除测试与当前允许的国际化文件', () => {
    const webRoot = createProject({
      'src/App.tsx': 'export default function App() { return null }',
      'src/main.tsx': 'export const main = true',
      'src/router.tsx': 'export const router = true',
      'src/components/ui/button.tsx': 'export const Button = true',
      'src/nested/feature.ts': 'export const feature = true',
      'src/example.test.ts': "export const text = '测试'",
      'src/example.spec.tsx': "export const text = '测试'",
      'src/__tests__/helper.ts': "export const text = '测试'",
      'src/test/setup.ts': "export const text = '测试'",
      'src/i18n/locales/zh/common.ts': "export const text = '保存'",
      'src/i18n/locales/en/common.ts': "export const text = 'Save'",
      'src/i18n/zh.ts': "export const text = '保存'",
      'src/i18n/en.ts': "export const text = 'Save'",
      'src/i18n/errors.ts': "export const text = '加载失败'",
      'src/i18n/index.ts': 'export const language = true',
      'src/README.md': '中文说明',
    })

    expect(checker.collectSourceFiles(webRoot).map(({ relativePath }) => relativePath)).toEqual([
      'src/App.tsx',
      'src/components/ui/button.tsx',
      'src/i18n/en.ts',
      'src/i18n/index.ts',
      'src/i18n/zh.ts',
      'src/main.tsx',
      'src/nested/feature.ts',
      'src/router.tsx',
    ])
  })
})

describe('hashRuntimeTexts', () => {
  const finding = (text: string, line = 1) => ({ file: 'sample.ts', line, text })

  it('忽略违规顺序和行号', () => {
    const first = checker.hashRuntimeTexts([finding('保存', 1), finding('加载失败', 2)])
    const second = checker.hashRuntimeTexts([finding('加载失败', 200), finding('保存', 100)])

    expect(first).toBe(second)
  })

  it('在新增、删除、修改或重复中文时改变哈希', () => {
    const baseline = checker.hashRuntimeTexts([finding('保存'), finding('加载失败')])
    const changedHashes = [
      checker.hashRuntimeTexts([finding('保存'), finding('加载失败'), finding('新增')]),
      checker.hashRuntimeTexts([finding('保存')]),
      checker.hashRuntimeTexts([finding('保存'), finding('加载错误')]),
      checker.hashRuntimeTexts([finding('保存'), finding('加载失败'), finding('加载失败')]),
    ]

    expect(changedHashes).not.toContain(baseline)
    expect(new Set(changedHashes).size).toBe(changedHashes.length)
  })
})

describe('scanProject', () => {
  it('接受文本多重集相同但行号变化的固定基线', () => {
    const baselineFindings = checker.findRuntimeChinese(
      'src/legacy.ts',
      "export const message = '旧提示'",
    )
    const webRoot = createProject({
      'src/legacy.ts': "\n\nexport const message = '旧提示'",
    })

    const result = checker.scanProject(webRoot, {
      'src/legacy.ts': checker.hashRuntimeTexts(baselineFindings),
    })

    expect(result.issues).toEqual([])
  })

  it('拒绝哈希变化的旧基线', () => {
    const webRoot = createProject({
      'src/legacy.ts': "export const message = '已修改'",
    })

    const result = checker.scanProject(webRoot, {
      'src/legacy.ts': '0'.repeat(64),
    })

    expect(result.issues).toEqual([
      expect.objectContaining({
        kind: 'baseline-changed',
        file: 'src/legacy.ts',
        expectedHash: '0'.repeat(64),
        actualHash: expect.any(String),
      }),
    ])
  })

  it('拒绝不存在的基线文件', () => {
    const webRoot = createProject({
      'src/App.tsx': 'export default function App() { return null }',
    })

    const result = checker.scanProject(webRoot, {
      'src/missing.ts': '0'.repeat(64),
    })

    expect(result.issues).toEqual([
      expect.objectContaining({ kind: 'baseline-missing', file: 'src/missing.ts' }),
    ])
  })

  it('拒绝已经没有中文命中的基线文件', () => {
    const webRoot = createProject({
      'src/legacy.ts': 'export const ready = true',
    })

    const result = checker.scanProject(webRoot, {
      'src/legacy.ts': '0'.repeat(64),
    })

    expect(result.issues).toEqual([
      expect.objectContaining({ kind: 'baseline-empty', file: 'src/legacy.ts' }),
    ])
  })

  it('报告不在固定基线中的中文', () => {
    const webRoot = createProject({
      'src/App.tsx': '<main>新增中文</main>',
    })

    const result = checker.scanProject(webRoot, {})

    expect(result.issues).toEqual([
      expect.objectContaining({
        kind: 'violation',
        file: 'src/App.tsx',
        line: 1,
        text: '新增中文',
      }),
    ])
  })
})

describe('LEGACY_BASELINES', () => {
  it('完整迁移后不再允许任何旧违规基线', () => {
    expect(checker.LEGACY_BASELINES).toEqual({})
  })

  it('匹配当前项目的真实中文多重集', () => {
    const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

    expect(checker.scanProject(webRoot, checker.LEGACY_BASELINES).issues).toEqual([])
  })
})

describe('formatDiagnostics', () => {
  it('使用稳定前缀并将违规文本压缩成单行', () => {
    const webRoot = createProject({
      'src/App.tsx': '<main>新增\n  中文</main>',
    })
    const result = checker.scanProject(webRoot, {})

    expect(checker.formatDiagnostics(result)).toEqual([
      '[i18n-check] src/App.tsx:1 新增 中文',
    ])
  })
})

describe('runCli', () => {
  it('无违规时返回成功退出码且不输出诊断', () => {
    const webRoot = createProject({
      'src/App.tsx': 'export default function App() { return null }',
    })
    const output: string[] = []

    expect(checker.runCli({ webRoot, baselines: {}, writeLine: (line) => output.push(line) }))
      .toBe(0)
    expect(output).toEqual([])
  })

  it('有违规时返回失败退出码并逐行输出诊断', () => {
    const webRoot = createProject({
      'src/App.tsx': '<main>新增\n  中文</main>',
    })
    const output: string[] = []

    expect(checker.runCli({ webRoot, baselines: {}, writeLine: (line) => output.push(line) }))
      .toBe(1)
    expect(output).toEqual(['[i18n-check] src/App.tsx:1 新增 中文'])
    expect(output.every((line) => !/[\r\n]/u.test(line))).toBe(true)
  })

  it('捕获文件系统异常并输出稳定错误前缀', () => {
    const webRoot = path.join(createProject({}), 'missing')
    const output: string[] = []

    expect(checker.runCli({ webRoot, baselines: {}, writeLine: (line) => output.push(line) }))
      .toBe(1)
    expect(output).toHaveLength(1)
    expect(output[0]).toMatch(/^\[i18n-check\] 扫描失败:/u)
  })
})

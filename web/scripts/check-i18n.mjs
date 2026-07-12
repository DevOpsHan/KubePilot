// @ts-check

import { createHash } from 'node:crypto'
import { readdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

/** @typedef {{ file: string, line: number, text: string }} Finding */
/** @typedef {{ absolutePath: string, relativePath: string }} SourceFileEntry */
/** @typedef {'violation' | 'baseline-missing' | 'baseline-empty' | 'baseline-changed'} IssueKind */
/**
 * @typedef {object} ScanIssue
 * @property {IssueKind} kind
 * @property {string} file
 * @property {number} [line]
 * @property {string} [text]
 * @property {string} [expectedHash]
 * @property {string} [actualHash]
 */
/** @typedef {{ files: SourceFileEntry[], issues: ScanIssue[] }} ScanResult */
/**
 * @typedef {object} CliOptions
 * @property {string} [webRoot]
 * @property {Readonly<Record<string, string>>} [baselines]
 * @property {(line: string) => void} [writeLine]
 */

const HAN_SCRIPT = /\p{Script=Han}/u
const DIAGNOSTIC_PREFIX = '[i18n-check]'
const SCRIPT_FILE = fileURLToPath(import.meta.url)
const WEB_ROOT = path.resolve(path.dirname(SCRIPT_FILE), '..')
const SOURCE_EXTENSIONS = new Set([
  '.cjs',
  '.cts',
  '.js',
  '.jsx',
  '.mjs',
  '.mts',
  '.ts',
  '.tsx',
])
const ALLOWED_I18N_FILES = new Set([
  'src/i18n/errors.ts',
])

/** @type {Readonly<Record<string, string>>} */
export const LEGACY_BASELINES = Object.freeze({
})

/**
 * @param {string} file
 * @returns {string}
 */
function normalizePath(file) {
  return file.replaceAll('\\', '/')
}

/**
 * @param {string} left
 * @param {string} right
 * @returns {number}
 */
function compareText(left, right) {
  if (left < right) return -1
  if (left > right) return 1
  return 0
}

/**
 * @param {string} file
 * @returns {import('typescript').ScriptKind}
 */
function scriptKindFor(file) {
  const extension = path.extname(file).toLowerCase()
  if (extension === '.tsx') return ts.ScriptKind.TSX
  if (extension === '.jsx') return ts.ScriptKind.JSX
  if (extension === '.js' || extension === '.mjs' || extension === '.cjs') {
    return ts.ScriptKind.JS
  }
  return ts.ScriptKind.TS
}

/**
 * 当前保守报告所有字符串字面量，包括类型位置和模块路径；JSX 实体按源码处理，不做 HTML 解码。
 * @param {import('typescript').Node} node
 * @param {import('typescript').SourceFile} sourceFile
 * @returns {string | undefined}
 */
function runtimeTextOf(node, sourceFile) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
    return node.text
  }
  if (ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) {
    return node.text
  }
  if (ts.isJsxText(node)) {
    return node.getText(sourceFile).trim()
  }
  return undefined
}

/**
 * @param {string} file
 * @param {string} source
 * @returns {Finding[]}
 */
export function findRuntimeChinese(file, source) {
  const normalizedFile = normalizePath(file)
  const sourceFile = ts.createSourceFile(
    normalizedFile,
    source,
    ts.ScriptTarget.Latest,
    true,
    scriptKindFor(normalizedFile),
  )
  /** @type {Finding[]} */
  const findings = []

  /** @param {import('typescript').Node} node */
  function visit(node) {
    const runtimeText = runtimeTextOf(node, sourceFile)
    if (runtimeText && HAN_SCRIPT.test(runtimeText)) {
      const position = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile))
      findings.push({ file: normalizedFile, line: position.line + 1, text: runtimeText })
    }
    ts.forEachChild(node, visit)
  }

  visit(sourceFile)
  return findings
}

/**
 * @param {Finding[]} findings
 * @returns {string}
 */
export function hashRuntimeTexts(findings) {
  const texts = findings.map(({ text }) => text).sort(compareText)
  return createHash('sha256').update(JSON.stringify(texts), 'utf8').digest('hex')
}

/**
 * @param {string} relativePath
 * @returns {boolean}
 */
function isSkippedDirectory(relativePath) {
  return relativePath === 'src/test'
    || relativePath.startsWith('src/test/')
    || relativePath === 'src/i18n/locales'
    || relativePath.startsWith('src/i18n/locales/')
    || relativePath.split('/').includes('__tests__')
}

/**
 * @param {string} relativePath
 * @returns {boolean}
 */
function isTestFile(relativePath) {
  const fileName = path.posix.basename(relativePath)
  return /\.(?:test|spec)\.[cm]?[jt]sx?$/u.test(fileName)
}

/**
 * @param {string} relativePath
 * @returns {boolean}
 */
function isSkippedFile(relativePath) {
  return isTestFile(relativePath) || ALLOWED_I18N_FILES.has(relativePath)
}

/**
 * @param {string} webRoot
 * @returns {SourceFileEntry[]}
 */
export function collectSourceFiles(webRoot) {
  /** @type {SourceFileEntry[]} */
  const files = []

  /** @param {string} directory */
  function visitDirectory(directory) {
    const entries = readdirSync(directory, { withFileTypes: true })
      .sort((left, right) => compareText(left.name, right.name))

    for (const entry of entries) {
      const absolutePath = path.join(directory, entry.name)
      const relativePath = normalizePath(path.relative(webRoot, absolutePath))

      if (entry.isDirectory()) {
        if (!isSkippedDirectory(relativePath)) {
          visitDirectory(absolutePath)
        }
        continue
      }

      const extension = path.extname(entry.name).toLowerCase()
      if (entry.isFile()
        && SOURCE_EXTENSIONS.has(extension)
        && !isSkippedFile(relativePath)) {
        files.push({ absolutePath, relativePath })
      }
    }
  }

  visitDirectory(path.join(webRoot, 'src'))
  return files.sort((left, right) => compareText(left.relativePath, right.relativePath))
}

/**
 * @param {string} webRoot
 * @param {Readonly<Record<string, string>>} [baselines]
 * @returns {ScanResult}
 */
export function scanProject(webRoot, baselines = LEGACY_BASELINES) {
  const files = collectSourceFiles(webRoot)
  /** @type {Map<string, Finding[]>} */
  const findingsByFile = new Map()
  /** @type {ScanIssue[]} */
  const issues = []

  for (const file of files) {
    const source = readFileSync(file.absolutePath, 'utf8')
    findingsByFile.set(file.relativePath, findRuntimeChinese(file.relativePath, source))
  }

  for (const file of Object.keys(baselines).sort(compareText)) {
    const findings = findingsByFile.get(file)
    if (!findings) {
      issues.push({ kind: 'baseline-missing', file })
      continue
    }
    if (findings.length === 0) {
      issues.push({ kind: 'baseline-empty', file })
      continue
    }

    const actualHash = hashRuntimeTexts(findings)
    const expectedHash = baselines[file]
    if (actualHash !== expectedHash) {
      issues.push({ kind: 'baseline-changed', file, expectedHash, actualHash })
    }
  }

  for (const file of files) {
    if (Object.hasOwn(baselines, file.relativePath)) {
      continue
    }
    for (const finding of findingsByFile.get(file.relativePath) ?? []) {
      issues.push({ kind: 'violation', ...finding })
    }
  }

  return { files, issues }
}

/**
 * @param {string} text
 * @returns {string}
 */
function toSingleLine(text) {
  return text.replace(/\s+/gu, ' ').trim()
}

/**
 * @param {ScanResult} result
 * @returns {string[]}
 */
export function formatDiagnostics(result) {
  return result.issues.map((issue) => {
    if (issue.kind === 'violation') {
      return `${DIAGNOSTIC_PREFIX} ${issue.file}:${issue.line} ${toSingleLine(issue.text ?? '')}`
    }
    if (issue.kind === 'baseline-missing') {
      return `${DIAGNOSTIC_PREFIX} 基线失效 ${issue.file}: 文件不存在或不在扫描范围`
    }
    if (issue.kind === 'baseline-empty') {
      return `${DIAGNOSTIC_PREFIX} 基线失效 ${issue.file}: 文件已无中文命中`
    }
    return `${DIAGNOSTIC_PREFIX} 基线变化 ${issue.file}: expected=${issue.expectedHash} actual=${issue.actualHash}`
  })
}

/**
 * @param {string} line
 * @returns {void}
 */
function writeErrorLine(line) {
  console.error(line)
}

/**
 * @param {CliOptions} [options]
 * @returns {0 | 1}
 */
export function runCli(options = {}) {
  const webRoot = options.webRoot ?? WEB_ROOT
  const baselines = options.baselines ?? LEGACY_BASELINES
  const writeLine = options.writeLine ?? writeErrorLine

  try {
    const result = scanProject(webRoot, baselines)
    for (const line of formatDiagnostics(result)) {
      writeLine(line)
    }
    return result.issues.length === 0 ? 0 : 1
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    writeLine(`${DIAGNOSTIC_PREFIX} 扫描失败: ${toSingleLine(message)}`)
    return 1
  }
}

const invokedScript = process.argv[1] ? path.resolve(process.argv[1]) : undefined
const isMainModule = invokedScript && process.platform === 'win32'
  ? invokedScript.toLowerCase() === SCRIPT_FILE.toLowerCase()
  : invokedScript === SCRIPT_FILE

if (isMainModule) {
  process.exitCode = runCli()
}

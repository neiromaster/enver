'use strict'

const { test } = require('node:test')
const assert = require('node:assert')
const { spawnSync, spawn } = require('node:child_process')
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')

const shim = require('../bin/enver.js')

const SHIM_PATH = path.join(__dirname, '..', 'bin', 'enver.js')
const WIN = process.platform === 'win32'

// A node child that counts the SIGINTs it receives and exits 100+n after a
// short grace window, so a second (double) delivery is observable.
const SIGINT_COUNT = `#!/usr/bin/env node
let n = 0
process.on('SIGINT', () => {
  n++
  setTimeout(() => process.exit(100 + n), 200)
})
setInterval(() => {}, 1000)
`

// A node child that re-raises SIGINT so it dies from the signal
// deterministically.
const SIGINT_DIE = `#!/usr/bin/env node
process.on('SIGINT', () => {
  process.removeAllListeners('SIGINT')
  process.kill(process.pid, 'SIGINT')
})
setInterval(() => {}, 1000)
`

function fakePlatformPackage(dir, key, { exitCode = 0, binContent, binMode = 0o755 } = {}) {
  const pkgDir = path.join(dir, 'node_modules', '@enver-go', `enver-${key}`)
  fs.mkdirSync(path.join(pkgDir, 'bin'), { recursive: true })
  fs.writeFileSync(path.join(pkgDir, 'package.json'), '{}')
  if (binContent === null) return undefined
  const binPath = path.join(pkgDir, 'bin', shim.binaryName(key))
  fs.writeFileSync(binPath, binContent ?? `#!/usr/bin/env node\nprocess.exit(${exitCode})\n`)
  fs.chmodSync(binPath, binMode)
  return binPath
}

test('platformKey is <platform>-<arch>', () => {
  assert.match(shim.platformKey(), /^(darwin|linux|win32)-(x64|arm64)$/)
})

test('binaryName appends .exe on win32', () => {
  assert.equal(shim.binaryName('win32-x64'), 'enver.exe')
  assert.equal(shim.binaryName('darwin-arm64'), 'enver')
})

test('packageName prefixes the scope', () => {
  assert.equal(shim.packageName('darwin-arm64'), '@enver-go/enver-darwin-arm64')
})

test('shim propagates the child exit code', { skip: WIN }, () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'enver-shim-'))
  try {
    fakePlatformPackage(dir, shim.platformKey(), { exitCode: 42 })
    const r = spawnSync(process.execPath, [SHIM_PATH], {
      env: { ...process.env, NODE_PATH: path.join(dir, 'node_modules') },
      encoding: 'utf8',
    })
    assert.equal(r.status, 42)
  } finally {
    fs.rmSync(dir, { recursive: true, force: true })
  }
})

test('shim exits 1 with a message when the platform package is missing', () => {
  const r = spawnSync(process.execPath, [SHIM_PATH], {
    env: { ...process.env, NODE_PATH: path.join(os.tmpdir(), 'enver-missing') },
    encoding: 'utf8',
  })
  assert.equal(r.status, 1)
  assert.match(r.stderr, /not installed/)
})

test('shim exits 1 with a message when the binary is not on disk', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'enver-no-bin-'))
  try {
    fakePlatformPackage(dir, shim.platformKey(), { binContent: null })
    const r = spawnSync(process.execPath, [SHIM_PATH], {
      env: { ...process.env, NODE_PATH: path.join(dir, 'node_modules') },
      encoding: 'utf8',
    })
    assert.equal(r.status, 1)
    assert.match(r.stderr, /binary missing/)
  } finally {
    fs.rmSync(dir, { recursive: true, force: true })
  }
})

test('shim exits 1 when the binary is not executable', { skip: WIN }, () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'enver-noexec-'))
  try {
    fakePlatformPackage(dir, shim.platformKey(), { binMode: 0o644 })
    const r = spawnSync(process.execPath, [SHIM_PATH], {
      env: { ...process.env, NODE_PATH: path.join(dir, 'node_modules') },
      encoding: 'utf8',
    })
    assert.equal(r.status, 1)
    assert.match(r.stderr, /failed to execute binary/)
  } finally {
    fs.rmSync(dir, { recursive: true, force: true })
  }
})

test('shim forwards exactly one SIGINT per terminal Ctrl+C', { skip: WIN }, async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'enver-sigint-once-'))
  try {
    fakePlatformPackage(dir, shim.platformKey(), { binContent: SIGINT_COUNT })

    // detached makes the shim its own process group leader, so a group-wide
    // signal — what a terminal delivers to the foreground group — reaches the
    // shim and only the shim, which forwards it exactly once.
    const child = spawn(process.execPath, [SHIM_PATH], {
      env: { ...process.env, NODE_PATH: path.join(dir, 'node_modules') },
      detached: true,
    })

    await new Promise((resolve, reject) => {
      child.on('exit', (code) => {
        try {
          assert.equal(code, 101, 'the child must see exactly one SIGINT')
          resolve()
        } catch (e) {
          reject(e)
        }
      })
      setTimeout(() => {
        process.kill(-child.pid, 'SIGINT')
      }, 300)
      setTimeout(() => reject(new Error('shim did not exit')), 3000)
    })
  } finally {
    fs.rmSync(dir, { recursive: true, force: true })
  }
})

test('shim propagates SIGINT', { skip: WIN }, async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'enver-sigint-'))
  try {
    fakePlatformPackage(dir, shim.platformKey(), { binContent: SIGINT_DIE })

    const child = spawn(process.execPath, [SHIM_PATH], {
      env: { ...process.env, NODE_PATH: path.join(dir, 'node_modules') },
    })

    await new Promise((resolve, reject) => {
      child.on('exit', (code, signal) => {
        try {
          assert.equal(signal, 'SIGINT')
          resolve()
        } catch (e) {
          reject(e)
        }
      })

      // Give it a moment to start and register listeners
      setTimeout(() => {
        child.kill('SIGINT')
      }, 100)
    })
  } finally {
    fs.rmSync(dir, { recursive: true, force: true })
  }
})

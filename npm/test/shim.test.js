'use strict'

const { test } = require('node:test')
const assert = require('node:assert')
const { spawnSync, spawn } = require('node:child_process')
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')

const shim = require('../bin/enver.js')

const SHIM_PATH = path.join(__dirname, '..', 'bin', 'enver.js')

function fakePlatformPackage(dir, key, exitCode) {
  const pkgDir = path.join(dir, 'node_modules', '@neiromaster', `enver-${key}`)
  fs.mkdirSync(path.join(pkgDir, 'bin'), { recursive: true })
  fs.writeFileSync(path.join(pkgDir, 'package.json'), '{}')
  const binPath = path.join(pkgDir, 'bin', shim.binaryName(key))
  fs.writeFileSync(binPath, `#!/usr/bin/env node\nprocess.exit(${exitCode})\n`)
  fs.chmodSync(binPath, 0o755)
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
  assert.equal(shim.packageName('darwin-arm64'), '@neiromaster/enver-darwin-arm64')
})

test('resolveBinary points at the platform package bin', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'enver-shim-'))
  try {
    const binPath = fakePlatformPackage(dir, 'darwin-arm64', 0)
    assert.equal(fs.realpathSync(shim.resolveBinary('darwin-arm64', dir)), fs.realpathSync(binPath))
  } finally {
    fs.rmSync(dir, { recursive: true, force: true })
  }
})

test('shim propagates the child exit code', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'enver-shim-'))
  try {
    fakePlatformPackage(dir, shim.platformKey(), 42)
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

test('shim exits 1 when binary is missing/not executable', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'enver-no-bin-'))
  try {
    const pkgDir = path.join(dir, 'node_modules', '@neiromaster', `enver-${shim.platformKey()}`)
    fs.mkdirSync(pkgDir, { recursive: true })
    fs.writeFileSync(path.join(pkgDir, 'package.json'), '{}')
    
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

test('shim propagates SIGINT', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'enver-sigint-'))
  try {
    const pkgDir = path.join(dir, 'node_modules', '@neiromaster', `enver-${shim.platformKey()}`)
    fs.mkdirSync(path.join(pkgDir, 'bin'), { recursive: true })
    fs.writeFileSync(path.join(pkgDir, 'package.json'), '{}')
    const binPath = path.join(pkgDir, 'bin', shim.binaryName(shim.platformKey()))
    
    // Use a shell script that sleeps
    fs.writeFileSync(binPath, '#!/bin/sh\nsleep 30\n')
    fs.chmodSync(binPath, 0o755)

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

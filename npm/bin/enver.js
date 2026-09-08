#!/usr/bin/env node
'use strict'

// Bin shim for the @neiromaster/enver npm package. The real binary lives in a
// platform package installed as an optionalDependency; this shim resolves it
// and spawns it, so `enver` works with zero lifecycle scripts (pnpm-safe).

const { spawn } = require('node:child_process')
const { createRequire } = require('node:module')
const path = require('node:path')

const PKG_PREFIX = '@neiromaster/enver'

function platformKey() {
  return `${process.platform}-${process.arch}`
}

function binaryName(key) {
  return key.startsWith('win32') ? 'enver.exe' : 'enver'
}

function packageName(key) {
  return `${PKG_PREFIX}-${key}`
}

function resolveBinary(key, baseDir) {
  const req = baseDir ? createRequire(path.join(baseDir, 'noop.js')) : require
  const pkgJson = req.resolve(`${packageName(key)}/package.json`)
  return path.join(path.dirname(pkgJson), 'bin', binaryName(key))
}

function run(binPath, args) {
  const child = spawn(binPath, args, { stdio: 'inherit' })
  for (const sig of ['SIGINT', 'SIGTERM']) {
    process.on(sig, () => child.kill(sig))
  }
  child.on('exit', (code, signal) => {
    if (signal) process.kill(process.pid, signal)
    else process.exit(code ?? 1)
  })
}

function main() {
  const key = platformKey()
  let binPath
  try {
    binPath = resolveBinary(key)
  } catch {
    console.error(`[enver] no binary for ${key}: platform package ${packageName(key)} is not installed.`)
    console.error('  Reinstall without --omit=optional, or check that this platform is supported.')
    process.exit(1)
  }
  run(binPath, process.argv.slice(2))
}

if (require.main === module) main()

module.exports = { platformKey, binaryName, packageName, resolveBinary, run }

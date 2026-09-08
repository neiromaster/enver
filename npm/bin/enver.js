#!/usr/bin/env node
'use strict'

// Bin shim for the @neiromaster/enver npm package. The real binary lives in a
// platform package installed as an optionalDependency; this shim resolves it
// and spawns it, so `enver` works with zero lifecycle scripts (pnpm-safe).

const { spawn } = require('node:child_process')
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')

const PKG_PREFIX = '@neiromaster/enver'

// SIGHUP is forwarded because the detached child no longer receives terminal
// hangup directly — only the shim does.
const FORWARDED_SIGNALS = ['SIGINT', 'SIGTERM', 'SIGHUP']

function platformKey() {
  return `${process.platform}-${process.arch}`
}

function binaryName(key) {
  return key.startsWith('win32') ? 'enver.exe' : 'enver'
}

function packageName(key) {
  return `${PKG_PREFIX}-${key}`
}

function resolveBinary(key) {
  const pkgJson = require.resolve(`${packageName(key)}/package.json`)
  return path.join(path.dirname(pkgJson), 'bin', binaryName(key))
}

function run(binPath, args) {
  // detached gives the child its own process group, so a terminal SIGINT
  // reaches only the shim and is forwarded exactly once. With the child in
  // the shim's group the kernel delivers the signal to both.
  const child = spawn(binPath, args, { stdio: 'inherit', detached: true })
  for (const sig of FORWARDED_SIGNALS) {
    process.on(sig, () => child.kill(sig))
  }
  child.on('error', (err) => {
    console.error(`[enver] failed to execute binary: ${err.message}`)
    process.exit(1)
  })
  child.on('exit', (code, signal) => {
    if (signal) {
      for (const sig of FORWARDED_SIGNALS) process.removeAllListeners(sig)
      // Default disposition normally finishes the job; kernels drop
      // default-disposition signals sent to PID 1, hence the fallback.
      process.kill(process.pid, signal)
      process.exit(128 + os.constants.signals[signal])
    } else {
      process.exit(code ?? 1)
    }
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
  if (!fs.existsSync(binPath)) {
    console.error(`[enver] binary missing at ${binPath}.`)
    console.error('  The platform package resolved but its binary was never unpacked to disk')
    console.error('  (e.g. Yarn PnP). Install enver globally with npm instead.')
    process.exit(1)
  }
  run(binPath, process.argv.slice(2))
}

if (require.main === module) main()

module.exports = { platformKey, binaryName, packageName, resolveBinary, run }
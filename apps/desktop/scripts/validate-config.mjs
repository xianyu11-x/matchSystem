import { readFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const desktopRoot = resolve(fileURLToPath(new URL('..', import.meta.url)))
const configPath = resolve(desktopRoot, 'src-tauri', 'tauri.conf.json')
const capabilityPath = resolve(desktopRoot, 'src-tauri', 'capabilities', 'default.json')
const packagePath = resolve(desktopRoot, 'package.json')
const updaterManifestPath = resolve(desktopRoot, 'updater', 'Cargo.toml')
const updaterLockPath = resolve(desktopRoot, 'updater', 'Cargo.lock')

const loadJson = async (path) => JSON.parse(await readFile(path, 'utf8'))
const [config, capability, packageJson, updaterManifest, updaterLock] = await Promise.all([
  loadJson(configPath),
  loadJson(capabilityPath),
  loadJson(packagePath),
  readFile(updaterManifestPath, 'utf8'),
  readFile(updaterLockPath, 'utf8'),
])

const assert = (condition, message) => {
  if (!condition) throw new Error(message)
}

assert(config.build?.frontendDist === '../../web/dist', 'frontendDist must reuse apps/web/dist')
assert(config.build?.devUrl === 'http://127.0.0.1:5173', 'devUrl must match the Vite dev server')
assert(
  config.bundle?.externalBin?.includes('binaries/simulator-api'),
  'externalBin must include binaries/simulator-api',
)
assert(
  config.bundle?.targets?.includes('nsis') && config.bundle?.targets?.includes('msi'),
  'Windows NSIS and MSI targets must be enabled',
)
assert(
  capability.permissions?.some(
    (permission) =>
      permission?.identifier === 'shell:allow-spawn' &&
      permission.allow?.some(
        (entry) =>
          entry.name === 'binaries/simulator-api' &&
          entry.sidecar === true &&
          entry.args?.join('\u0000') === '--addr\u0000127.0.0.1:0',
      ),
  ),
  'shell capability must allow the simulator-api sidecar and fixed loopback args',
)
const packageStart = updaterManifest.indexOf('[package]')
const packageBody = packageStart >= 0 ? updaterManifest.slice(packageStart + '[package]'.length) : ''
const nextPackageSection = packageBody.search(/^\[/m)
const updaterPackage = nextPackageSection >= 0 ? packageBody.slice(0, nextPackageSection) : packageBody
assert(/^\s*name\s*=\s*['"]matchscope-updater['"]\s*$/m.test(updaterPackage), 'updater Cargo package must be matchscope-updater')
assert(/\[\[bin\]\][\s\S]*?^\s*name\s*=\s*['"]Updater['"]\s*$/m.test(updaterManifest), 'updater Cargo binary must be Updater')
const updaterVersion = updaterPackage.match(/^\s*version\s*=\s*['"]([^'"]+)['"]\s*$/m)?.[1]
assert(updaterVersion === packageJson.version, 'updater Cargo version must match desktop package.json')
const lockedUpdaterVersion = updaterLock.match(/\[\[package\]\]\s*name\s*=\s*['"]matchscope-updater['"]\s*version\s*=\s*['"]([^'"]+)['"]/s)?.[1]
assert(lockedUpdaterVersion === updaterVersion, 'updater Cargo.lock version must match updater Cargo.toml')
for (const script of ['build:web', 'build:sidecar', 'build:updater', 'build:portable', 'dev', 'build']) {
  assert(typeof packageJson.scripts?.[script] === 'string', 'missing npm script: ' + script)
}

console.log('Tauri desktop JSON/npm configuration is valid.')

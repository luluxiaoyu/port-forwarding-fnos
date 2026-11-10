import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import {
  chmodSync, copyFileSync, cpSync, mkdirSync, mkdtempSync, readFileSync,
  renameSync, rmSync, statSync, writeFileSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'

const root = fileURLToPath(new URL('../', import.meta.url))
const output = join(root, 'dist_fpk')
const baseManifest = readFileSync(join(root, 'manifest'), 'utf8').trim()
const fields = manifestFields(baseManifest)
const app = fields.get('appname')
const version = (process.env.APP_VERSION ?? fields.get('version') ?? '').replace(/^v/, '')
const targets = [
  { arch: 'amd64', platform: 'x86', machine: 62 },
  { arch: 'arm64', platform: 'arm', machine: 183 },
]
const outerFiles = ['manifest', 'app.tgz', 'cmd', 'config', 'wizard', 'ui', 'ICON.PNG', 'ICON_256.PNG']
const lifecycleScripts = [
  'main', 'install_init', 'install_callback', 'config_init', 'config_callback',
  'upgrade_init', 'upgrade_callback', 'uninstall_init', 'uninstall_callback',
]

assert.match(app ?? '', /^[a-z][a-z0-9-]*$/, 'Invalid application name')
assert.match(version, /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/, 'Expected an X.Y.Z version')

function manifestFields(data) {
  return new Map(data.split(/\r?\n/).filter(line => line.includes('=')).map(line => {
    const split = line.indexOf('=')
    return [line.slice(0, split), line.slice(split + 1)]
  }))
}

function checksum(path) {
  return createHash('md5').update(readFileSync(path)).digest('hex')
}

function run(command, args, env = {}) {
  execFileSync(command, args, {
    cwd: root, stdio: 'inherit',
    env: { ...process.env, COPYFILE_DISABLE: '1', ...env },
  })
}

function pack(target, work) {
  const payload = join(work, 'payload')
  const outer = join(work, 'outer')
  const binary = join(payload, 'bin', 'port-forwarding')
  mkdirSync(join(payload, 'bin'), { recursive: true })
  mkdirSync(outer)
  run('go', ['build', '-trimpath', '-o', binary, '.'], {
    CGO_ENABLED: '0', GOOS: 'linux', GOARCH: target.arch,
  })
  chmodSync(binary, 0o755)
  cpSync(join(root, 'frontend/dist'), join(payload, 'www'), { recursive: true })
  cpSync(join(root, 'app/ui'), join(payload, 'ui'), { recursive: true })
  const payloadArchive = join(outer, 'app.tgz')
  run('tar', ['--format=ustar', '-czf', payloadArchive, '-C', payload, 'bin', 'www', 'ui'])

  for (const dir of ['cmd', 'config']) {
    cpSync(join(root, dir), join(outer, dir), { recursive: true })
  }
  mkdirSync(join(outer, 'wizard'))
  cpSync(join(root, 'app/ui'), join(outer, 'ui'), { recursive: true })
  for (const icon of ['ICON.PNG', 'ICON_256.PNG']) {
    copyFileSync(join(root, icon), join(outer, icon))
  }
  const manifest = new Map(fields)
  manifest.set('platform', target.platform)
  manifest.set('version', version)
  manifest.set('checksum', checksum(payloadArchive))
  writeFileSync(join(outer, 'manifest'), Array.from(manifest, ([key, value]) => key + '=' + value).join('\n') + '\n')
  const file = app + '_' + version + '_' + target.arch + '.fpk'
  const path = join(work, file)
  run('tar', ['--format=ustar', '-czf', path, '-C', outer, ...outerFiles])
  verify(path, target, work)
  return { path, file }
}

function verify(path, target, work) {
  const outer = join(work, 'check-outer')
  const inner = join(work, 'check-inner')
  mkdirSync(outer)
  mkdirSync(inner)
  run('tar', ['-xzf', path, '-C', outer])
  const manifest = manifestFields(readFileSync(join(outer, 'manifest'), 'utf8'))
  assert.equal(manifest.get('version'), version)
  assert.equal(manifest.get('platform'), target.platform)
  const archive = join(outer, 'app.tgz')
  assert.equal(manifest.get('checksum'), checksum(archive))
  for (const name of lifecycleScripts) {
    const path = 'cmd/' + name
    const script = statSync(join(outer, path))
    assert.ok(script.isFile() && (script.mode & 0o111), path + ' must be an executable file')
  }
  assert.ok(statSync(join(outer, 'wizard')).isDirectory())
  const config = readFileSync(join(outer, 'ui/config'))
  const launchName = app + '.Application'
  assert.equal(manifest.get('desktop_applaunchname'), launchName)
  assert.ok(JSON.parse(config)['.url']?.[launchName], 'Desktop launch entry is missing')
  JSON.parse(readFileSync(join(outer, 'config/privilege')))
  JSON.parse(readFileSync(join(outer, 'config/resource')))
  run('tar', ['-xzf', archive, '-C', inner])
  assert.deepEqual(readFileSync(join(inner, 'ui/config')), config)
  assert.ok(statSync(join(inner, 'www/index.html')).size)
  const binaryPath = join(inner, 'bin/port-forwarding')
  assert.ok(statSync(binaryPath).mode & 0o111, 'The daemon is not executable')
  const binary = readFileSync(binaryPath)
  assert.equal(binary.subarray(0, 4).toString('hex'), '7f454c46', 'Expected an ELF daemon')
  assert.equal(binary[4], 2, 'Expected a 64-bit ELF')
  assert.equal(binary[5], 1, 'Expected a little-endian ELF')
  assert.equal(binary.readUInt16LE(18), target.machine, 'ELF architecture does not match the manifest')
}

const staging = mkdtempSync(join(tmpdir(), 'port-forwarding-fpk-'))
try {
  run('npm', ['--prefix', 'frontend', 'run', 'build'])
  const packages = targets.map(target => {
    const work = join(staging, target.arch)
    mkdirSync(work)
    return pack(target, work)
  })
  mkdirSync(output, { recursive: true })
  for (const { path, file } of packages) {
    const temporary = join(output, '.' + file + '.' + process.pid)
    try {
      copyFileSync(path, temporary)
      renameSync(temporary, join(output, file))
    } finally {
      rmSync(temporary, { force: true })
    }
    console.log('Verified ' + join(output, file))
  }
} finally {
  rmSync(staging, { recursive: true, force: true })
}

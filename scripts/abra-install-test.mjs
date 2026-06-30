#!/usr/bin/env node
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, symlinkSync, writeFileSync } from 'node:fs';
import { access, chmod, mkdtemp, rm } from 'node:fs/promises';
import { constants } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, '..');
const installScript = join(repoRoot, 'scripts', 'install.sh');
const assetName = 'abra_linux_amd64.tar.gz';

const requiredTools = [
  'awk',
  'cat',
  'chmod',
  'cp',
  'find',
  'gzip',
  'head',
  'mkdir',
  'mktemp',
  'rm',
  'sed',
  'tar',
];

function findTool(name) {
  const candidates = [
    `/bin/${name}`,
    `/usr/bin/${name}`,
    `/usr/local/bin/${name}`,
    `/opt/homebrew/bin/${name}`,
  ];
  for (const candidate of candidates) {
    if (existsSync(candidate)) return candidate;
  }
  throw new Error(`required test tool not found: ${name}`);
}

function sha256(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex');
}

function shQuote(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}

async function writeExecutable(path, content) {
  writeFileSync(path, content, { mode: 0o755 });
  await chmod(path, 0o755);
}

async function makeTools(dir, options = {}) {
  mkdirSync(dir, { recursive: true });
  for (const tool of requiredTools) {
    symlinkSync(findTool(tool), join(dir, tool));
  }
  await writeExecutable(
    join(dir, 'uname'),
    `#!/bin/sh
case "$1" in
  -s) printf '%s\\n' "\${ABRA_FAKE_UNAME_S:-Linux}" ;;
  -m) printf '%s\\n' "\${ABRA_FAKE_UNAME_M:-x86_64}" ;;
  *) printf '%s\\n' "\${ABRA_FAKE_UNAME_S:-Linux}" ;;
esac
`,
  );
  if (options.curl !== 'missing') {
    await writeExecutable(
      join(dir, 'curl'),
      `#!/bin/sh
set -eu
out=''
url=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      shift
      out="$1"
      ;;
    -*)
      ;;
    *)
      url="$1"
      ;;
  esac
  shift || true
done
name="\${url##*/}"
if [ -n "\${ABRA_FAKE_CURL_LOG:-}" ]; then
  printf '%s\\n' "$url" >> "$ABRA_FAKE_CURL_LOG"
fi
src="$ABRA_FAKE_RELEASE_DIR/$name"
if [ ! -f "$src" ]; then
  printf 'missing %s\\n' "$name" >&2
  exit 22
fi
if [ -n "$out" ]; then
  cp "$src" "$out"
else
  cat "$src"
fi
`,
    );
  }
  if (options.tar === 'missing') {
    await rm(join(dir, 'tar'), { force: true });
  }
  if (options.checksumTool !== false) {
    await writeExecutable(
      join(dir, 'sha256sum'),
      `#!/bin/sh
${shQuote(process.execPath)} -e 'const fs = require("fs"); const crypto = require("crypto"); for (const file of process.argv.slice(1)) { const hash = crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex"); console.log(hash + "  " + file); }' "$@"
`,
    );
  }
  if (options.gh !== 'missing') {
    await writeExecutable(
      join(dir, 'gh'),
      `#!/bin/sh
set -eu
if [ "\${ABRA_FAKE_GH_FAIL:-0}" = "1" ]; then
  exit 1
fi
last=''
for arg in "$@"; do
  last="$arg"
done
printf '%s\\n' "\${last##*/}" >> "$ABRA_FAKE_GH_LOG"
exit 0
`,
    );
  }
  if (options.existingAbra === true) {
    await writeExecutable(
      join(dir, 'abra'),
      `#!/bin/sh
printf '%s\\n' 'abra old-test-binary'
`,
    );
  }
}

async function makeArchive(releaseDir, options = {}) {
  const archive = join(releaseDir, assetName);
  if (options.asset === false) return archive;
  if (options.archive === 'invalid') {
    writeFileSync(archive, 'not a gzip archive\n');
    return archive;
  }

  const payload = await mkdtemp(join(releaseDir, 'payload-'));
  const abraPath = join(payload, 'abra');
  writeFileSync(abraPath, '#!/bin/sh\nprintf "abra test binary\\n"\n');
  await chmod(abraPath, options.archive === 'noExecutable' ? 0o644 : 0o755);
  execFileSync(findTool('tar'), ['-czf', archive, '-C', payload, 'abra']);
  return archive;
}

async function makeRelease(root, options = {}) {
  const releaseDir = join(root, 'release');
  mkdirSync(releaseDir, { recursive: true });
  const archive = await makeArchive(releaseDir, options);
  if (options.asset !== false && options.sums !== false) {
    const checksum = options.checksum === 'mismatch' ? '0'.repeat(64) : sha256(archive);
    const entryName = options.checksum === 'missingEntry' ? 'other_asset.tar.gz' : assetName;
    writeFileSync(join(releaseDir, 'SHA256SUMS'), `${checksum}  ${entryName}\n`);
  }
  return releaseDir;
}

async function runInstaller(options = {}) {
  const root = await mkdtemp(join(tmpdir(), 'abra-install-test-'));
  const installDir = join(root, 'bin');
  const toolsDir = join(root, 'tools');
  const ghLog = join(root, 'gh.log');
  const curlLog = join(root, 'curl.log');
  const releaseDir = await makeRelease(root, options);
  await makeTools(toolsDir, options);

  const env = {
    ABRA_ALLOW_SOURCE_BUILD: '0',
    ABRA_FAKE_GH_FAIL: options.gh === 'fail' ? '1' : '0',
    ABRA_FAKE_GH_LOG: ghLog,
    ABRA_FAKE_CURL_LOG: curlLog,
    ABRA_FAKE_RELEASE_DIR: releaseDir,
    ABRA_INSTALL_DIR: installDir,
    ABRA_RELEASE_BASE_URL: options.releaseBaseURL ?? `file://${releaseDir}`,
    ABRA_REPO: 'hermawan22/abra',
    ABRA_VERIFY_ATTESTATION: options.attestation ?? '1',
    ABRA_FAKE_UNAME_M: options.unameM ?? 'x86_64',
    ABRA_FAKE_UNAME_S: options.unameS ?? 'Linux',
    HOME: join(root, 'home'),
    PATH: toolsDir,
    TMPDIR: tmpdir(),
  };
  mkdirSync(env.HOME, { recursive: true });

  const result = spawnSync('/bin/sh', [installScript], {
    cwd: repoRoot,
    encoding: 'utf8',
    env,
  });
  return {
    ...result,
    root,
    installDir,
    installedBinary: join(installDir, 'abra'),
    ghLog,
    curlLog,
  };
}

async function assertInstalled(result) {
  assert.equal(result.status, 0, result.stderr || result.stdout);
  await access(result.installedBinary, constants.X_OK);
}

function assertFailedClosed(result, message) {
  assert.notEqual(result.status, 0, 'installer unexpectedly succeeded');
  assert.match(result.stderr, message);
  assert.equal(existsSync(result.installedBinary), false, 'installer left an abra binary after failure');
}

async function test(name, fn) {
  try {
    await fn();
    console.log(`ok ${name}`);
  } catch (error) {
    console.error(`not ok ${name}`);
    console.error(error?.stack || error);
    process.exitCode = 1;
  }
}

await test('installs verified release and requires both attestations', async () => {
  const result = await runInstaller({ attestation: '1' });
  await assertInstalled(result);
  const attestations = readFileSync(result.ghLog, 'utf8').trim().split('\n');
  assert.deepEqual(attestations, [assetName, 'SHA256SUMS']);
});

await test('fails closed when SHA256SUMS is missing', async () => {
  const result = await runInstaller({ sums: false, attestation: '0' });
  assertFailedClosed(result, /missing SHA256SUMS/);
});

await test('fails closed when checksum mismatches', async () => {
  const result = await runInstaller({ checksum: 'mismatch', attestation: '0' });
  assertFailedClosed(result, /checksum mismatch/);
});

await test('fails closed when checksum entry is absent', async () => {
  const result = await runInstaller({ checksum: 'missingEntry', attestation: '0' });
  assertFailedClosed(result, /SHA256SUMS does not include/);
});

await test('fails closed when checksum tooling is missing', async () => {
  const result = await runInstaller({ checksumTool: false, attestation: '0' });
  assertFailedClosed(result, /missing checksum tool/);
});

await test('fails closed when required attestation lacks gh', async () => {
  const result = await runInstaller({ gh: 'missing', attestation: '1' });
  assertFailedClosed(result, /missing GitHub CLI/);
});

await test('auto attestation warning continues after checksum when gh verification fails', async () => {
  const result = await runInstaller({ gh: 'fail', attestation: 'auto' });
  await assertInstalled(result);
  assert.match(result.stderr, /GitHub artifact attestation verification failed/);
  assert.match(result.stderr, /Checksum verification passed; continuing because ABRA_VERIFY_ATTESTATION=auto/);
});

await test('required attestation fails closed when gh verification fails', async () => {
  const result = await runInstaller({ gh: 'fail', attestation: '1' });
  assertFailedClosed(result, /GitHub artifact attestation verification failed/);
});

await test('fails closed for invalid archive', async () => {
  const result = await runInstaller({ archive: 'invalid', attestation: '0' });
  assertFailedClosed(result, /failed to extract verified release archive/);
});

await test('fails closed when archive has no executable abra', async () => {
  const result = await runInstaller({ archive: 'noExecutable', attestation: '0' });
  assertFailedClosed(result, /did not contain an executable abra binary/);
});

await test('fails closed when platform asset is missing and source build is disabled', async () => {
  const result = await runInstaller({ asset: false, attestation: '0' });
  assertFailedClosed(result, /Source builds are disabled by default/);
});

await test('fails before install on unsupported OS', async () => {
  const result = await runInstaller({ attestation: '0', unameS: 'Plan9' });
  assertFailedClosed(result, /unsupported OS/);
});

await test('fails before install on unsupported architecture', async () => {
  const result = await runInstaller({ attestation: '0', unameM: 'sparc' });
  assertFailedClosed(result, /unsupported architecture/);
});

await test('fails before install when curl is missing', async () => {
  const result = await runInstaller({ curl: 'missing', attestation: '0' });
  assertFailedClosed(result, /missing required command: curl/);
});

await test('fails before install when tar is missing', async () => {
  const result = await runInstaller({ tar: 'missing', attestation: '0' });
  assertFailedClosed(result, /missing required command: tar/);
});

await test('fails closed for invalid attestation mode', async () => {
  const result = await runInstaller({ attestation: 'maybe' });
  assertFailedClosed(result, /invalid ABRA_VERIFY_ATTESTATION/);
});

await test('trims trailing slash from custom release base URL', async () => {
  const root = await mkdtemp(join(tmpdir(), 'abra-install-base-url-'));
  const releaseDir = await makeRelease(root, {});
  const result = await runInstaller({
    attestation: '0',
    releaseBaseURL: `file://${releaseDir}/`,
  });
  await assertInstalled(result);
  const urls = readFileSync(result.curlLog, 'utf8').trim().split('\n');
  assert.equal(urls[0], `file://${releaseDir}/${assetName}`);
  assert.equal(urls[1], `file://${releaseDir}/SHA256SUMS`);
});

await test('prints direct next command when install dir is not on PATH', async () => {
  const result = await runInstaller({ attestation: '0' });
  await assertInstalled(result);
  assert.match(result.stderr, /Add this to PATH if needed: export PATH=/);
  assert.match(result.stderr, /Next: .*\/bin\/abra setup/);
});

await test('warns when PATH resolves a different abra binary after install', async () => {
  const result = await runInstaller({ attestation: '0', existingAbra: true });
  await assertInstalled(result);
  assert.match(result.stderr, /Warning: PATH resolves 'abra' to /);
  assert.match(result.stderr, /not .*\/bin\/abra/);
  assert.match(result.stderr, /export PATH=/);
  assert.match(result.stderr, /Next: .*\/bin\/abra setup/);
});

if (process.exitCode) {
  process.exit(process.exitCode);
}

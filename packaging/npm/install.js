#!/usr/bin/env node
// Downloads the whatsapp-connect-mcp release binary matching this
// package's own version and this machine's platform/arch, then
// re-executes it with the arguments this process was invoked with. This
// file is the npm "bin" entry point itself (no separate postinstall
// download): the binary is fetched lazily, on first run, and reused on
// every run after that. No dependencies beyond Node's standard library.
//
// Asset naming convention — kept in sync with .github/workflows/release.yml,
// scripts/install.sh, and scripts/install.ps1:
//   whatsapp-connect-mcp_<version>_<os>_<arch>[.exe]
// <version> is this package's package.json "version" with no "v" prefix;
// the GitHub release tag is "v" + that version.

'use strict';

const fs = require('fs');
const https = require('https');
const os = require('os');
const path = require('path');
const { spawnSync } = require('child_process');

const REPO = 'idle-sync/whatsapp-connect-mcp';
const MAX_REDIRECTS = 5;

const OS_MAP = { darwin: 'darwin', linux: 'linux', win32: 'windows' };
const ARCH_MAP = { x64: 'amd64', arm64: 'arm64' };

function assetName(version, platform, arch) {
  const ext = platform === 'windows' ? '.exe' : '';
  return `whatsapp-connect-mcp_${version}_${platform}_${arch}${ext}`;
}

// cacheDir lives inside this installed npm package, so a global or local
// install caches the binary once and every subsequent invocation
// (including repeat `npx` calls once npm's own npx cache is warm) reuses
// it without a network round trip.
function cacheDir() {
  return path.join(__dirname, '.bin');
}

// fetchToFile GETs url and writes the response body to tmpPath, following
// up to redirectsLeft redirects (GitHub Releases assets redirect to
// objects.githubusercontent.com). It recurses per redirect hop but writes
// the final 200 response exactly once — callers must not call it more than
// once per download, or later hops would overwrite tmpPath concurrently.
function fetchToFile(url, tmpPath, redirectsLeft) {
  return new Promise((resolve, reject) => {
    if (redirectsLeft < 0) {
      reject(new Error('too many redirects downloading ' + url));
      return;
    }
    const req = https.get(url, { headers: { 'User-Agent': 'whatsapp-connect-mcp-npm-installer' } }, (res) => {
      const status = res.statusCode || 0;
      if (status >= 300 && status < 400 && res.headers.location) {
        res.resume();
        const next = new URL(res.headers.location, url).toString();
        fetchToFile(next, tmpPath, redirectsLeft - 1).then(resolve, reject);
        return;
      }
      if (status !== 200) {
        res.resume();
        reject(new Error(`download failed: HTTP ${status} for ${url}`));
        return;
      }

      const file = fs.createWriteStream(tmpPath, { mode: 0o755 });
      res.pipe(file);
      file.on('finish', () => {
        file.close((err) => (err ? reject(err) : resolve()));
      });
      file.on('error', reject);
    });
    req.on('error', reject);
  });
}

// downloadFile fetches url to destPath via a sibling ".download" temp file,
// renaming into place only once the full body has landed, so a failed or
// interrupted download never leaves a partial file at destPath.
async function downloadFile(url, destPath, maxRedirects) {
  const tmpPath = destPath + '.download';
  try {
    await fetchToFile(url, tmpPath, maxRedirects);
    fs.renameSync(tmpPath, destPath);
  } catch (err) {
    try {
      fs.unlinkSync(tmpPath);
    } catch {
      // nothing to clean up
    }
    throw err;
  }
}

// ensureBinary returns the local path to this platform's release binary,
// downloading it first if it is not already cached.
async function ensureBinary() {
  const platform = OS_MAP[os.platform()];
  const arch = ARCH_MAP[os.arch()];
  if (!platform || !arch) {
    throw new Error(
      `unsupported platform/arch: ${os.platform()}/${os.arch()} — see release assets at ` +
        `https://github.com/${REPO}/releases for what is built`
    );
  }

  const pkg = require('./package.json');
  const version = process.env.WHATSAPP_CONNECT_MCP_VERSION || pkg.version;
  const asset = assetName(version, platform, arch);
  const destPath = path.join(cacheDir(), asset);

  if (!fs.existsSync(destPath)) {
    fs.mkdirSync(cacheDir(), { recursive: true });
    const url = `https://github.com/${REPO}/releases/download/v${version}/${asset}`;
    process.stderr.write(`whatsapp-connect-mcp: downloading ${asset}...\n`);
    await downloadFile(url, destPath, MAX_REDIRECTS);
  }

  if (platform !== 'windows') {
    fs.chmodSync(destPath, 0o755);
  }
  fs.accessSync(destPath, fs.constants.X_OK); // throws if not executable/present

  return destPath;
}

async function main() {
  const binPath = await ensureBinary();
  const result = spawnSync(binPath, process.argv.slice(2), { stdio: 'inherit' });
  if (result.error) {
    throw new Error(`could not run ${binPath}: ${result.error.message}`);
  }
  process.exit(result.signal ? 1 : (result.status === null ? 1 : result.status));
}

main().catch((err) => {
  process.stderr.write('whatsapp-connect-mcp: ' + err.message + '\n');
  process.exit(1);
});

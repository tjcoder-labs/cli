#!/usr/bin/env node
// fetch-binary.js - download the platform-appropriate `coder` binary from
// the project's GitHub Releases page during `npm install`.
//
// This is invoked by the package.json `postinstall` script. It is
// idempotent: if the binary is already present for the current
// platform/arch, it does nothing.

const fs = require("node:fs");
const path = require("node:path");
const https = require("node:https");
const { URL } = require("node:url");

// --- configuration ---------------------------------------------------------

const pkg = require("../package.json");
const REPO = process.env.CODER_CLI_REPO || "tcoder915/ergo-cli-go";
// Allow pinning a version (matches the @tj/coder-cli package version by
// default so that `npm i @tj/coder-cli@0.1.4` installs coder v0.1.4).
const VERSION = process.env.CODER_CLI_VERSION || `v${pkg.version}`;

// --- platform detection ----------------------------------------------------

function detect() {
  // Map Node's platform/arch names to the suffixes we use in release assets.
  const platformMap = {
    darwin: "darwin",
    linux: "linux",
    win32: "windows",
    freebsd: "freebsd",
  };
  const archMap = {
    x64: "amd64",
    arm64: "arm64",
    x32: "386",
  };

  const os = platformMap[process.platform];
  const arch = archMap[process.arch];

  if (!os) {
    throw new Error(
      `unsupported platform: ${process.platform} ` +
        `(coder publishes darwin, linux, windows, freebsd)`,
    );
  }
  if (!arch) {
    throw new Error(
      `unsupported architecture: ${process.arch} ` +
        `(coder publishes amd64, arm64, 386)`,
    );
  }

  const ext = os === "windows" ? ".zip" : ".tar.gz";
  const asset = `coder_${VERSION}_${os}_${arch}${ext}`;
  return { os, arch, ext, asset };
}

// --- download --------------------------------------------------------------

function download(url, dest) {
  return new Promise((resolve, reject) => {
    const req = https.get(
      url,
      { headers: { "User-Agent": `${pkg.name}/${pkg.version}` } },
      (res) => {
        // GitHub returns 301/302 to the S3-backed download URL. Follow.
        if (
          res.statusCode &&
          [301, 302, 303, 307, 308].includes(res.statusCode) &&
          res.headers.location
        ) {
          res.resume();
          download(new URL(res.headers.location, url).toString(), dest)
            .then(resolve)
            .catch(reject);
          return;
        }
        if (res.statusCode !== 200) {
          reject(
            new Error(
              `download failed: ${res.statusCode} ${res.statusMessage} for ${url}`,
            ),
          );
          return;
        }
        const out = fs.createWriteStream(dest);
        res.pipe(out);
        out.on("finish", () => out.close(() => resolve(dest)));
        out.on("error", reject);
      },
    );
    req.on("error", reject);
    req.setTimeout(60_000, () => {
      req.destroy(new Error(`download timeout after 60s: ${url}`));
    });
  });
}

function extract(archivePath, workDir, info) {
  const { execFileSync } = require("node:child_process");
  const binName = info.os === "windows" ? "coder.exe" : "coder";
  if (info.ext === ".tar.gz") {
    execFileSync("tar", ["-xzf", archivePath, "-C", workDir], {
      stdio: "ignore",
    });
  } else if (info.ext === ".zip") {
    // `tar` on Windows 10+ and modern macOS handles .zip too; fall back to
    // a system unzip if not.
    try {
      execFileSync("tar", ["-xf", archivePath, "-C", workDir], {
        stdio: "ignore",
      });
    } catch {
      execFileSync("unzip", ["-q", archivePath, "-d", workDir], {
        stdio: "ignore",
      });
    }
  } else {
    throw new Error(`unknown archive extension: ${info.ext}`);
  }
  // The archive contains a top-level `coder` (or `coder.exe`) binary.
  const extracted = path.join(workDir, binName);
  if (!fs.existsSync(extracted)) {
    // Some releases nest the binary in a subdirectory; try one level.
    const nested = path.join(workDir, `coder_${VERSION}_${info.os}_${info.arch}`, binName);
    if (fs.existsSync(nested)) return nested;
    throw new Error(
      `expected binary not found in archive: ${extracted} (and ${nested})`,
    );
  }
  return extracted;
}

async function checksum(filePath) {
  // Optional SHA256 verification against a sibling .sha256 file. We try, but
  // never fail the install if the file is missing — that keeps dev installs
  // working before the first signed release.
  const fs = require("node:fs/promises");
  const crypto = require("node:crypto");
  const shaPath = `${filePath}.sha256`;
  if (!fs.existsSync(shaPath)) return;
  const expected = (await fs.readFile(shaPath, "utf8")).trim().split(/\s+/)[0];
  const actual = crypto
    .createHash("sha256")
    .update(await fs.readFile(filePath))
    .digest("hex");
  if (expected.toLowerCase() !== actual.toLowerCase()) {
    throw new Error(
      `sha256 mismatch: expected ${expected}, got ${actual} for ${filePath}`,
    );
  }
}

// --- main ------------------------------------------------------------------

async function main() {
  const info = detect();
  const libDir = path.join(__dirname);
  const binName = info.os === "windows" ? "coder.exe" : "coder";
  const binPath = path.join(libDir, binName);

  if (fs.existsSync(binPath)) {
    // Already installed (e.g. `npm rebuild`); leave the existing binary
    // alone so we don't clobber a working install with a broken download.
    return;
  }

  const url = `https://github.com/${REPO}/releases/download/${VERSION}/${info.asset}`;
  process.stdout.write(`coder: downloading ${info.asset} ...\n`);

  const tmp = fs.mkdtempSync(path.join(require("node:os").tmpdir(), "coder-"));
  const archivePath = path.join(tmp, info.asset);
  try {
    await download(url, archivePath);
    const extracted = extract(archivePath, tmp, info);
    if (info.os !== "windows") {
      fs.chmodSync(extracted, 0o755);
    }
    await checksum(extracted);
    fs.renameSync(extracted, binPath);
    process.stdout.write(`coder: installed ${binPath}\n`);
  } catch (err) {
    process.stderr.write(
      `coder: ${err.message}\n` +
        `coder: install via the Go-based installer instead:\n` +
        `coder:   curl -fsSL https://raw.githubusercontent.com/${REPO}/main/install.sh | bash\n`,
    );
    // Don't fail the install for an optional postinstall: leave the JS shim
    // in place so users can still see the error when they run `coder`.
    // Set CODER_CLI_REQUIRE_DOWNLOAD=1 to make this fatal.
    if (process.env.CODER_CLI_REQUIRE_DOWNLOAD === "1") process.exit(1);
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

main().catch((err) => {
  process.stderr.write(`coder: unexpected error: ${err.stack || err}\n`);
  process.exit(1);
});

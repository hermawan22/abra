#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const expected = ["LICENSE", "README.md", "package.json"];
const cacheDir = mkdtempSync(join(tmpdir(), "abra-npm-pack-"));

try {
  const result = spawnSync("npm", ["pack", "--dry-run", "--json", "--cache", cacheDir], {
    encoding: "utf8",
  });
  if (result.status !== 0) {
    process.stderr.write(result.stderr || result.stdout);
    process.exit(result.status || 1);
  }
  const payload = JSON.parse(result.stdout);
  const files = [...(payload[0]?.files || []).map((file) => file.path)].sort();
  const want = [...expected].sort();
  if (JSON.stringify(files) !== JSON.stringify(want)) {
    console.error("npm pack file list does not match the OSS-safe allowlist");
    console.error("expected:", want.join(", "));
    console.error("actual:  ", files.join(", "));
    process.exit(1);
  }
  console.log(`ok npm pack allowlist: ${files.join(", ")}`);
} finally {
  rmSync(cacheDir, { recursive: true, force: true });
}

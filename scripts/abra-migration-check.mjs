#!/usr/bin/env node

import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const dir = "migrations";
const files = readdirSync(dir).filter((name) => name.endsWith(".sql")).sort();
const failures = [];
const seen = new Set();

if (files.length === 0) {
  failures.push("migrations: no .sql files found");
}

files.forEach((file, index) => {
  const match = file.match(/^(\d{3})_[a-z0-9]+(?:_[a-z0-9]+)*\.sql$/);
  if (!match) {
    failures.push(`${file}: expected NNN_lowercase_slug.sql`);
    return;
  }
  const number = Number(match[1]);
  const expected = index + 1;
  if (number !== expected) {
    failures.push(`${file}: sequence number ${number} should be ${expected}`);
  }
  if (seen.has(number)) {
    failures.push(`${file}: duplicate migration number ${number}`);
  }
  seen.add(number);

  const content = readFileSync(join(dir, file), "utf8");
  const firstLine = content.split(/\r?\n/, 1)[0] || "";
  if (!firstLine.startsWith(`-- Migration ${match[1]}:`)) {
    failures.push(`${file}: first line must start with "-- Migration ${match[1]}:"`);
  }
  if (!/\S/.test(content)) {
    failures.push(`${file}: migration is empty`);
  }
});

if (failures.length > 0) {
  console.error("Migration check failed:");
  for (const failure of failures) {
    console.error(`- ${failure}`);
  }
  process.exit(1);
}

console.log(`Migration check passed (${files.length} files).`);

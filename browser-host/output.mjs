import { createHash, randomBytes } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

const OWNER_BYTES = 64 << 20;

// The daemon's encoder writes <, >, & and U+2028/U+2029 as six-byte escapes
// where JSON.stringify keeps one character, so each is counted as six.
export function encodedBytes(text) {
  return Buffer.byteLength(JSON.stringify(text)) + 5 * (text.match(/[<>&\u2028\u2029]/g)?.length ?? 0);
}

export function excerpt(text, budget) {
  if (encodedBytes(text) <= budget) return { text, truncated: false };
  let kept = '';
  let used = 2;
  for (const line of text.split('\n')) {
    const cost = encodedBytes(line + '\n') - 2;
    if (used + cost > budget) break;
    kept += line + '\n';
    used += cost;
  }
  return { text: kept, truncated: true };
}

export class Output {
  constructor(root, ownerBytes = OWNER_BYTES) {
    this.root = root;
    this.ownerBytes = ownerBytes;
  }

  clear() {
    fs.rmSync(this.root, { recursive: true, force: true });
  }

  dir(owner) {
    return path.join(this.root, createHash('sha256').update(owner).digest('hex').slice(0, 32));
  }

  write(owner, stem, extension, data) {
    const dir = this.dir(owner);
    fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
    const file = path.join(dir, `${stem}-${Date.now()}-${randomBytes(3).toString('hex')}.${extension}`);
    fs.writeFileSync(file, data, { mode: 0o600 });
    this.evict(dir, file);
    return file;
  }

  evict(dir, keep) {
    const files = fs.readdirSync(dir).map(name => {
      const file = path.join(dir, name);
      const stat = fs.statSync(file);
      return { file, size: stat.size, mtime: stat.mtimeMs };
    }).sort((a, b) => a.mtime - b.mtime);
    let total = files.reduce((sum, entry) => sum + entry.size, 0);
    for (const entry of files) {
      if (total <= this.ownerBytes) break;
      if (entry.file === keep) continue;
      fs.rmSync(entry.file, { force: true });
      total -= entry.size;
    }
  }

  removeOwner(owner) {
    fs.rmSync(this.dir(owner), { recursive: true, force: true });
  }
}

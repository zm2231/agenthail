// Every Node test entry file imports this first, so neither it nor anything it
// starts touches the person's own Agenthail, Claude or Codex state.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const home = fs.mkdtempSync(path.join(os.tmpdir(), 'agenthail-test-home-'));
process.env.HOME = home;
process.env.CFFIXED_USER_HOME = home;
for (const name of Object.keys(process.env)) {
  if (['CODEX_HOME', 'CLAUDE_CONFIG_DIR', 'CLAUDECODE', 'AGENTHAIL_SESSION_ID', 'CODEX_THREAD_ID'].includes(name)
    || name.startsWith('CLAUDE_CODE_') || /^XDG_(CONFIG|CACHE|DATA|STATE)_HOME$/.test(name)) {
    delete process.env[name];
  }
}
const removeHome = () => fs.rmSync(home, { recursive: true, force: true });
process.on('exit', removeHome);
for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
  process.once(signal, () => {
    removeHome();
    process.kill(process.pid, signal);
  });
}

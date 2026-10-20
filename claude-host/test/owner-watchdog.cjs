// Preloaded into every host and fake Claude a test starts. They run detached
// from the test, so its cleanup cannot reach them once the test process is
// killed or times out; this exits them when any process in
// AGENTHAIL_TEST_OWNER_PIDS is gone.
'use strict';

const owners = (process.env.AGENTHAIL_TEST_OWNER_PIDS || '')
  .split(',')
  .map(Number)
  .filter((pid) => Number.isInteger(pid) && pid > 1);

function alive(pid) {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return error.code === 'EPERM';
  }
}

if (owners.length > 0) {
  setInterval(() => {
    if (!owners.every(alive)) process.exit(1);
  }, 500).unref();
}

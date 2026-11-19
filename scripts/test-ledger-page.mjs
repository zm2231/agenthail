import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const source = readFileSync(new URL("../internal/daemon/ledger_page/ledger-text.js", import.meta.url), "utf8");
const { withoutRefs, shownRows, shortList } = await import("data:text/javascript," + encodeURIComponent(source));

assert.equal(withoutRefs("done: #1009 (54ee919f1)"), "", "a note that is only refs says nothing");
assert.equal(withoutRefs("merged #1009 (54ee919f1)"), "", "a status word left alone says nothing");
assert.equal(withoutRefs("Merged in #1009 (54ee919f1), the tiles keep their order"), "Merged, the tiles keep their order");
assert.equal(withoutRefs("Fixed in PR #1086 and 6c63b4554"), "");
assert.equal(withoutRefs("Superseded by #1070; the list drops ids"), "Superseded; the list drops ids");
assert.equal(withoutRefs("Keep the tile order in the daemon (#1086)"), "Keep the tile order in the daemon");
assert.equal(withoutRefs("Shipped: the reconnect test passes"), "the reconnect test passes");
assert.equal(withoutRefs("Retry after 2 KiB, decade, facade and the 1234567 bytes"), "Retry after 2 KiB, decade, facade and the 1234567 bytes", "plain words and numbers are not refs");
assert.equal(withoutRefs(""), "");
assert.equal(withoutRefs(undefined), undefined);

const rows = Array.from({ length: 14 }, (_, n) => n);
assert.equal(shortList, 5);
assert.deepEqual(shownRows(rows, false), [0, 1, 2, 3, 4], "a section shows its first five");
assert.equal(shownRows(rows, true).length, 14, "and all of them once asked");
assert.deepEqual(shownRows([1, 2], false), [1, 2]);
console.log("ledger page text ok");

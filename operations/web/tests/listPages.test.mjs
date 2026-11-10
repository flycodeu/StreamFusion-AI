import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/listPages.ts", import.meta.url), "utf8");
function helper(request) {
  const exports = {};
  new Function("require", "exports", ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText)(() => ({ request }), exports);
  return exports.requestAllPages;
}
test("complete list reads go beyond the former cap with a fixed page bound and propagate cancellation", async () => {
  const calls = [], rows = Array.from({ length: 1005 }, (_, id) => ({ id: String(id) }));
  const signal = new AbortController().signal;
  const read = helper(async (path, method, data, passedSignal) => {
    calls.push(path); assert.equal(passedSignal, signal);
    const query = new URL(path, "http://test").searchParams, page = Number(query.get("page"));
    assert.equal(query.get("hostId"), "host");
    return { items: rows.slice((page - 1) * 100, page * 100), total: page === 1 ? rows.length : rows.length + 100 };
  });
  assert.equal((await read("/software-instances?hostId=host", signal)).length, 1005);
  assert.equal(calls.length, 11);
});
test("a later page failure rejects the complete refresh rather than replacing data with a partial list", async () => {
  let calls = 0;
  const read = helper(async () => { if (++calls > 1) throw new Error("network interrupted"); return { items: [{ id: "one" }], total: 201 }; });
  await assert.rejects(read("/plugin-installations"), /network interrupted/);
  assert.equal(calls, 2);
});

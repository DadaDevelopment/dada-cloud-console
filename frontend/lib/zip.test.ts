import assert from "node:assert/strict";
import test from "node:test";
import { buildZip, crc32, isExcludedPath } from "./zip.ts";

const utf8 = (s: string) => new TextEncoder().encode(s);

test("crc32 matches the standard check vector", () => {
  assert.equal(crc32(utf8("123456789")), 0xcbf43926);
});

test("crc32 of empty data is 0", () => {
  assert.equal(crc32(new Uint8Array(0)), 0);
});

test("buildZip refuses an empty entry list", () => {
  assert.throws(() => buildZip([]), /no files to package/);
});

test("buildZip produces local file header, central directory and EOCD signatures", () => {
  const zip = buildZip([
    { path: "package.json", data: utf8('{"name":"x"}') },
    { path: "src/main.tsx", data: utf8("export default 1;") },
  ]);

  assert.equal(zip[0], 0x50);
  assert.equal(zip[1], 0x4b);
  assert.equal(zip[2], 0x03);
  assert.equal(zip[3], 0x04);

  const centralSig = [0x50, 0x4b, 0x01, 0x02];
  const centralOffset = findSequence(zip, centralSig);
  assert.ok(centralOffset >= 0, "central directory signature not found");

  const eocdSig = [0x50, 0x4b, 0x05, 0x06];
  const eocdOffset = findSequence(zip, eocdSig);
  assert.ok(eocdOffset >= 0, "EOCD signature not found");
  assert.equal(eocdOffset, zip.length - 22, "EOCD must be the last 22 bytes (no comment)");

  const view = new DataView(zip.buffer, zip.byteOffset, zip.byteLength);
  const entryCount = view.getUint16(eocdOffset + 10, true);
  assert.equal(entryCount, 2);
});

test("buildZip handles an empty file with CRC 0", () => {
  const zip = buildZip([{ path: "empty.txt", data: new Uint8Array(0) }]);
  const view = new DataView(zip.buffer, zip.byteOffset, zip.byteLength);
  assert.equal(view.getUint32(14, true), 0, "CRC-32 of empty entry must be 0");
  assert.equal(view.getUint32(18, true), 0, "compressed size of empty entry must be 0");
});

test("buildZip stores UTF-8 names with the UTF-8 flag bit set", () => {
  const name = "papka/файл.txt";
  const zip = buildZip([{ path: name, data: utf8("hi") }]);
  const view = new DataView(zip.buffer, zip.byteOffset, zip.byteLength);

  const flags = view.getUint16(6, true);
  assert.equal(flags & 0x0800, 0x0800, "UTF-8 language encoding flag must be set");

  const nameLen = view.getUint16(26, true);
  const nameBytes = zip.slice(30, 30 + nameLen);
  assert.equal(new TextDecoder().decode(nameBytes), name);
});

test("buildZip strips a leading ./ and normalizes backslashes", () => {
  const zip = buildZip([{ path: ".\\myapp\\package.json", data: utf8("{}") }]);
  const view = new DataView(zip.buffer, zip.byteOffset, zip.byteLength);
  const nameLen = view.getUint16(26, true);
  const nameBytes = zip.slice(30, 30 + nameLen);
  assert.equal(new TextDecoder().decode(nameBytes), "myapp/package.json");
});

test("buildZip is deterministic across repeated calls with the same input", () => {
  const entries = [
    { path: "a.txt", data: utf8("aaa") },
    { path: "b/c.txt", data: utf8("bbb") },
  ];
  const first = buildZip(entries.map((e) => ({ path: e.path, data: e.data.slice() })));
  const second = buildZip(entries.map((e) => ({ path: e.path, data: e.data.slice() })));
  assert.deepEqual(Array.from(first), Array.from(second));
});

test("buildZip rejects more than 65535 entries", () => {
  const entries = Array.from({ length: 65536 }, (_, i) => ({ path: `f${i}.txt`, data: utf8("x") }));
  assert.throws(() => buildZip(entries), /zip64-free limit/);
});

test("isExcludedPath matches CLI's alwaysExcludedDirs set", () => {
  assert.equal(isExcludedPath("node_modules/react/index.js"), true);
  assert.equal(isExcludedPath("myapp/node_modules/react/index.js"), true);
  assert.equal(isExcludedPath("myapp/.git/HEAD"), true);
  assert.equal(isExcludedPath("myapp/dist/bundle.js"), true);
  assert.equal(isExcludedPath("myapp/src/dist-utils.ts"), false);
  assert.equal(isExcludedPath("myapp/package.json"), false);
});

function findSequence(haystack: Uint8Array, needle: number[]): number {
  outer: for (let i = 0; i <= haystack.length - needle.length; i++) {
    for (let j = 0; j < needle.length; j++) {
      if (haystack[i + j] !== needle[j]) continue outer;
    }
    return i;
  }
  return -1;
}

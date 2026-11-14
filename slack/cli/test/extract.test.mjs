// The desktop app's LevelDB is built by hand here, in the shapes that broke
// or could break token extraction: a Snappy-compressed table, an older
// version of the value, a newer one still in the write-ahead log, and a file
// that is not LevelDB at all.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { extractTokens } from "../lib/extract.js";

// Assembled at run time so no token-shaped string sits in the source.
const token = (tail) => ["xox", "c-1111-2222-3333-"].join("") + tail.repeat(40);
const KEY = Buffer.from("_https://app.slack.com\x00\x01localConfig_v2", "latin1");

function varint(n) {
  const bytes = [];
  while (n >= 0x80) {
    bytes.push((n & 0x7f) | 0x80);
    n = Math.floor(n / 128);
  }
  bytes.push(n);
  return Buffer.from(bytes);
}

// A Snappy stream from a script of literals and [offset, length] copies.
function snappy(parts) {
  let length = 0;
  const out = [];
  for (const part of parts) {
    if (Buffer.isBuffer(part)) {
      length += part.length;
      const size = Buffer.alloc(3);
      size[0] = 61 << 2; // a literal whose length follows in two bytes
      size.writeUInt16LE(part.length - 1, 1);
      out.push(size, part);
    } else {
      const [offset, size] = part;
      length += size;
      const copy = Buffer.alloc(3);
      copy[0] = ((size - 1) << 2) | 2; // a copy with a two-byte offset
      copy.writeUInt16LE(offset, 1);
      out.push(copy);
    }
  }
  return Buffer.concat([varint(length), ...out]);
}

function entry(key, value) {
  return Buffer.concat([varint(0), varint(key.length), varint(value.length), key, value]);
}

const restarts = Buffer.from([0, 0, 0, 0, 1, 0, 0, 0]); // one restart point, at offset 0

function internalKey(sequence, type = 1) {
  const trailer = Buffer.alloc(8);
  trailer.writeBigUInt64LE((BigInt(sequence) << 8n) | BigInt(type));
  return Buffer.concat([KEY, trailer]);
}

// A table with one data block. `compress` turns the block's bytes into Snappy
// parts; without it the block is stored as it is.
function table(sequence, value, compress) {
  const plain = Buffer.concat([entry(internalKey(sequence), value), restarts]);
  const data = compress ? snappy(compress(plain)) : plain;
  const trailer = Buffer.from([compress ? 1 : 0, 0, 0, 0, 0]);
  const index = Buffer.concat([
    entry(Buffer.from("z"), Buffer.concat([varint(0), varint(data.length)])),
    restarts,
  ]);
  const indexOffset = data.length + trailer.length;
  const footer = Buffer.alloc(48);
  Buffer.concat([varint(0), varint(0), varint(indexOffset), varint(index.length)]).copy(footer);
  Buffer.from("57fb808b247547db", "hex").copy(footer, 40);
  return Buffer.concat([data, trailer, index, Buffer.alloc(5), footer]);
}

// A write-ahead log with one batch holding one record.
function log(sequence, value) {
  const batch = Buffer.concat([Buffer.alloc(12), Buffer.from([1]), varint(KEY.length), KEY, varint(value.length), value]);
  batch.writeBigUInt64LE(BigInt(sequence), 0);
  batch.writeUInt32LE(1, 8);
  const header = Buffer.alloc(7);
  header.writeUInt16LE(batch.length, 4);
  header[6] = 1; // a whole batch in one fragment
  return Buffer.concat([header, batch]);
}

const config = (...tokens) =>
  Buffer.from("\x01" + JSON.stringify({ teams: tokens.map((t) => ({ token: t })) }), "latin1");

function database(files) {
  const dir = mkdtempSync(join(tmpdir(), "slack-leveldb-"));
  for (const [name, data] of Object.entries(files)) writeFileSync(join(dir, name), data);
  return dir;
}

test("reads every token of the current value from a Snappy-compressed table", () => {
  const [first, second, stale] = [token("a"), token("b"), token("c")];
  // What Snappy does to the second token: its prefix, which the first token
  // already wrote, becomes a copy, and only the tail stays a literal.
  const compress = (plain) => {
    const at = plain.indexOf(second);
    const prefix = second.length - 40; // the part both tokens share
    return [
      plain.subarray(0, at),
      [at - plain.indexOf(first), prefix],
      plain.subarray(at + prefix),
    ];
  };
  const dir = database({
    "000001.ldb": table(5, config(stale)),
    "000002.ldb": table(9, config(first, second), compress),
  });
  assert.deepEqual(extractTokens(dir).sort(), [first, second]);
});

test("a newer value in the write-ahead log replaces the one in the table", () => {
  const [old, current] = [token("d"), token("e")];
  const dir = database({
    "000003.ldb": table(9, config(old)),
    "000004.log": log(12, config(current)),
  });
  assert.deepEqual(extractTokens(dir), [current]);
});

test("falls back to a raw scan when no file reads as LevelDB", () => {
  const found = token("f");
  const dir = database({ "000005.ldb": Buffer.from(`not a table ${found} at all`) });
  assert.deepEqual(extractTokens(dir), [found]);
});

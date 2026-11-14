// A read-only LevelDB reader, just large enough to find the newest value of a
// key in the Slack desktop app's localStorage. It reads the table files
// (.ldb) and the write-ahead logs (.log) straight from disk with Node
// built-ins, so it needs no LevelDB engine, no lock on the database, and no
// Snappy dependency. It works while Slack is running.
//
// Format reference: https://github.com/google/leveldb/tree/main/doc
// (table_format.md, log_format.md) and
// https://github.com/google/snappy/blob/main/format_description.txt

const TABLE_MAGIC = Buffer.from("57fb808b247547db", "hex");
const FOOTER_SIZE = 48;
const BLOCK_TRAILER_SIZE = 5; // 1-byte compression type + 4-byte checksum
const LOG_BLOCK_SIZE = 32768;
const LOG_HEADER_SIZE = 7; // 4-byte checksum, 2-byte length, 1-byte type

const TYPE_DELETION = 0;
const TYPE_VALUE = 1;

function readVarint(buf, pos) {
  let result = 0;
  let shift = 0;
  for (;;) {
    if (pos >= buf.length) throw new Error("varint runs past the end of the buffer");
    const byte = buf[pos++];
    // Multiplication, not <<, so lengths above 2^31 stay positive.
    result += (byte & 0x7f) * 2 ** shift;
    if (byte < 0x80) return [result, pos];
    shift += 7;
    if (shift > 49) throw new Error("varint is too long");
  }
}

// Snappy raw (block) format: a varint with the uncompressed length, then a
// sequence of literals and copies of bytes already written.
export function snappyDecompress(buf) {
  let [length, pos] = readVarint(buf, 0);
  const out = Buffer.alloc(length);
  let written = 0;
  while (pos < buf.length) {
    const tag = buf[pos++];
    const kind = tag & 3;
    if (kind === 0) {
      let size = tag >> 2;
      if (size >= 60) {
        const bytes = size - 59;
        size = buf.readUIntLE(pos, bytes);
        pos += bytes;
      }
      size += 1;
      if (pos + size > buf.length || written + size > length) throw new Error("Snappy literal overruns");
      buf.copy(out, written, pos, pos + size);
      pos += size;
      written += size;
      continue;
    }
    let size;
    let offset;
    if (kind === 1) {
      size = ((tag >> 2) & 7) + 4;
      offset = ((tag >> 5) << 8) | buf[pos];
      pos += 1;
    } else if (kind === 2) {
      size = (tag >> 2) + 1;
      offset = buf.readUInt16LE(pos);
      pos += 2;
    } else {
      size = (tag >> 2) + 1;
      offset = buf.readUInt32LE(pos);
      pos += 4;
    }
    if (offset === 0 || offset > written || written + size > length) throw new Error("Snappy copy overruns");
    // Byte by byte: a copy may overlap the bytes it is writing.
    for (let i = 0; i < size; i++) out[written + i] = out[written + i - offset];
    written += size;
  }
  if (written !== length) throw new Error("Snappy block is shorter than its header says");
  return out;
}

function readBlock(file, offset, size) {
  if (offset + size + BLOCK_TRAILER_SIZE > file.length) throw new Error("block runs past the end of the table");
  const raw = file.subarray(offset, offset + size);
  const compression = file[offset + size];
  if (compression === 0) return raw;
  if (compression === 1) return snappyDecompress(raw);
  throw new Error(`unsupported block compression type ${compression}`);
}

// Yields the [key, value] pairs of one block. Keys share prefixes with the key
// before them; the restart array at the end of the block is not needed for a
// full scan.
function* blockEntries(block) {
  if (block.length < 4) throw new Error("block is too short");
  const restarts = block.readUInt32LE(block.length - 4);
  const end = block.length - 4 - 4 * restarts;
  if (end < 0) throw new Error("block restart array is too long");
  let pos = 0;
  let key = Buffer.alloc(0);
  while (pos < end) {
    let shared;
    let unshared;
    let valueLength;
    [shared, pos] = readVarint(block, pos);
    [unshared, pos] = readVarint(block, pos);
    [valueLength, pos] = readVarint(block, pos);
    if (shared > key.length || pos + unshared + valueLength > end) throw new Error("block entry overruns");
    key = Buffer.concat([key.subarray(0, shared), block.subarray(pos, pos + unshared)]);
    pos += unshared;
    yield [key, block.subarray(pos, pos + valueLength)];
    pos += valueLength;
  }
}

function readHandle(buf, pos) {
  let offset;
  let size;
  [offset, pos] = readVarint(buf, pos);
  [size, pos] = readVarint(buf, pos);
  return [offset, size, pos];
}

// Yields every record of one table file as { key, sequence, value }. A table
// stores each key with an 8-byte trailer: the sequence number of the write,
// which orders versions of the same key, and the record type. A deletion has
// an empty value.
export function* tableRecords(file) {
  if (file.length < FOOTER_SIZE || !file.subarray(file.length - 8).equals(TABLE_MAGIC)) {
    throw new Error("not a LevelDB table");
  }
  const footer = file.subarray(file.length - FOOTER_SIZE);
  const [, , afterMetaindex] = readHandle(footer, 0);
  const [indexOffset, indexSize] = readHandle(footer, afterMetaindex);
  for (const [, handle] of blockEntries(readBlock(file, indexOffset, indexSize))) {
    const [offset, size] = readHandle(handle, 0);
    for (const [internalKey, value] of blockEntries(readBlock(file, offset, size))) {
      if (internalKey.length < 8) throw new Error("table key has no trailer");
      const trailer = internalKey.readBigUInt64LE(internalKey.length - 8);
      yield { key: internalKey.subarray(0, internalKey.length - 8), sequence: trailer >> 8n, value };
    }
  }
}

// Yields every record of one write-ahead log, in the same shape. The log is a
// run of 32 KiB blocks holding fragments of write batches; a batch that is
// cut off, as the last one can be while Slack is writing, ends the scan.
export function* logRecords(file) {
  let batch = [];
  let pos = 0;
  while (pos + LOG_HEADER_SIZE <= file.length) {
    const blockLeft = LOG_BLOCK_SIZE - (pos % LOG_BLOCK_SIZE);
    if (blockLeft < LOG_HEADER_SIZE) {
      pos += blockLeft; // the tail of a block too short for a header is padding
      continue;
    }
    const length = file.readUInt16LE(pos + 4);
    const type = file[pos + 6];
    const start = pos + LOG_HEADER_SIZE;
    if (type === 0 || type > 4 || length > blockLeft - LOG_HEADER_SIZE || start + length > file.length) return;
    pos = start + length;
    // 1 = a whole batch, 2 = its first fragment, 3 = a middle one, 4 = its last.
    if (type === 1 || type === 2) batch = [];
    batch.push(file.subarray(start, start + length));
    if (type === 1 || type === 4) {
      try {
        yield* batchRecords(Buffer.concat(batch));
      } catch {
        return;
      }
      batch = [];
    }
  }
}

// A write batch: an 8-byte sequence number for its first record, a 4-byte
// record count, then the records. Each record takes the next sequence number.
function* batchRecords(batch) {
  if (batch.length < 12) throw new Error("write batch is too short");
  let sequence = batch.readBigUInt64LE(0);
  const count = batch.readUInt32LE(8);
  let pos = 12;
  for (let i = 0; i < count; i++, sequence++) {
    const type = batch[pos++];
    let length;
    [length, pos] = readVarint(batch, pos);
    const key = batch.subarray(pos, pos + length);
    pos += length;
    let value = Buffer.alloc(0);
    if (type === TYPE_VALUE) {
      [length, pos] = readVarint(batch, pos);
      value = batch.subarray(pos, pos + length);
      pos += length;
    } else if (type !== TYPE_DELETION) {
      throw new Error(`unknown write batch record type ${type}`);
    }
    if (pos > batch.length) throw new Error("write batch record overruns");
    yield { key, sequence, value };
  }
}

// The current value of every key that `matches` selects, across the given
// database files: for each key the record with the highest sequence number
// wins, and a key whose newest record is a deletion has an empty value.
// `files` is a list of { name, data }. Files that cannot be read as LevelDB
// are returned in `unreadable` so the caller can decide what to do with them.
export function currentValues(files, matches) {
  const newest = new Map();
  const unreadable = [];
  for (const file of files) {
    try {
      const records = file.name.endsWith(".log") ? logRecords(file.data) : tableRecords(file.data);
      for (const record of records) {
        if (!matches(record.key)) continue;
        const id = record.key.toString("latin1");
        const seen = newest.get(id);
        if (!seen || record.sequence > seen.sequence) newest.set(id, record);
      }
    } catch {
      unreadable.push(file);
    }
  }
  return { values: [...newest.values()].map((r) => r.value), unreadable };
}

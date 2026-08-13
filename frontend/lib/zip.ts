/**
 * Minimal, dependency-free ZIP writer (method STORE, no compression).
 *
 * Built for the source-upload onramp: a user drops a folder or a bare file
 * (Lovable/Bolt/v0 export) and the console packages it client-side into a zip
 * the existing `/source-archive` endpoint already accepts. `backend/internal/
 * sourcedetect/detect.go` reads the archive with Go's stdlib `archive/zip`,
 * which is the format contract this file targets: local file headers,
 * central directory, end-of-central-directory record, general-purpose flag
 * bit 11 for UTF-8 names. No deflate, no zip64 - both are unneeded at the
 * 100MB upload ceiling this flow enforces, and skipping them keeps this file
 * a few hundred lines instead of vendoring a compression library.
 *
 * Every entry is stamped with a fixed DOS timestamp (1980-01-01 00:00:00) so
 * that packaging the same file set twice produces byte-identical output -
 * useful for tests and for not leaking the user's local clock into an
 * uploaded artifact.
 */

const CRC_TABLE = buildCrcTable();

function buildCrcTable(): Uint32Array {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) {
      c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    }
    table[n] = c >>> 0;
  }
  return table;
}

/** CRC-32 (ISO-HDLC / zip variant) of the given bytes. */
export function crc32(data: Uint8Array): number {
  let crc = 0xffffffff;
  for (let i = 0; i < data.length; i++) {
    crc = CRC_TABLE[(crc ^ data[i]) & 0xff] ^ (crc >>> 8);
  }
  return (crc ^ 0xffffffff) >>> 0;
}

class ByteWriter {
  private chunks: Uint8Array[] = [];
  private length = 0;

  push(bytes: Uint8Array): void {
    this.chunks.push(bytes);
    this.length += bytes.length;
  }

  u16(value: number): void {
    const b = new Uint8Array(2);
    new DataView(b.buffer).setUint16(0, value, true);
    this.push(b);
  }

  u32(value: number): void {
    const b = new Uint8Array(4);
    new DataView(b.buffer).setUint32(0, value >>> 0, true);
    this.push(b);
  }

  get size(): number {
    return this.length;
  }

  toUint8Array(): Uint8Array {
    const out = new Uint8Array(this.length);
    let offset = 0;
    for (const chunk of this.chunks) {
      out.set(chunk, offset);
      offset += chunk.length;
    }
    return out;
  }
}

export interface ZipInputEntry {
  /** Forward-slash relative path, e.g. "my-app/package.json". */
  path: string;
  data: Uint8Array;
}

/**
 * ZIP_LOCAL_FILE_SIGNATURE, ZIP_CENTRAL_DIR_SIGNATURE, ZIP_EOCD_SIGNATURE:
 * the three magic numbers backend/internal/sourcedetect/detect.go checks
 * (detectFormat) and Go's archive/zip parses on.
 */
const LOCAL_FILE_SIGNATURE = 0x04034b50;
const CENTRAL_DIR_SIGNATURE = 0x02014b50;
const EOCD_SIGNATURE = 0x06054b50;

const UTF8_NAME_FLAG = 0x0800;
const METHOD_STORE = 0;
const VERSION_NEEDED = 20;

/** Fixed DOS date/time: 1980-01-01 00:00:00, for deterministic output. */
const DOS_TIME = 0;
const DOS_DATE = (0 << 9) | (1 << 5) | 1;

const MAX_ENTRIES = 65535;
const MAX_OFFSET = 0xffffffff;

function normalizePath(path: string): string {
  return path.replace(/\\/g, "/").replace(/^\.?\/+/, "");
}

/**
 * Packages entries into a zip byte buffer using the STORE method. Throws a
 * plain-language error instead of silently producing a corrupt or
 * unreadable archive when a limit this writer intentionally does not
 * support (zip64: >65535 files or >4GB total) would be needed.
 */
export function buildZip(entries: ZipInputEntry[]): Uint8Array {
  if (entries.length === 0) {
    throw new Error("buildZip: no files to package");
  }
  if (entries.length > MAX_ENTRIES) {
    throw new Error(`buildZip: ${entries.length} files exceeds the ${MAX_ENTRIES} zip64-free limit`);
  }

  const encoder = new TextEncoder();
  const local = new ByteWriter();
  const centralEntries: Array<{ nameBytes: Uint8Array; crc: number; size: number; offset: number }> = [];

  for (const entry of entries) {
    const nameBytes = encoder.encode(normalizePath(entry.path));
    const data = entry.data;
    const crc = crc32(data);
    const offset = local.size;
    if (offset > MAX_OFFSET) {
      throw new Error("buildZip: archive exceeds 4GB, which needs zip64 (not supported)");
    }

    local.u32(LOCAL_FILE_SIGNATURE);
    local.u16(VERSION_NEEDED);
    local.u16(UTF8_NAME_FLAG);
    local.u16(METHOD_STORE);
    local.u16(DOS_TIME);
    local.u16(DOS_DATE);
    local.u32(crc);
    local.u32(data.length);
    local.u32(data.length);
    local.u16(nameBytes.length);
    local.u16(0);
    local.push(nameBytes);
    local.push(data);

    centralEntries.push({ nameBytes, crc, size: data.length, offset });
  }

  const central = new ByteWriter();
  for (const e of centralEntries) {
    central.u32(CENTRAL_DIR_SIGNATURE);
    central.u16(VERSION_NEEDED);
    central.u16(VERSION_NEEDED);
    central.u16(UTF8_NAME_FLAG);
    central.u16(METHOD_STORE);
    central.u16(DOS_TIME);
    central.u16(DOS_DATE);
    central.u32(e.crc);
    central.u32(e.size);
    central.u32(e.size);
    central.u16(e.nameBytes.length);
    central.u16(0);
    central.u16(0);
    central.u16(0);
    central.u16(0);
    central.u32(0);
    central.u32(e.offset);
    central.push(e.nameBytes);
  }

  const centralOffset = local.size;
  const centralSize = central.size;
  if (centralOffset + centralSize > MAX_OFFSET) {
    throw new Error("buildZip: archive exceeds 4GB, which needs zip64 (not supported)");
  }

  const eocd = new ByteWriter();
  eocd.u32(EOCD_SIGNATURE);
  eocd.u16(0);
  eocd.u16(0);
  eocd.u16(centralEntries.length);
  eocd.u16(centralEntries.length);
  eocd.u32(centralSize);
  eocd.u32(centralOffset);
  eocd.u16(0);

  const out = new Uint8Array(local.size + central.size + eocd.size);
  out.set(local.toUint8Array(), 0);
  out.set(central.toUint8Array(), local.size);
  out.set(eocd.toUint8Array(), local.size + central.size);
  return out;
}

/**
 * Directory names excluded from client-side packaging, mirroring
 * cli/internal/archive/archive.go's alwaysExcludedDirs: build output and
 * dependency trees that regularly blow the 100MB upload budget on projects
 * that never bothered to gitignore them.
 */
export const ALWAYS_EXCLUDED_DIRS: ReadonlySet<string> = new Set([
  ".git",
  "node_modules",
  ".next",
  "dist",
  "build",
  "venv",
  "__pycache__",
]);

/** Reports whether relPath (forward-slash, no leading slash) falls inside an always-excluded directory. */
export function isExcludedPath(relPath: string): boolean {
  const segments = normalizePath(relPath).split("/");
  return segments.some((segment) => ALWAYS_EXCLUDED_DIRS.has(segment));
}

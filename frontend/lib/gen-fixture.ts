/**
 * Regenerates backend/internal/sourcedetect/testdata/client-packed-vite.zip,
 * the cross-language proof fixture for frontend/lib/zip.ts consumed by
 * backend/internal/sourcedetect/client_zip_test.go (TestDetectClientPackedZip).
 *
 * Run after changing frontend/lib/zip.ts:
 *
 *   node --experimental-strip-types frontend/lib/gen-fixture.ts
 *
 * Not part of the app bundle or the test suite - a one-off dev utility.
 */
import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { buildZip } from "./zip.ts";

const utf8 = (s: string) => new TextEncoder().encode(s);

const zip = buildZip([
  {
    path: "my-lovable-app/package.json",
    data: utf8(
      JSON.stringify({
        name: "my-lovable-app",
        dependencies: { react: "18.3.0", vite: "5.4.0" },
      })
    ),
  },
  { path: "my-lovable-app/src/main.tsx", data: utf8("export default function App() { return null; }\n") },
  { path: "my-lovable-app/index.html", data: utf8("<!doctype html><html><body></body></html>\n") },
  { path: "my-lovable-app/README.md", data: utf8("# My Lovable App\n") },
]);

const outPath = fileURLToPath(new URL("../../backend/internal/sourcedetect/testdata/client-packed-vite.zip", import.meta.url));
writeFileSync(outPath, zip);
console.log(`wrote ${zip.length} bytes to ${outPath}`);

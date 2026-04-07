// esbuild configuration for loom-spawn-driver. Bundles the TypeScript sources
// under src/ along with their SDK dependencies into a single CommonJS file at
// dist/spawn-driver.js so the Go HUD can embed it via go:embed.
//
// We target Node.js 20 (matches the node-22-alpine devbox base image's
// runtime baseline) and produce CJS so the bundle can be invoked with bare
// `node /opt/loom/spawn-driver.js` without an --experimental-vm-modules flag
// or a package.json declaring "type": "module" inside the spawn pod.

import { build } from "esbuild";
import { mkdir, chmod, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const outFile = resolve(__dirname, "dist/spawn-driver.js");
const outDir = dirname(outFile);

if (!existsSync(outDir)) {
  await mkdir(outDir, { recursive: true });
}

const result = await build({
  entryPoints: [resolve(__dirname, "src/index.ts")],
  outfile: outFile,
  bundle: true,
  platform: "node",
  target: "node20",
  format: "cjs",
  sourcemap: false,
  minify: false,
  legalComments: "none",
  // The Codex SDK calls `createRequire(import.meta.url)` to dynamically load
  // its platform-specific native binary. When bundled to CJS, esbuild's
  // default `import_meta.url` shim is `undefined`, which crashes at module
  // load time. Define `import.meta.url` to a CJS-friendly expression and
  // inject the matching declaration via the banner so `require('url')` is
  // resolved at runtime instead of bundle time.
  define: {
    "import.meta.url": "__loom_spawn_driver_meta_url",
  },
  // Inject a shebang so the bundle can be executed directly, plus a
  // `__loom_spawn_driver_meta_url` constant that mirrors `import.meta.url`
  // semantics for the CJS runtime.
  banner: {
    js: [
      "#!/usr/bin/env node",
      '"use strict";',
      "const __loom_spawn_driver_meta_url = require('url').pathToFileURL(__filename).href;",
    ].join("\n"),
  },
  logLevel: "info",
  metafile: true,
});

if (result.metafile) {
  // Write the metafile next to the bundle for size analysis (gitignored).
  await writeFile(
    resolve(outDir, "spawn-driver.meta.json"),
    JSON.stringify(result.metafile, null, 2),
  );
}

// The parent package.json declares "type": "module" so tsc + esbuild can
// resolve `.js` imports against `.ts` sources during development. The emitted
// bundle, however, is CommonJS — drop a sub-package.json into dist/ so Node
// loads spawn-driver.js as CJS regardless of how it is invoked.
await writeFile(
  resolve(outDir, "package.json"),
  JSON.stringify({ type: "commonjs" }, null, 2) + "\n",
);

await chmod(outFile, 0o755);
console.log(`built ${outFile}`);

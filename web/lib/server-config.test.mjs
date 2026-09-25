// server-config tests — run with:  node --test lib/server-config.test.mjs
// (from web/). No extra deps: node:test + the project's own typescript
// package (devDependency) transpiles server-config.ts in memory, so these
// assertions run against the real source — never a copy.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { Module } from "node:module";
import { mkdtempSync, rmSync, writeFileSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const ts = require("typescript");
const here = dirname(fileURLToPath(import.meta.url));

function load() {
  const src = readFileSync(join(here, "server-config.ts"), "utf8");
  const { outputText } = ts.transpileModule(src, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  });
  const m = new Module("server-config-under-test");
  m.paths = Module._nodeModulePaths(here);
  m._compile(outputText, join(here, "server-config-under-test.js"));
  return m.exports;
}

const mod = load();

const YAML = `
run:
  profile: yaml-prof
models:
  - name: yaml-prof
    provider: openai-compatible
    model: yaml-model
    apiBase: http://127.0.0.1:9/v1
    apiKeyEnv: TUX_TEST_ENV_KEY
    apiKey: yaml-literal-key   # local-dev fallback
  - name: quoted
    apiBase: http://127.0.0.1:9/v1
    apiKeyEnv: TUX_TEST_OTHER_KEY
    apiKey: "quoted-key"
  - name: empty-literal
    apiBase: http://127.0.0.1:9/v1
    apiKeyEnv: TUX_TEST_MISSING_KEY
    apiKey: ""
database:
  dsnEnv: TUX_TEST_DSN_ENV
  dsn: "yaml-literal-dsn"
`;

function withEnv(vars, fn) {
  const saved = new Map();
  for (const k of Object.keys(vars)) {
    saved.set(k, process.env[k]);
    if (vars[k] === undefined) delete process.env[k];
    else process.env[k] = vars[k];
  }
  try {
    return fn();
  } finally {
    for (const [k, v] of saved) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  }
}

test("serverConfigPath: null without env, absolute when set, null when missing", () => {
  withEnv({ TUXGO_CONFIG: undefined }, () => {
    assert.equal(mod.serverConfigPath(), null);
  });
  const dir = mkdtempSync(join(tmpdir(), "tux-cfg-"));
  try {
    const p = join(dir, "srv.yaml");
    writeFileSync(p, YAML);
    withEnv({ TUXGO_CONFIG: p }, () => {
      assert.equal(mod.serverConfigPath(), p);
    });
    withEnv({ TUXGO_CONFIG: join(dir, "nope.yaml") }, () => {
      assert.equal(mod.serverConfigPath(), null);
    });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("withServerConfig: injects -config after subcommand; skips analyze + explicit -config", () => {
  const dir = mkdtempSync(join(tmpdir(), "tux-cfg-"));
  try {
    const p = join(dir, "srv.yaml");
    writeFileSync(p, YAML);
    withEnv({ TUXGO_CONFIG: p }, () => {
      assert.deepEqual(mod.withServerConfig(["convertgo", "a.pc", "-no-llm"]), ["convertgo", "-config", p, "a.pc", "-no-llm"]);
      assert.deepEqual(mod.withServerConfig(["discover", "a.pc", "-out", "m"]), ["discover", "-config", p, "a.pc", "-out", "m"]);
      assert.deepEqual(mod.withServerConfig(["extract", "a.pc"]), ["extract", "-config", p, "a.pc"]);
      // analyze takes no -config flag (flags.go) — untouched.
      assert.deepEqual(mod.withServerConfig(["analyze", "a.pc"]), ["analyze", "a.pc"]);
      // explicit caller -config wins — no double injection, both spellings.
      assert.deepEqual(mod.withServerConfig(["convertgo", "-config", "mine.yaml", "a.pc"]), ["convertgo", "-config", "mine.yaml", "a.pc"]);
      assert.deepEqual(mod.withServerConfig(["convertgo", "a.pc", "-config=mine.yaml"]), ["convertgo", "a.pc", "-config=mine.yaml"]);
      assert.deepEqual(mod.withServerConfig([]), []);
    });
    withEnv({ TUXGO_CONFIG: undefined }, () => {
      assert.deepEqual(mod.withServerConfig(["convertgo", "a.pc"]), ["convertgo", "a.pc"]);
    });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("readServerConfigSnapshot: profile, per-model env+literal, database dsn", () => {
  const dir = mkdtempSync(join(tmpdir(), "tux-cfg-"));
  try {
    const p = join(dir, "srv.yaml");
    writeFileSync(p, YAML);
    withEnv({ TUXGO_CONFIG: p }, () => {
      const snap = mod.readServerConfigSnapshot();
      assert.ok(snap);
      assert.equal(snap.profile, "yaml-prof");
      assert.equal(snap.models.length, 3);
      assert.deepEqual(snap.models[0], { name: "yaml-prof", apiKeyEnv: "TUX_TEST_ENV_KEY", hasLiteralApiKey: true });
      assert.deepEqual(snap.models[1], { name: "quoted", apiKeyEnv: "TUX_TEST_OTHER_KEY", hasLiteralApiKey: true });
      assert.deepEqual(snap.models[2], { name: "empty-literal", apiKeyEnv: "TUX_TEST_MISSING_KEY", hasLiteralApiKey: false });
      assert.deepEqual(snap.database, { dsnEnv: "TUX_TEST_DSN_ENV", hasLiteralDsn: true });
      // values never leak — only presence booleans.
      assert.ok(!JSON.stringify(snap).includes("yaml-literal-key"));
      assert.ok(!JSON.stringify(snap).includes("yaml-literal-dsn"));
    });
    withEnv({ TUXGO_CONFIG: undefined }, () => {
      assert.equal(mod.readServerConfigSnapshot(), null);
    });
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("modelResolves/dsnResolves: env wins, yaml literal is the fallback", () => {
  const m = { name: "yaml-prof", apiKeyEnv: "TUX_TEST_ENV_KEY", hasLiteralApiKey: true };
  withEnv({ TUX_TEST_ENV_KEY: "env-key" }, () => assert.equal(mod.modelResolves(m), true));
  withEnv({ TUX_TEST_ENV_KEY: undefined }, () => assert.equal(mod.modelResolves(m), true)); // literal
  withEnv({ TUX_TEST_ENV_KEY: undefined }, () =>
    assert.equal(mod.modelResolves({ name: "x", apiKeyEnv: "TUX_TEST_ENV_KEY", hasLiteralApiKey: false }), false),
  );
  withEnv({ TUX_TEST_DSN_ENV: "env-dsn" }, () => assert.equal(mod.dsnResolves("TUX_TEST_DSN_ENV", true), true));
  withEnv({ TUX_TEST_DSN_ENV: undefined }, () => assert.equal(mod.dsnResolves("TUX_TEST_DSN_ENV", true), true)); // literal
  withEnv({ TUX_TEST_DSN_ENV: undefined }, () => assert.equal(mod.dsnResolves("TUX_TEST_DSN_ENV", false), false));
});

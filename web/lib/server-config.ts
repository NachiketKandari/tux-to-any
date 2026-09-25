import { statSync } from "node:fs";
import { readFileSync } from "node:fs";
import { isAbsolute, join } from "node:path";

// Server-level run config (TUXGO_CONFIG) — the web viewer's answer to the
// disposable-tmpdir problem.
//
// Every tuxconv call the viewer spawns runs with cwd=<job tmpdir>, and job
// tmpdirs carry no .tuxgo.yaml — so without this, browser runs always fell
// back to the stock defaults (profile `onprem-vllm`, key = $VLLM_API_KEY)
// while the CLI next door read yaml literals from the working copy. Set
// TUXGO_CONFIG once for the server process:
//
//   export TUXGO_CONFIG=$PWD/.tuxgo.yaml   # any path; literals stay gitignored
//
// and every viewer run shares the CLI's yaml keys (models[].apiKey,
// database.dsn — env vars still win over literals, same resolution).
//
// Two layers, both fail-open to today's behavior:
//
//  1. runTux injects `-config <abs path>` into every supporting subcommand
//     (explicit caller -config still wins — injection skips when present).
//  2. The tuxconv CLI itself falls back to $TUXGO_CONFIG when neither
//     -config nor ./.tuxgo.yaml resolves (cmd/tuxconv/extract.go), so
//     direct CLI use from a bare directory honors it too.
//
// Nothing here ever returns a secret value — only presence booleans and
// the profile name. Key/DSN values stay in the server process and the
// spawned CLI; the browser sees booleans via /api/llm-status + /api/db-status.

const ENV_VAR = "TUXGO_CONFIG";

// Subcommands accepting -config (mirrors cmd/tuxconv/flags.go commandFlags).
// `analyze` takes none, so injection skips it; version/help/retrystats take
// none either and the viewer never spawns them.
const CONFIG_COMMANDS = new Set([
  "extract",
  "plan",
  "convertgo",
  "discover",
  "convertbatchpy",
  "convertcs",
  "gentest",
  "ainames",
  "templates",
  "flow",
  "dbcheck",
]);

// Repo root = parent of web/. Absolute -config paths survive the per-job
// cwd (job tmpdirs); a relative TUXGO_CONFIG resolves against the repo root
// first, then the server cwd.
function repoRoot(): string {
  return join(process.cwd(), "..");
}

let warnedMissing = "";

export function serverConfigPath(): string | null {
  const raw = (process.env[ENV_VAR] ?? "").trim();
  if (raw === "") return null;
  const candidates = isAbsolute(raw) ? [raw] : [join(repoRoot(), raw), join(process.cwd(), raw)];
  for (const c of candidates) {
    try {
      if (statSync(c).isFile()) {
        warnedMissing = "";
        return c;
      }
    } catch {
      /* try next */
    }
  }
  if (warnedMissing !== raw) {
    warnedMissing = raw;
    console.warn(`[tux-web] ${ENV_VAR}=${raw} names no file — running on stock defaults`);
  }
  return null;
}

// configArgsFor returns ["-config", path] when the subcommand supports the
// flag, the caller did not pass one already, and TUXGO_CONFIG resolves.
export function configArgsFor(subcommand: string, args: string[]): string[] {
  if (!CONFIG_COMMANDS.has(subcommand)) return [];
  if (args.some((a) => a === "-config" || a === "--config" || a.startsWith("-config=") || a.startsWith("--config="))) {
    return [];
  }
  const p = serverConfigPath();
  return p ? ["-config", p] : [];
}

// withServerConfig splices the server -config right after the subcommand
// (args[0]), so `runTux(dir, ["convertgo", src, ...])` keeps reading
// naturally at every call site.
export function withServerConfig(args: string[]): string[] {
  if (args.length === 0) return args;
  const extra = configArgsFor(args[0], args.slice(1));
  if (extra.length === 0) return args;
  return [args[0], ...extra, ...args.slice(1)];
}

export interface ServerModelSnapshot {
  name: string;
  apiKeyEnv: string;
  hasLiteralApiKey: boolean;
}

export interface ServerConfigSnapshot {
  path: string;
  profile: string;
  models: ServerModelSnapshot[];
  database: { dsnEnv: string; hasLiteralDsn: boolean };
}

function unquote(v: string): string {
  const t = v.trim();
  if (t.length >= 2 && ((t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'")))) {
    return t.slice(1, -1).trim();
  }
  return t;
}

// stripComment cuts a trailing `# comment` — only when the hash starts the
// line or follows whitespace, so `sk-or#key`-style values survive.
function stripComment(line: string): string {
  let inSingle = false;
  let inDouble = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (c === "'" && !inDouble) inSingle = !inSingle;
    else if (c === '"' && !inSingle) inDouble = !inDouble;
    else if (c === "#" && !inSingle && !inDouble && (i === 0 || /\s/.test(line[i - 1]))) {
      return line.slice(0, i);
    }
  }
  return line;
}

// readServerConfigSnapshot parses just the key-resolution surface of the
// server yaml (profile name, per-model apiKeyEnv + literal presence,
// database dsnEnv + literal presence). Values are never returned — only
// whether a literal is set. Unknown lines are ignored; a missing or
// unreadable file yields null (callers fall back to stock-profile checks).
export function readServerConfigSnapshot(): ServerConfigSnapshot | null {
  const path = serverConfigPath();
  if (!path) return null;
  let text: string;
  try {
    text = readFileSync(path, "utf8");
  } catch {
    return null;
  }
  const snap: ServerConfigSnapshot = {
    path,
    profile: "",
    models: [],
    database: { dsnEnv: "", hasLiteralDsn: false },
  };
  let section = "";
  let current: ServerModelSnapshot | null = null;
  for (const rawLine of text.split("\n")) {
    const line = stripComment(rawLine);
    if (line.trim() === "") continue;
    const indent = line.length - line.trimStart().length;
    const t = line.trim();
    if (indent === 0 && t.endsWith(":")) {
      section = t.slice(0, -1);
      current = null;
      continue;
    }
    if (section === "run" && indent > 0) {
      const m = t.match(/^profile\s*:\s*(.+)$/);
      if (m) snap.profile = unquote(m[1]);
      continue;
    }
    if (section === "models") {
      const start = t.match(/^-\s*name\s*:\s*(.+)$/);
      if (start) {
        current = { name: unquote(start[1]), apiKeyEnv: "", hasLiteralApiKey: false };
        snap.models.push(current);
        continue;
      }
      if (current && indent > 0) {
        let m = t.match(/^apiKeyEnv\s*:\s*(.*)$/);
        if (m) {
          current.apiKeyEnv = unquote(m[1]);
          continue;
        }
        m = t.match(/^apiKey\s*:\s*(.*)$/);
        if (m) {
          current.hasLiteralApiKey = unquote(m[1]) !== "";
          continue;
        }
      }
      continue;
    }
    if (section === "database" && indent > 0) {
      let m = t.match(/^dsnEnv\s*:\s*(.*)$/);
      if (m) {
        snap.database.dsnEnv = unquote(m[1]);
        continue;
      }
      m = t.match(/^dsn\s*:\s*(.*)$/);
      if (m) {
        snap.database.hasLiteralDsn = unquote(m[1]) !== "";
      }
    }
  }
  return snap;
}

// modelResolves mirrors internal/config Model.ResolveKey without touching
// values: env var wins, yaml literal is the fallback.
export function modelResolves(m: ServerModelSnapshot): boolean {
  if (m.apiKeyEnv !== "" && (process.env[m.apiKeyEnv] ?? "").trim() !== "") return true;
  return m.hasLiteralApiKey;
}

// dsnResolves mirrors internal/db ResolveDSN presence: env wins, literal fallback.
export function dsnResolves(dsnEnv: string, hasLiteral: boolean): boolean {
  const env = (dsnEnv || "ORACLE_DSN").trim() || "ORACLE_DSN";
  if ((process.env[env] ?? "").trim() !== "") return true;
  return hasLiteral;
}

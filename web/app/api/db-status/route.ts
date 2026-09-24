import { NextResponse } from "next/server";

// GET /api/db-status — optional Oracle connectivity summary (internal/db).
// Never requires credentials: reports enabled=false when no DSN resolves
// (neither ORACLE_DSN nor database.dsn), which is the normal offline mode.
// The DSN value is never read out — only its source channel.
export async function GET() {
  const dsnEnv = process.env.ORACLE_DSN_ENV ?? "ORACLE_DSN";
  const fromEnv = process.env[dsnEnv] ?? process.env.ORACLE_DSN ?? "";
  const enabled = fromEnv.trim() !== "";
  return NextResponse.json({
    enabled,
    driver: "oracle",
    source: enabled ? `env:${process.env[dsnEnv] ? dsnEnv : "ORACLE_DSN"}` : "none",
    note: enabled
      ? "DSN resolves — live Oracle available (run `tuxconv dbcheck -ping` to verify)"
      : "offline — no DSN configured; conversion runs deterministic without it",
  });
}

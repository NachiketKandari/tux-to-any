import { promises as fs } from "node:fs";
import { NextResponse } from "next/server";
import { getJob, setJob, makeLogger } from "@/lib/jobs";
import { runTux } from "@/lib/tuxconv";

// POST /api/jobs/:id/filter-preview { expr } — read-only scenarioFilter fold
// preview (`discover -filter "<expr>" -stdout`-style output, no artifacts).
export async function POST(req: Request, { params }: { params: { id: string } }) {
  const job = getJob(params.id);
  if (!job) return NextResponse.json({ error: "unknown job" }, { status: 404 });
  if (job.status === "running") return NextResponse.json({ error: "job is busy" }, { status: 409 });
  const body = (await req.json().catch(() => ({}))) as { expr?: string };
  const expr = (body.expr ?? "").trim();
  if (!expr) return NextResponse.json({ error: "expr is required" }, { status: 400 });
  if (expr.length > 500) return NextResponse.json({ error: "expr exceeds 500 chars" }, { status: 400 });

  job.status = "running";
  setJob(job);
  const log = makeLogger(job);
  try {
    const r = await runTux(job.dir, ["discover", job.sourcePath, "-filter", expr, "-stdout"], log);
    const output = (r.stdout || "") + (r.stderr ? `\n${r.stderr}` : "");
    job.status = "done";
    setJob(job);
    return NextResponse.json({ status: "done", code: r.code, output: output || "(no output)" });
  } catch (e) {
    job.status = "error";
    job.error = e instanceof Error ? e.message : "filter preview failed";
    setJob(job);
    return NextResponse.json({ status: "error", error: job.error }, { status: 500 });
  }
}

import { promises as fs } from "node:fs";
import { join, relative } from "node:path";
import { NextResponse } from "next/server";
import { getJob } from "@/lib/jobs";
import { recordMetric } from "@/lib/metrics";

const MAX_BYTES = 200 * 1024;

function inside(jobDir: string, abs: string): boolean {
  const back = relative(jobDir, abs);
  return back !== "" && !back.startsWith("..");
}

// GET /api/jobs/:id/files?path=<root-relative> — read one converted file.
export async function GET(req: Request, { params }: { params: { id: string } }) {
  const job = getJob(params.id);
  if (!job) return NextResponse.json({ error: "unknown job" }, { status: 404 });
  if (!job.converted) return NextResponse.json({ error: "nothing converted yet" }, { status: 400 });
  const rel = new URL(req.url).searchParams.get("path") ?? "";
  const abs = join(job.converted.root, rel);
  if (!inside(job.dir, abs)) {
    return NextResponse.json({ error: "path escapes the job directory" }, { status: 400 });
  }
  try {
    const st = await fs.stat(abs);
    if (!st.isFile() || st.size > MAX_BYTES) {
      return NextResponse.json({ error: "file missing or over 200KB" }, { status: 400 });
    }
    return NextResponse.json({ path: rel, content: await fs.readFile(abs, "utf8") });
  } catch {
    return NextResponse.json({ error: "cannot read file" }, { status: 404 });
  }
}

// PUT /api/jobs/:id/files { path, content } — save an edited converted file.
// Writes stay inside the converted tree; every save appends to the master log.
export async function PUT(req: Request, { params }: { params: { id: string } }) {
  const job = getJob(params.id);
  if (!job) return NextResponse.json({ error: "unknown job" }, { status: 404 });
  if (!job.converted) return NextResponse.json({ error: "nothing converted yet" }, { status: 400 });
  const body = (await req.json().catch(() => ({}))) as { path?: string; content?: string };
  if (!body.path || typeof body.content !== "string") {
    return NextResponse.json({ error: "path and content are required" }, { status: 400 });
  }
  if (body.content.length > MAX_BYTES) {
    return NextResponse.json({ error: "file exceeds 200KB" }, { status: 400 });
  }
  const abs = join(job.converted.root, body.path);
  if (!inside(job.dir, abs) || relative(job.converted.root, abs).startsWith("..")) {
    return NextResponse.json({ error: "path escapes the converted tree" }, { status: 400 });
  }
  try {
    const st = await fs.stat(abs);
    if (!st.isFile()) return NextResponse.json({ error: "not a file" }, { status: 400 });
  } catch {
    return NextResponse.json({ error: "cannot read file" }, { status: 404 });
  }
  await fs.writeFile(abs, body.content);
  await recordMetric({ kind: "edit", job: job.name, target: job.converted.target, files: 1, note: body.path });
  return NextResponse.json({ ok: true, path: body.path });
}

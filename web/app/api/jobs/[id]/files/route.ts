import { promises as fs } from "node:fs";
import { join, relative } from "node:path";
import { NextResponse } from "next/server";
import { getJob } from "@/lib/jobs";

const MAX_BYTES = 200 * 1024;

// GET /api/jobs/:id/files?path=<root-relative> — read one converted file.
export async function GET(req: Request, { params }: { params: { id: string } }) {
  const job = getJob(params.id);
  if (!job) return NextResponse.json({ error: "unknown job" }, { status: 404 });
  if (!job.converted) return NextResponse.json({ error: "nothing converted yet" }, { status: 400 });
  const rel = new URL(req.url).searchParams.get("path") ?? "";
  const abs = join(job.converted.root, rel);
  const back = relative(job.dir, abs);
  if (back === "" || back.startsWith("..")) {
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

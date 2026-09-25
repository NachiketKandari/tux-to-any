import { promises as fs } from "node:fs";
import { join } from "node:path";
import { NextResponse } from "next/server";
import { getJob } from "@/lib/jobs";
import { buildLineage } from "@/lib/lineage";

// GET /api/jobs/:id/lineage — evidence-based source→output trace.
//
// Reads the converted tree from the job tmpdir (capped: 60 files, 120KB
// each), matches IR literals (tables, FML fields, tpcall services) against
// file contents, and returns links of the form "q1 became db/…go".
// Pure derivation from artifacts already on disk — no CLI spawn, no LLM.
const MAX_FILES = 60;
const MAX_BYTES = 120 * 1024;

export async function GET(_req: Request, { params }: { params: { id: string } }) {
  const job = getJob(params.id);
  if (!job) return NextResponse.json({ error: "unknown job" }, { status: 404 });
  if (!job.converted) return NextResponse.json({ error: "convert first — no converted tree yet" }, { status: 409 });

  const root = job.converted.root;
  const rel = (job.converted.files ?? []).slice(0, MAX_FILES);
  const files: { path: string; content: string }[] = [];
  for (const r of rel) {
    try {
      if (r.includes("..")) continue;
      const abs = join(root, r);
      if (!abs.startsWith(root)) continue;
      const st = await fs.stat(abs);
      if (!st.isFile() || st.size > MAX_BYTES) continue;
      const content = await fs.readFile(abs, "utf8");
      files.push({ path: r, content: content.slice(0, MAX_BYTES) });
    } catch {
      continue;
    }
  }

  const result = buildLineage(job.ir, files);
  return NextResponse.json({ ...result, target: job.converted.target });
}

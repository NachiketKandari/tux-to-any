import { promises as fs } from "node:fs";
import { join } from "node:path";
import { NextResponse } from "next/server";
import { getJob, setJob, makeLogger } from "@/lib/jobs";
import { runTux } from "@/lib/tuxconv";
import { recordMetric } from "@/lib/metrics";

// POST /api/jobs/:id/discover { target: "go" | "cs", useLLM?: boolean }
// Step 1 of the mapping-first flow: runs the scan-then-tag draft pass.
// useLLM=false (default) appends -no-llm → deterministic names; useLLM=true
// omits it → AI naming when a key resolves, deterministic fallback
// otherwise (no key required — the run degrades, never fails). -target cs
// is deterministic-only throughout, so the flag is a no-op there.
export async function POST(req: Request, { params }: { params: { id: string } }) {
  const job = getJob(params.id);
  if (!job) return NextResponse.json({ error: "unknown job" }, { status: 404 });
  if (job.status === "running") return NextResponse.json({ error: "job is busy" }, { status: 409 });
  const body = (await req.json().catch(() => ({}))) as { target?: string; useLLM?: boolean };
  const target = body.target === "cs" ? "cs" : "go";
  const useLLM = body.useLLM === true;

  job.status = "running";
  job.error = undefined;
  setJob(job);
  const log = makeLogger(job);

  try {
    const outDir = join(job.dir, "mappings");
    let args: string[];
    if (target === "cs") {
      args = ["discover", job.sourcePath, "-out", outDir, "-target", "cs"];
    } else if (useLLM) {
      args = ["discover", job.sourcePath, "-out", outDir];
    } else {
      args = ["discover", job.sourcePath, "-out", outDir, "-no-llm"];
    }
    log(`$ discover target=${target} llm=${useLLM ? "on" : "off"}${target === "cs" ? " (cs is deterministic-only)" : ""}`);
    const r = await runTux(job.dir, args, log);
    if (r.code !== 0) throw new Error("discover failed — see logs");
    const names = await fs.readdir(outDir);
    const drafts = [];
    for (const n of names) {
      if (!/\.ya?ml$/i.test(n)) continue;
      const p = join(outDir, n);
      drafts.push({ path: p, content: await fs.readFile(p, "utf8") });
    }
    if (drafts.length === 0) throw new Error("discover wrote no drafts — see logs");
    drafts.sort((a, b) => a.path.localeCompare(b.path));
    job.drafts = drafts;
    job.mappingPath = drafts[0].path;
    job.mappingLLM = useLLM && target !== "cs";
    job.status = "done";
    await recordMetric({ kind: "discover", job: job.name, target, files: drafts.length });
  } catch (e) {
    job.status = "error";
    job.error = e instanceof Error ? e.message : "discover failed";
  }
  setJob(job);
  return NextResponse.json({ status: job.status, error: job.error, drafts: job.drafts, mappingPath: job.mappingPath });
}

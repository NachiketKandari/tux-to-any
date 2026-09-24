import { NextResponse } from "next/server";
import { getJob, setJob, makeLogger } from "@/lib/jobs";
import { listFilesRecursive, runTux } from "@/lib/tuxconv";
import { recordMetric } from "@/lib/metrics";

// POST /api/jobs/:id/gentest { mode: "check" | "generate", useLLM?: boolean }
// Runs against the converted Go tree (in-place for generate).
// useLLM=false (default) keeps -no-llm → template-deterministic suites;
// useLLM=true omits it → field-mapping controller tests ride the LLM seam
// when a key resolves, llm-required notes otherwise (no key required).
export async function POST(req: Request, { params }: { params: { id: string } }) {
  const job = getJob(params.id);
  if (!job) return NextResponse.json({ error: "unknown job" }, { status: 404 });
  if (job.status === "running") return NextResponse.json({ error: "job is busy" }, { status: 409 });
  if (!job.converted || job.converted.target !== "go") {
    return NextResponse.json({ error: "convert a Go tree first" }, { status: 400 });
  }
  const body = (await req.json().catch(() => ({}))) as { mode?: string; useLLM?: boolean };
  const mode = body.mode === "generate" ? "generate" : "check";
  const useLLM = body.useLLM === true;

  job.status = "running";
  job.error = undefined;
  setJob(job);
  const log = makeLogger(job);

  try {
    const root = job.converted.root;
    if (mode === "check") {
      const r = await runTux(job.dir, ["gentest", root, "-check-only"], log);
      job.gentestGap = r.stdout || r.stderr;
    } else {
      const args = useLLM ? ["gentest", root] : ["gentest", root, "-no-llm"];
      log(`$ gentest llm=${useLLM ? "on" : "off"}`);
      const r = await runTux(job.dir, args, log);
      if (r.code !== 0) throw new Error("gentest failed — see logs");
      const all = await listFilesRecursive(root);
      job.gentestFiles = all.filter((f) => f.endsWith("_test.go")).map((f) => f.slice(root.length + 1));
      const tail = r.stdout.trim().split("\n").slice(-6).join("\n");
      job.gentestGap = tail;
      // Refresh the converted tree listing (new _test.go files landed).
      if (job.converted) {
        job.converted.files = all.map((f) => f.slice(root.length + 1));
      }
      await recordMetric({ kind: "gentest", job: job.name, target: "go", tests: job.gentestFiles.length, files: job.gentestFiles.length });
    }
    job.status = "done";
  } catch (e) {
    job.status = "error";
    job.error = e instanceof Error ? e.message : "gentest failed";
  }
  setJob(job);
  return NextResponse.json({ status: job.status, error: job.error, gap: job.gentestGap, files: job.gentestFiles });
}

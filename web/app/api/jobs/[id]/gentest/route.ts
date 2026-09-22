import { NextResponse } from "next/server";
import { getJob, setJob, makeLogger } from "@/lib/jobs";
import { listFilesRecursive, runTux } from "@/lib/tuxconv";

// POST /api/jobs/:id/gentest { mode: "check" | "generate" }
// Runs against the converted Go tree (in-place for generate).
export async function POST(req: Request, { params }: { params: { id: string } }) {
  const job = getJob(params.id);
  if (!job) return NextResponse.json({ error: "unknown job" }, { status: 404 });
  if (job.status === "running") return NextResponse.json({ error: "job is busy" }, { status: 409 });
  if (!job.converted || job.converted.target !== "go") {
    return NextResponse.json({ error: "convert a Go tree first" }, { status: 400 });
  }
  const body = (await req.json().catch(() => ({}))) as { mode?: string };
  const mode = body.mode === "generate" ? "generate" : "check";

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
      const r = await runTux(job.dir, ["gentest", root, "-no-llm"], log);
      if (r.code !== 0) throw new Error("gentest failed — see logs");
      const all = await listFilesRecursive(root);
      job.gentestFiles = all.filter((f) => f.endsWith("_test.go")).map((f) => f.slice(root.length + 1));
      const tail = r.stdout.trim().split("\n").slice(-6).join("\n");
      job.gentestGap = tail;
      // Refresh the converted tree listing (new _test.go files landed).
      if (job.converted) {
        job.converted.files = all.map((f) => f.slice(root.length + 1));
      }
    }
    job.status = "done";
  } catch (e) {
    job.status = "error";
    job.error = e instanceof Error ? e.message : "gentest failed";
  }
  setJob(job);
  return NextResponse.json({ status: job.status, error: job.error, gap: job.gentestGap, files: job.gentestFiles });
}

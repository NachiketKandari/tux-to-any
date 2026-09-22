import { join } from "node:path";
import { NextResponse } from "next/server";
import { getJob, setJob, makeLogger } from "@/lib/jobs";
import { listFilesRecursive, runTux } from "@/lib/tuxconv";

type Target = "go" | "py" | "cs";

// POST /api/jobs/:id/convert { target, mappingPath? }
// mappingPath defaults to the job's reviewed draft. Go/CS without a mapping
// run draft-and-stop and return the fresh drafts instead of a tree.
export async function POST(req: Request, { params }: { params: { id: string } }) {
  const job = getJob(params.id);
  if (!job) return NextResponse.json({ error: "unknown job" }, { status: 404 });
  if (job.status === "running") return NextResponse.json({ error: "job is busy" }, { status: 409 });
  const body = (await req.json().catch(() => ({}))) as { target?: Target; mappingPath?: string };
  const target: Target = body.target === "py" || body.target === "cs" ? body.target : "go";
  const mapping = body.mappingPath ?? job.mappingPath;

  job.status = "running";
  job.error = undefined;
  job.converted = undefined;
  setJob(job);
  const log = makeLogger(job);

  try {
    let root = "";
    let args: string[];
    if (target === "go") {
      root = join(job.dir, "go");
      args = ["convertgo", job.sourcePath, "-no-llm", "-base", root];
      if (mapping) args.push("-mapping", mapping);
    } else if (target === "py") {
      root = join(job.dir, "py");
      args = ["convertbatchpy", job.sourcePath, "-no-llm", "-out", root];
    } else {
      root = join(job.dir, "cs");
      args = ["convertcs", job.sourcePath, "-no-llm", "-out", root];
      if (mapping) args.push("-mapping", mapping);
    }
    const r = await runTux(job.dir, args, log);
    const files = await listFilesRecursive(root);
    if (files.length === 0) {
      // Draft-and-stop (or a failure): surface fresh drafts when present.
      const { promises: fs } = await import("node:fs");
      const drafts = [];
      try {
        for (const n of await fs.readdir(join(job.dir, "mappings"))) {
          if (!/\.ya?ml$/i.test(n)) continue;
          const p = join(job.dir, "mappings", n);
          drafts.push({ path: p, content: await fs.readFile(p, "utf8") });
        }
      } catch {
        /* no mappings dir */
      }
      if (drafts.length > 0) {
        job.drafts = drafts;
        job.mappingPath = drafts[0].path;
      }
      if (r.code !== 0 && drafts.length === 0) throw new Error("convert failed — see logs");
      job.status = "done";
      setJob(job);
      return NextResponse.json({
        status: job.status,
        drafts: job.drafts,
        mappingPath: job.mappingPath,
        note: "no mapping yet — review the draft, then convert again",
      });
    }
    const tail = r.stdout.trim().split("\n").slice(-3).join("\n");
    job.converted = { target, root, files: files.map((f) => f.slice(root.length + 1)), summary: tail };
    job.status = "done";
  } catch (e) {
    job.status = "error";
    job.error = e instanceof Error ? e.message : "convert failed";
  }
  setJob(job);
  return NextResponse.json({ status: job.status, error: job.error, converted: job.converted });
}

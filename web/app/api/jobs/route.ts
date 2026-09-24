import { randomUUID } from "node:crypto";
import { promises as fs } from "node:fs";
import { join } from "node:path";
import { NextResponse } from "next/server";
import { setJob, makeLogger, type Job, type FlowReport } from "@/lib/jobs";
import { newJobDir, runTux } from "@/lib/tuxconv";

const MAX_UPLOAD = 5 * 1024 * 1024;

function coerceFlowReport(raw: unknown, fallbackTarget: string): FlowReport | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const r = raw as { target?: string; files?: unknown };
  if (!Array.isArray(r.files)) return undefined;
  return {
    target: typeof r.target === "string" ? r.target : fallbackTarget,
    files: (r.files as Array<Record<string, unknown>>).map((f) => ({
      path: String(f.path ?? ""),
      functions: Array.isArray(f.functions)
        ? (f.functions as Array<Record<string, unknown>>).map((fn) => {
            const tree = (fn.Tree ?? fn.tree) as Record<string, unknown> | undefined;
            const cov = (tree?.Coverage ?? tree?.coverage) as Record<string, unknown> | undefined;
            const hints = (fn.Hints ?? fn.hints) as Array<Record<string, unknown>> | undefined;
            return {
              name: String(fn.Name ?? fn.name ?? "?"),
              startLine: Number(tree?.StartLine ?? tree?.startLine ?? 0) || undefined,
              endLine: Number(tree?.EndLine ?? tree?.endLine ?? 0) || undefined,
              coverage: cov
                ? {
                    classified: Number(cov.Classified ?? cov.classified ?? 0),
                    codeLines: Number(cov.CodeLines ?? cov.codeLines ?? 0),
                    unknown: Number(cov.Unknown ?? cov.unknown ?? 0),
                    residue: Array.isArray(cov.Residue ?? cov.residue)
                      ? ((cov.Residue ?? cov.residue) as unknown[]).map(Number).slice(0, 50)
                      : undefined,
                  }
                : undefined,
              hints: Array.isArray(hints)
                ? hints.slice(0, 100).map((h) => ({
                    kind: String(h.Kind ?? h.kind ?? "hint"),
                    line: Number(h.Line ?? h.line ?? 0),
                    detail: String(h.Detail ?? h.detail ?? ""),
                  }))
                : undefined,
              tree: tree ?? undefined,
            };
          })
        : [],
    })),
  };
}

export async function POST(req: Request) {
  const form = await req.formData();
  const file = form.get("file");
  if (!(file instanceof File)) {
    return NextResponse.json({ error: "field 'file' is required (.pc/.pcf)" }, { status: 400 });
  }
  const name = file.name || "upload.pc";
  if (!/\.(pc|pcf)$/i.test(name)) {
    return NextResponse.json({ error: "only .pc/.pcf files are accepted" }, { status: 400 });
  }
  if (file.size > MAX_UPLOAD) {
    return NextResponse.json({ error: "file exceeds the 5MB upload limit" }, { status: 400 });
  }

  const dir = await newJobDir();
  const inputDir = join(dir, "input");
  await fs.mkdir(inputDir, { recursive: true });
  const sourcePath = join(inputDir, name);
  await fs.writeFile(sourcePath, Buffer.from(await file.arrayBuffer()));

  const job: Job = {
    id: randomUUID().slice(0, 8),
    name,
    dir,
    sourcePath,
    status: "running",
    logs: [],
  };
  setJob(job);
  const log = makeLogger(job);

  try {
    // Deterministic IR to disk (stdout stays free for the log tail).
    // Note: extract takes no -no-llm flag — it never touches the LLM.
    const irPath = join(dir, "ir.json");
    const ex = await runTux(dir, ["extract", sourcePath, "-out", irPath], log);
    if (ex.code !== 0) throw new Error("extract failed — see logs");
    job.ir = JSON.parse(await fs.readFile(irPath, "utf8"));

    const fl = await runTux(dir, ["flow", sourcePath], log);
    job.flowText = fl.stdout || "(no flow output)";

    // Structured flow report for the visual explorer (coverage + hints).
    // Best-effort: the text above is the fallback when this fails.
    try {
      const flowPath = join(dir, "flow.json");
      const fr = await runTux(dir, ["flow", sourcePath, "-out", flowPath], log);
      if (fr.code === 0) {
        const raw = JSON.parse(await fs.readFile(flowPath, "utf8"));
        job.flowReport = coerceFlowReport(raw, sourcePath);
      }
    } catch {
      /* keep flowText-only */
    }

    try {
      const src = await fs.readFile(sourcePath, "utf8");
      job.sourcePreview = src.length > 24 * 1024 ? src.slice(0, 24 * 1024) + "\n…[truncated]" : src;
    } catch {
      /* preview is optional */
    }

    job.status = "done";
  } catch (e) {
    job.status = "error";
    job.error = e instanceof Error ? e.message : "upload pipeline failed";
  }
  setJob(job);
  return NextResponse.json({ id: job.id, status: job.status, error: job.error });
}

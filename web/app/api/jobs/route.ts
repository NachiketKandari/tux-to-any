import { randomUUID } from "node:crypto";
import { promises as fs } from "node:fs";
import { join } from "node:path";
import { NextResponse } from "next/server";
import { setJob, makeLogger, type Job } from "@/lib/jobs";
import { newJobDir, runTux } from "@/lib/tuxconv";

const MAX_UPLOAD = 5 * 1024 * 1024;

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
    job.status = "done";
  } catch (e) {
    job.status = "error";
    job.error = e instanceof Error ? e.message : "upload pipeline failed";
  }
  setJob(job);
  return NextResponse.json({ id: job.id, status: job.status, error: job.error });
}

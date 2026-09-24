// Job store: in-memory cache backed by per-job tmpdir snapshots (job.json).
// Jobs live in disposable tmpdirs; no database.
//
// Why disk-backed: Next dev compiles route bundles independently and
// reloads drop module state, so this Map is only a cache — the tmpdir
// snapshot is the source of truth. All reads go through getJob, which
// rehydrates from disk on a cache miss (sync fs: job counts are tiny).
export type JobStatus = "ready" | "running" | "done" | "error";

export interface DraftFile {
  path: string;
  content: string;
}

export interface ConvertedTree {
  target: string;
  root: string;
  files: string[];
  summary: string;
  /** Whether the run used the LLM seam (false = deterministic -no-llm). */
  llm?: boolean;
}

export interface FlowCoverage {
  classified: number;
  codeLines: number;
  unknown: number;
  residue?: number[];
}

export interface FlowHint {
  kind: string;
  line: number;
  detail: string;
}

export interface FlowFunction {
  name: string;
  startLine?: number;
  endLine?: number;
  coverage?: FlowCoverage;
  hints?: FlowHint[];
  /** Raw tree node kept for the visual explorer (opaque, versioned by CLI). */
  tree?: unknown;
}

export interface FlowReport {
  target: string;
  files: { path: string; functions: FlowFunction[] }[];
}

export interface ScenarioArtifact {
  /** File name relative to the scenarios/ dir, e.g. SVC_X.trn_cd_A.pc */
  name: string;
  /** Rendered flattened source (may be large — capped at read time). */
  content: string;
}

export interface ScenarioBundle {
  axesText: string;
  axesMd?: string;
  sharedMd?: string;
  sharedJson?: unknown;
  artifacts: ScenarioArtifact[];
  note?: string;
}

export interface Job {
  id: string;
  name: string;
  dir: string;
  sourcePath: string;
  status: JobStatus;
  error?: string;
  ir?: Record<string, unknown>;
  flowText?: string;
  flowReport?: FlowReport;
  sourcePreview?: string;
  scenarios?: ScenarioBundle;
  drafts?: DraftFile[];
  mappingPath?: string;
  /** Whether the mapping draft used the LLM naming seam. */
  mappingLLM?: boolean;
  converted?: ConvertedTree;
  gentestGap?: string;
  gentestFiles?: string[];
  logs: string[];
}

import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const jobs = new Map<string, Job>();

function snapshotPath(dir: string): string {
  return join(dir, "job.json");
}

export function getJob(id: string): Job | undefined {
  const hit = jobs.get(id);
  if (hit) return hit;
  // Cache miss (dev reload, fresh worker): scan tmpdir snapshots.
  let entries: string[] = [];
  try {
    entries = readdirSync(tmpdir());
  } catch {
    return undefined;
  }
  for (const e of entries) {
    if (!e.startsWith("tux-web-")) continue;
    try {
      const snap = JSON.parse(readFileSync(join(tmpdir(), e, "job.json"), "utf8")) as Job;
      if (snap && snap.id === id) {
        jobs.set(id, snap);
        return snap;
      }
    } catch {
      continue;
    }
  }
  return undefined;
}

export function setJob(job: Job): void {
  jobs.set(job.id, job);
  persist(job);
}

function persist(job: Job): void {
  try {
    writeFileSync(
      snapshotPath(job.dir),
      JSON.stringify({ ...job, logs: job.logs.slice(-500) })
    );
  } catch {
    /* tmpdir gone — nothing to persist */
  }
}

// makeLogger appends to the job log and re-persists at most every 2s, so
// polling routes see a live tail even across bundles/reloads mid-run.
export function makeLogger(job: Job): (line: string) => void {
  let last = 0;
  return (line: string) => {
    job.logs.push(line);
    const now = Date.now();
    if (now - last > 2000) {
      last = now;
      persist(job);
    }
  };
}

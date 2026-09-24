import { promises as fs } from "node:fs";
import { join } from "node:path";
import { NextResponse } from "next/server";
import { REPO_ROOT } from "@/lib/tuxconv";

// Curated one-click fixtures so the demo works without hunting for a .pc.
const SAMPLES = [
  { path: "testdata/fixtures/nav/SVC_DEMO_LIST.pc", label: "Dispatch-axis service", detail: "H/F/I/default arms · 7 query units" },
  { path: "testdata/fixtures/cs/SVC_CUST_GET_DTL.pc", label: "C# component", detail: "Cursor choreography → List<DTO>" },
  { path: "testdata/fixtures/stripped/SVC_MIN_KITCHEN.pc", label: "Minimal service", detail: "Smallest convertgo round-trip" },
  { path: "testdata/fixtures/stripped/BAT_MIN_SIMPLE.pc", label: "Batch → Python", detail: "Simple batch shape" },
  { path: "testdata/fixtures/merge/SVC_DEMO_MERGE.pc", label: "MERGE service", detail: "Upsert path with tx" },
];

export async function GET() {
  const out: { path: string; label: string; detail: string; bytes: number; available: boolean }[] = [];
  for (const s of SAMPLES) {
    try {
      const st = await fs.stat(join(REPO_ROOT, s.path));
      out.push({ ...s, bytes: st.size, available: st.isFile() });
    } catch {
      out.push({ ...s, bytes: 0, available: false });
    }
  }
  return NextResponse.json({ samples: out });
}

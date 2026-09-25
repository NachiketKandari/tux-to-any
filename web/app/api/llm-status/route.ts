import { NextResponse } from "next/server";
import { getLLMStatus } from "@/lib/llm-status";

// GET /api/llm-status — do LLM-on runs reach a model from this server?
// Reports env-key presence only (never values): the web tmpdirs carry no
// .tuxgo.yaml, so the stock `onprem-vllm` profile applies and degrades to
// deterministic output when neither VLLM_API_KEY nor OPENROUTER_API_KEY
// is set. The UI uses this to warn upfront instead of looking broken
// after a run.
export async function GET() {
  return NextResponse.json(getLLMStatus());
}

// LLM key status — server-env presence check for the two stock profiles.
// Never leaks key values: only booleans + the env var names.
//
// The web server runs tuxconv inside a disposable job tmpdir with no
// .tuxgo.yaml, so the stock defaults apply: profile `onprem-vllm`
// (key = $VLLM_API_KEY) with `local-dev-openrouter` ($OPENROUTER_API_KEY)
// as the documented alternative. When neither env var is set, every
// LLM-on run degrades to deterministic output — the toggle still works,
// it just cannot reach a model. The UI fetches this once per page load
// to warn upfront instead of looking broken after the run.
export interface LLMStatus {
  anyKey: boolean;
  vllmKey: boolean;
  openrouterKey: boolean;
  profile: string;
  note: string;
}

export function getLLMStatus(): LLMStatus {
  const vllmKey = (process.env.VLLM_API_KEY ?? "").trim() !== "";
  const openrouterKey = (process.env.OPENROUTER_API_KEY ?? "").trim() !== "";
  const anyKey = vllmKey || openrouterKey;
  return {
    anyKey,
    vllmKey,
    openrouterKey,
    profile: "onprem-vllm",
    note: anyKey
      ? "API key resolves — LLM-on runs reach the model"
      : "no API key in server env (VLLM_API_KEY / OPENROUTER_API_KEY) — LLM-on runs fall back to deterministic output; set a key and restart the web server for AI fills",
  };
}

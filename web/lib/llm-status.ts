// LLM key status — server-env + server-yaml presence check for the
// configured profiles. Never leaks key values: only booleans + the env var
// names + the config source label.
//
// Resolution mirrors the CLI exactly (internal/config Model.ResolveKey):
// the env var wins, the yaml literal (models[].apiKey in the TUXGO_CONFIG
// file) is the fallback. Without TUXGO_CONFIG the stock defaults apply:
// profile `onprem-vllm` (key = $VLLM_API_KEY) with `local-dev-openrouter`
// ($OPENROUTER_API_KEY) as the documented alternative. When no key resolves
// for the active profile, every LLM-on run degrades to deterministic output
// — the toggle still works, it just cannot reach a model. The UI fetches
// this once per page load to warn upfront instead of looking broken after
// the run.
import { modelResolves, readServerConfigSnapshot } from "@/lib/server-config";

export interface LLMStatus {
  anyKey: boolean;
  vllmKey: boolean;
  openrouterKey: boolean;
  profile: string;
  config: string;
  note: string;
}

function envPresent(name: string): boolean {
  return (process.env[name] ?? "").trim() !== "";
}

export function getLLMStatus(): LLMStatus {
  const snap = readServerConfigSnapshot();
  const byName = new Map((snap?.models ?? []).map((m) => [m.name, m]));
  const resolves = (name: string, env: string): boolean => {
    const m = byName.get(name);
    if (m) return modelResolves(m);
    return envPresent(env);
  };
  const vllmKey = resolves("onprem-vllm", "VLLM_API_KEY");
  const openrouterKey = resolves("local-dev-openrouter", "OPENROUTER_API_KEY");
  const profile = snap?.profile || "onprem-vllm";
  const active = byName.get(profile);
  const activeResolves = active ? modelResolves(active) : resolves(profile, profile === "local-dev-openrouter" ? "OPENROUTER_API_KEY" : "VLLM_API_KEY");
  const anyKey = activeResolves || vllmKey || openrouterKey || (snap?.models ?? []).some(modelResolves);
  const config = snap ? "yaml (TUXGO_CONFIG)" : "stock defaults";
  return {
    anyKey,
    vllmKey,
    openrouterKey,
    profile,
    config,
    note: anyKey
      ? `API key resolves (${config}) — LLM-on runs reach the model`
      : "no API key resolves (server env VLLM_API_KEY / OPENROUTER_API_KEY nor the TUXGO_CONFIG yaml literal) — LLM-on runs fall back to deterministic output; set a key or point TUXGO_CONFIG at a yaml with one and restart the web server for AI fills",
  };
}

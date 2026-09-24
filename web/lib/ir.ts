/** Shared IR accessors — the CLI schema uses mixed-case keys across versions. */

export function asArray(ir: Record<string, unknown> | undefined, keys: string[]): Record<string, unknown>[] {
  if (!ir) return [];
  // Direct hits first.
  for (const k of keys) {
    const v = ir[k];
    if (Array.isArray(v)) return v as Record<string, unknown>[];
  }
  // Case-insensitive fallback for robustness across CLI versions.
  const lower = new Map(Object.keys(ir).map((k) => [k.toLowerCase(), k]));
  for (const k of keys) {
    const hit = lower.get(k.toLowerCase());
    if (hit && Array.isArray(ir[hit])) return ir[hit] as Record<string, unknown>[];
  }
  return [];
}

export function str(v: unknown, fallback = "—"): string {
  if (v === undefined || v === null || v === "") return fallback;
  return String(v);
}

export function entryOf(ir: Record<string, unknown> | undefined): string {
  if (!ir) return "?";
  return str(ir.entry ?? ir.Entry ?? ir.service ?? "?", "?");
}

export function countKind(items: Record<string, unknown>[], keyCandidates: string[]): Map<string, number> {
  const m = new Map<string, number>();
  for (const o of items) {
    let kind = "other";
    for (const k of keyCandidates) {
      const v = o[k];
      if (typeof v === "string" && v) {
        kind = v;
        break;
      }
    }
    m.set(kind, (m.get(kind) ?? 0) + 1);
  }
  return m;
}

export function shortSql(v: unknown, n = 120): string {
  const s = str(v, "");
  const one = s.replace(/\s+/g, " ").trim();
  return one.length > n ? one.slice(0, n - 1) + "…" : one;
}

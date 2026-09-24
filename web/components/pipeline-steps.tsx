"use client";

import { Check } from "lucide-react";
import { cn } from "@/lib/utils";

export const PIPELINE_STEPS = [
  { id: "upload", label: "Upload" },
  { id: "overview", label: "Overview" },
  { id: "scenarios", label: "Scenarios" },
  { id: "mapping", label: "Mapping" },
  { id: "convert", label: "Convert" },
  { id: "tests", label: "Tests" },
] as const;

export function PipelineSteps({
  active,
  done,
  onGo,
}: {
  active: string;
  done: Set<string>;
  onGo: (id: string) => void;
}) {
  return (
    <ol className="flex flex-wrap items-center gap-1.5">
      {PIPELINE_STEPS.map((s, i) => {
        const isDone = done.has(s.id) || (s.id === "upload" && done.size > 0);
        const isActive = active === s.id;
        return (
          <li key={s.id} className="flex items-center gap-1.5">
            {i > 0 && <span className="mx-0.5 h-px w-4 bg-border" aria-hidden />}
            <button
              onClick={() => onGo(s.id)}
              className={cn(
                "flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition-colors",
                isActive
                  ? "border-primary bg-primary text-primary-foreground"
                  : isDone
                    ? "border-primary/40 bg-primary/10 text-foreground hover:bg-primary/15"
                    : "border-input bg-background text-muted-foreground hover:bg-accent"
              )}
            >
              {isDone && !isActive ? <Check className="h-3 w-3" /> : <span className="tabular-nums">{i + 1}</span>}
              {s.label}
            </button>
          </li>
        );
      })}
    </ol>
  );
}

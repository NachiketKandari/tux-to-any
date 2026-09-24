"use client";

import { GitBranch, Lightbulb } from "lucide-react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Progress } from "@/components/ui/progress";
import { ScrollArea } from "@/components/ui/scroll-area";
import type { FlowReport } from "@/lib/jobs";

export function FlowExplorer({ flowText, flowReport }: { flowText?: string; flowReport?: FlowReport }) {
  const fns = flowReport?.files.flatMap((f) => f.functions) ?? [];

  if (!flowReport && !flowText) {
    return <p className="text-sm text-muted-foreground">Upload a file to inspect its flow tree.</p>;
  }

  return (
    <div className="space-y-3">
      {fns.length > 0 && (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          {fns.map((fn) => {
            const pct =
              fn.coverage && fn.coverage.codeLines > 0
                ? Math.round((fn.coverage.classified / fn.coverage.codeLines) * 100)
                : 100;
            return (
              <Card key={fn.name}>
                <CardHeader className="pb-2">
                  <CardTitle className="flex items-center gap-2 font-mono text-sm">
                    <GitBranch className="h-4 w-4 text-muted-foreground" />
                    {fn.name}
                  </CardTitle>
                  <CardDescription>
                    {fn.startLine && fn.endLine ? (
                      <>
                        lines {fn.startLine}–{fn.endLine} ·{" "}
                      </>
                    ) : null}
                    {fn.coverage ? (
                      <>
                        {fn.coverage.classified}/{fn.coverage.codeLines} classified ({pct}%)
                      </>
                    ) : (
                      "no coverage data"
                    )}
                  </CardDescription>
                </CardHeader>
                <CardContent className="space-y-2">
                  {fn.coverage && <Progress value={pct} />}
                  {(fn.hints?.length ?? 0) > 0 ? (
                    <ul className="space-y-1">
                      {fn.hints!.slice(0, 5).map((h, i) => (
                        <li key={i} className="flex items-start gap-1.5 text-xs">
                          <Lightbulb className="mt-0.5 h-3.5 w-3.5 shrink-0 text-chart-3" />
                          <span>
                            <Badge variant="outline" className="mr-1 font-mono text-[10px]">
                              L{h.line}
                            </Badge>
                            <span className="font-mono text-[10px] text-muted-foreground">[{h.kind}]</span> {h.detail}
                          </span>
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <p className="text-[11px] text-muted-foreground">No idiom hints — clean tree.</p>
                  )}
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">Raw flow output</CardTitle>
          <CardDescription>Verbatim CLI text — the visual cards above are derived from flow.json.</CardDescription>
        </CardHeader>
        <CardContent>
          <ScrollArea className="max-h-[320px]">
            <pre className="whitespace-pre-wrap p-3 font-mono text-xs">{flowText ?? "(no flow output)"}</pre>
          </ScrollArea>
        </CardContent>
      </Card>
    </div>
  );
}

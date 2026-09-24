"use client";

import * as React from "react";
import { FlaskConical, Loader2, Play } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ScrollArea } from "@/components/ui/scroll-area";

export function TestsPanel({
  busy,
  canRun,
  gap,
  testFiles,
  onGap,
  onGenerate,
}: {
  busy: boolean;
  canRun: boolean;
  gap?: string;
  testFiles?: string[];
  onGap: () => void;
  onGenerate: () => void;
}) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="flex items-center gap-2 text-sm">
            <FlaskConical className="h-4 w-4" />
            Tests — gap report & generation
          </CardTitle>
          <div className="ml-auto flex gap-2">
            <Button size="sm" variant="secondary" disabled={busy || !canRun} onClick={onGap}>
              {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
              Gap report
            </Button>
            <Button size="sm" disabled={busy || !canRun} onClick={onGenerate}>
              <Play className="h-3.5 w-3.5" />
              Generate tests
            </Button>
          </div>
        </div>
        <CardDescription>
          {canRun
            ? "Targets the converted Go tree — db stores via sqlmock, handlers via gomock suites."
            : "gentest targets converted Go trees — convert to Go first."}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ScrollArea className="max-h-[420px]">
          <pre className="whitespace-pre-wrap p-3 font-mono text-xs">{gap ?? "(no report yet)"}</pre>
        </ScrollArea>
        {(testFiles?.length ?? 0) > 0 && (
          <p className="mt-2 text-xs text-muted-foreground">{testFiles!.length} test files — see the Convert tab tree.</p>
        )}
      </CardContent>
    </Card>
  );
}

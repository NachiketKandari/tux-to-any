"use client";

import * as React from "react";
import { Hammer, Loader2, Braces, FileCode2, Container } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { FileTree, CodeView, FileTreeSkeleton, CodeViewSkeleton } from "@/components/files";
import { CONVERT_TARGETS, type ConvertTarget } from "@/lib/targets";
import { cn } from "@/lib/utils";

const ICONS: Record<ConvertTarget, React.ReactNode> = {
  go: <Braces className="h-4 w-4" />,
  py: <FileCode2 className="h-4 w-4" />,
  cs: <Container className="h-4 w-4" />,
};

export function ConvertPanel({
  target,
  onTarget,
  onConvert,
  busy,
  hasMapping,
  converted,
  onOpenFile,
  onSaveFile,
  savingFile,
  selFile,
  fileContent,
  loadingFile,
}: {
  target: ConvertTarget;
  onTarget: (t: ConvertTarget) => void;
  onConvert: () => void;
  busy: boolean;
  hasMapping: boolean;
  converted?: { target: string; files: string[]; summary: string };
  onOpenFile: (p: string) => void;
  onSaveFile: (path: string, content: string) => void;
  savingFile: boolean;
  selFile: string | null;
  fileContent: string;
  loadingFile: boolean;
}) {
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-3" role="radiogroup" aria-label="Conversion target">
        {CONVERT_TARGETS.map((t) => {
          const active = target === t.id;
          return (
            <button
              key={t.id}
              role="radio"
              aria-checked={active}
              onClick={() => onTarget(t.id)}
              className={cn(
                "rounded-xl border p-4 text-left transition-colors",
                active ? "border-primary bg-primary/5 shadow-sm" : "hover:border-primary/50 hover:bg-accent/50"
              )}
            >
              <div className="flex items-center gap-2">
                <span className={cn("flex h-8 w-8 items-center justify-center rounded-lg", active ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground")}>
                  {ICONS[t.id]}
                </span>
                <span className="text-sm font-bold">{t.title}</span>
                {active && <Badge className="ml-auto">selected</Badge>}
              </div>
              <p className="mt-2 text-xs text-muted-foreground">{t.blurb}</p>
              <p className="mt-1 font-mono text-[11px] text-muted-foreground">
                <span className="text-foreground">{t.command}</span> → {t.output}
              </p>
            </button>
          );
        })}
      </div>

      <Card>
        <CardHeader className="pb-2">
          <div className="flex flex-wrap items-center gap-2">
            <CardTitle className="flex items-center gap-2 text-sm">
              <Hammer className="h-4 w-4" />
              Convert — deterministic, gated
            </CardTitle>
            {busy ? (
              <Badge variant="secondary" className="flex items-center gap-1">
                <Loader2 className="h-3 w-3 animate-spin" /> converting…
              </Badge>
            ) : (
              converted && <Badge variant="secondary">{converted.files.length} files</Badge>
            )}
            {!hasMapping && (target === "go" || target === "cs") && (
              <Badge variant="outline">no mapping yet — first run drafts it</Badge>
            )}
            <Button size="sm" className="ml-auto" disabled={busy} onClick={onConvert}>
              {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Hammer className="h-3.5 w-3.5" />}
              Convert to {target === "go" ? "Go" : target === "py" ? "Python" : "C#"}
            </Button>
          </div>
          <CardDescription>
            Always <code className="font-mono">-no-llm</code> in the viewer — controller bodies render as
            deterministic drafts with SQL-fidelity gates. Bring your own key for the LLM seam in the CLI.
            Open a file to edit it in place; renames apply file-wide.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {converted && <pre className="mb-3 whitespace-pre-wrap font-mono text-xs text-muted-foreground">{converted.summary}</pre>}
          <div className="grid grid-cols-1 gap-3 md:grid-cols-[280px_1fr]">
            <ScrollArea className="max-h-[480px]">
              {busy && !converted ? (
                <FileTreeSkeleton />
              ) : (
                <FileTree files={converted?.files ?? []} selected={selFile} onSelect={onOpenFile} />
              )}
            </ScrollArea>
            <ScrollArea className="max-h-[480px]">
              {loadingFile ? (
                <CodeViewSkeleton />
              ) : selFile ? (
                <CodeView
                  content={fileContent}
                  path={selFile}
                  editable
                  saving={savingFile}
                  onSave={(next) => onSaveFile(selFile, next)}
                />
              ) : (
                <p className="p-3 text-xs text-muted-foreground">Select a file to view it.</p>
              )}
            </ScrollArea>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

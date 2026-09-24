"use client";

import * as React from "react";
import { GitFork, Play, Loader2, FlaskConical } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { CodeView } from "@/components/files";
import type { ScenarioBundle } from "@/lib/jobs";
import { previewFilter } from "@/lib/api-client";

function parseScenarioName(name: string): { key: string; value: string } {
  // SVC_X.trn_cd_A.pc → key "trn_cd=A"
  const base = name.replace(/\.pc$/i, "");
  const dot = base.indexOf(".");
  const rest = dot >= 0 ? base.slice(dot + 1) : base;
  const us = rest.lastIndexOf("_");
  if (us > 0) return { key: `${rest.slice(0, us)}=${rest.slice(us + 1)}`, value: rest.slice(us + 1) };
  return { key: rest, value: rest };
}

export function ScenarioExplorer({
  jobId,
  bundle,
  busy,
  onBuild,
}: {
  jobId: string;
  bundle?: ScenarioBundle;
  busy: boolean;
  onBuild: () => void;
}) {
  const [sel, setSel] = React.useState<string | null>(null);
  const [expr, setExpr] = React.useState("");
  const [preview, setPreview] = React.useState("");
  const [previewBusy, setPreviewBusy] = React.useState(false);

  React.useEffect(() => {
    if (bundle && bundle.artifacts.length > 0 && !sel) setSel(bundle.artifacts[0].name);
  }, [bundle, sel]);

  async function runPreview() {
    if (!expr.trim() || previewBusy) return;
    setPreviewBusy(true);
    try {
      const d = await previewFilter(jobId, expr.trim());
      setPreview(d.output);
    } catch (e) {
      setPreview(e instanceof Error ? e.message : "preview failed");
    } finally {
      setPreviewBusy(false);
    }
  }

  if (!bundle) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-sm">
            <GitFork className="h-4 w-4" />
            Scenarios — one slice per dispatch value
          </CardTitle>
          <CardDescription>
            The entry folds on its dispatch axis (e.g. trn_cd). Each arm becomes an isolated slice with its own
            FML census and queries — the unit a mapping endpoint covers.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button size="sm" disabled={busy} onClick={onBuild}>
            {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
            Build scenarios
          </Button>
        </CardContent>
      </Card>
    );
  }

  const selected = bundle.artifacts.find((a) => a.name === sel);

  return (
    <div className="space-y-3">
      <Card>
        <CardHeader className="pb-2">
          <div className="flex flex-wrap items-center gap-2">
            <CardTitle className="flex items-center gap-2 text-sm">
              <GitFork className="h-4 w-4" />
              Dispatch axis
            </CardTitle>
            <Badge variant="secondary">{bundle.artifacts.length} slices</Badge>
            <Button size="sm" variant="outline" className="ml-auto" disabled={busy} onClick={onBuild}>
              {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
              Rebuild
            </Button>
          </div>
          {bundle.note && <CardDescription>{bundle.note}</CardDescription>}
        </CardHeader>
        <CardContent>
          <ScrollArea className="max-h-[180px]">
            <pre className="whitespace-pre-wrap p-3 font-mono text-[11px]">{bundle.axesText || "(no axes output)"}</pre>
          </ScrollArea>
        </CardContent>
      </Card>

      {bundle.artifacts.length > 0 && (
        <div className="grid grid-cols-1 gap-3 md:grid-cols-[260px_1fr]">
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm">Slices</CardTitle>
              <CardDescription>Pick a value to see its flattened source.</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-wrap gap-1.5 md:flex-col md:items-stretch">
              {bundle.artifacts.map((a) => {
                const { key } = parseScenarioName(a.name);
                const active = sel === a.name;
                return (
                  <button
                    key={a.name}
                    onClick={() => setSel(a.name)}
                    className={`rounded-md border px-2.5 py-1.5 text-left font-mono text-xs transition-colors ${
                      active ? "border-primary bg-primary text-primary-foreground" : "hover:bg-accent"
                    }`}
                    title={a.name}
                  >
                    {key}
                  </button>
                );
              })}
            </CardContent>
          </Card>
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="font-mono text-sm">{selected?.name}</CardTitle>
              <CardDescription>Flattened .pc — preamble + folded body with provenance prefixes.</CardDescription>
            </CardHeader>
            <CardContent>
              <ScrollArea className="max-h-[420px]">
                {selected ? (
                  <CodeView content={selected.content} path={selected.name} />
                ) : (
                  <p className="p-3 text-xs text-muted-foreground">Select a slice.</p>
                )}
              </ScrollArea>
            </CardContent>
          </Card>
        </div>
      )}

      {bundle.sharedMd && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Shared vs unique blocks</CardTitle>
            <CardDescription>What every slice shares, and what makes each arm unique.</CardDescription>
          </CardHeader>
          <CardContent>
            <ScrollArea className="max-h-[240px]">
              <pre className="whitespace-pre-wrap p-3 font-mono text-[11px]">{bundle.sharedMd}</pre>
            </ScrollArea>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="flex items-center gap-2 text-sm">
            <FlaskConical className="h-4 w-4" />
            scenarioFilter playground
          </CardTitle>
          <CardDescription>
            Fold one endpoint over several arms, e.g. <code className="font-mono">c_flag == &apos;F&apos; || c_flag == &apos;I&apos;</code>.
            Read-only preview — no artifacts written.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
          <div className="flex gap-2">
            <Input
              value={expr}
              onChange={(e) => setExpr(e.target.value)}
              placeholder="c_flag == 'F' || c_flag == 'I'"
              className="font-mono text-xs"
              onKeyDown={(e) => {
                if (e.key === "Enter") runPreview();
              }}
            />
            <Button size="sm" disabled={previewBusy || !expr.trim()} onClick={runPreview}>
              {previewBusy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
              Preview
            </Button>
          </div>
          {preview && (
            <ScrollArea className="max-h-[240px]">
              <pre className="whitespace-pre-wrap p-3 font-mono text-[11px]">{preview}</pre>
            </ScrollArea>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

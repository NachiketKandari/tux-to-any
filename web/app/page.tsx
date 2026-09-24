"use client";

import * as React from "react";
import {
  LayoutDashboard,
  FileJson,
  GitBranch,
  GitFork,
  PencilLine,
  Hammer,
  FlaskConical,
  ScrollText,
  FileCode2,
  Loader2,
} from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { PipelineSteps } from "@/components/pipeline-steps";
import { Dropzone } from "@/components/dropzone";
import { SamplesPanel } from "@/components/samples-panel";
import { OverviewDashboard } from "@/components/overview-dashboard";
import { IRExplorer } from "@/components/ir-explorer";
import { FlowExplorer } from "@/components/flow-explorer";
import { ScenarioExplorer } from "@/components/scenario-explorer";
import { MappingEditor } from "@/components/mapping-editor";
import { ConvertPanel } from "@/components/convert-panel";
import { TestsPanel } from "@/components/tests-panel";
import { CodeView } from "@/components/files";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { useJob } from "@/hooks/use-job";
import {
  uploadFile,
  loadSample,
  discover,
  buildScenarios,
  saveMapping,
  convert,
  gentest,
  readConvertedFile,
} from "@/lib/api-client";
import type { ConvertTarget } from "@/lib/targets";

type TabId = "overview" | "ir" | "flow" | "scenarios" | "mapping" | "convert" | "tests" | "logs" | "source";

export default function Home() {
  const [jobId, setJobId] = React.useState<string | null>(null);
  const { job, setJob, logs, resetLogs } = useJob(jobId);
  const [tab, setTab] = React.useState<TabId>("overview");
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState("");
  const [target, setTarget] = React.useState<ConvertTarget>("go");
  const [selFile, setSelFile] = React.useState<string | null>(null);
  const [fileContent, setFileContent] = React.useState("");

  async function run<T>(fn: () => Promise<T>): Promise<T | null> {
    setBusy(true);
    setErr("");
    try {
      return await fn();
    } catch (e) {
      setErr(e instanceof Error ? e.message : "request failed");
      return null;
    } finally {
      setBusy(false);
      if (jobId) {
        const r = await fetch(`/api/jobs/${jobId}`);
        if (r.ok) setJob(await r.json());
      }
    }
  }

  async function handleUpload(f: File) {
    resetLogs();
    setSelFile(null);
    const d = await run(() => uploadFile(f));
    if (d) {
      setJobId(d.id);
      setTab("overview");
    }
  }

  async function handleSample(path: string) {
    resetLogs();
    setSelFile(null);
    const d = await run(() => loadSample(path));
    if (d) {
      setJobId(d.id);
      setTab("overview");
    }
  }

  async function handleDiscover(t: "go" | "cs") {
    if (!job) return;
    const j = await run(() => discover(job.id, t));
    if (j) {
      setJob(j);
      setTab("mapping");
    }
  }

  async function handleScenarios() {
    if (!job) return;
    const j = await run(() => buildScenarios(job.id));
    if (j) setJob(j);
  }

  async function handleSaveMapping(path: string, content: string) {
    if (!job) return;
    await run(async () => {
      await saveMapping(job.id, path, content);
      return null;
    });
    setTab("convert");
  }

  async function handleConvert() {
    if (!job) return;
    setSelFile(null);
    setFileContent("");
    const res = await run(() => convert(job.id, target));
    if (res) {
      setJob(res.job);
      // Draft-and-stop (no mapping yet): take the user to the fresh draft.
      if (!res.job.converted && (res.note || res.drafts)) {
        setTab("mapping");
        setErr("No mapping yet — review the draft in the Mapping tab, save, then convert again.");
      }
    }
  }

  async function handleGentest(mode: "check" | "generate") {
    if (!job) return;
    const j = await run(() => gentest(job.id, mode));
    if (j) setJob(j);
  }

  async function openFile(p: string) {
    if (!job) return;
    setSelFile(p);
    try {
      setFileContent(await readConvertedFile(job.id, p));
    } catch (e) {
      setFileContent(`// ${e instanceof Error ? e.message : "cannot read file"}`);
    }
  }

  const done = React.useMemo(() => {
    const s = new Set<string>();
    if (!job) return s;
    s.add("upload");
    if (job.ir) s.add("overview");
    if (job.scenarios) s.add("scenarios");
    if (job.drafts?.length || job.mappingPath) s.add("mapping");
    if (job.converted) s.add("convert");
    if (job.gentestGap) s.add("tests");
    return s;
  }, [job]);

  return (
    <div className="min-h-screen">
      <SiteHeader jobName={job?.name} status={job?.status} />

      <main className="container max-w-7xl py-6">
        {/* Hero */}
        <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
          <div>
            <h1 className="text-2xl font-bold tracking-tight">Drop a Pro*C file — watch it become an API</h1>
            <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
              Tree-sitter parse → IR breakdown → dispatch-axis scenarios → mapping →{" "}
              <span className="font-medium text-foreground">Tux → Go · Python · C#</span> → tests. Deterministic
              in the browser; the LLM seam stays in the CLI.
            </p>
          </div>
          {job && (
            <div className="flex items-center gap-2">
              <Badge variant="secondary" className="font-mono">
                {job.name}
              </Badge>
              {busy && (
                <span className="flex items-center gap-1 text-xs text-muted-foreground">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" /> working…
                </span>
              )}
            </div>
          )}
        </div>

        <div className="mb-4">
          <PipelineSteps active={job ? tab : "upload"} done={done} onGo={(id) => setTab(id as TabId)} />
        </div>

        {!job ? (
          <div className="grid grid-cols-1 gap-3 lg:grid-cols-[1fr_380px]">
            <Dropzone onFile={handleUpload} disabled={busy} />
            <SamplesPanel busy={busy} onPick={handleSample} />
          </div>
        ) : (
          <Card className="mb-4">
            <CardContent className="flex flex-wrap items-center gap-2 p-4">
              <Dropzone onFile={handleUpload} disabled={busy} compact />
              <span className="text-xs text-muted-foreground">…or start over with a new file. Jobs are disposable tmpdirs.</span>
            </CardContent>
          </Card>
        )}

        {err && (
          <Alert variant="destructive" className="mb-4">
            <AlertDescription>{err}</AlertDescription>
          </Alert>
        )}

        {job && (
          <Tabs value={tab} onValueChange={(v) => setTab(v as TabId)}>
            <TabsList className="flex h-auto flex-wrap justify-start gap-1">
              <TabsTrigger value="overview">
                <LayoutDashboard className="h-3.5 w-3.5" /> Overview
              </TabsTrigger>
              <TabsTrigger value="ir">
                <FileJson className="h-3.5 w-3.5" /> IR
              </TabsTrigger>
              <TabsTrigger value="flow">
                <GitBranch className="h-3.5 w-3.5" /> Flow
              </TabsTrigger>
              <TabsTrigger value="scenarios">
                <GitFork className="h-3.5 w-3.5" /> Scenarios
              </TabsTrigger>
              <TabsTrigger value="mapping">
                <PencilLine className="h-3.5 w-3.5" /> Mapping
              </TabsTrigger>
              <TabsTrigger value="convert">
                <Hammer className="h-3.5 w-3.5" /> Convert
              </TabsTrigger>
              <TabsTrigger value="tests">
                <FlaskConical className="h-3.5 w-3.5" /> Tests
              </TabsTrigger>
              <TabsTrigger value="source">
                <FileCode2 className="h-3.5 w-3.5" /> Source
              </TabsTrigger>
              <TabsTrigger value="logs">
                <ScrollText className="h-3.5 w-3.5" /> Logs
              </TabsTrigger>
            </TabsList>

            <TabsContent value="overview">
              <OverviewDashboard ir={job.ir} flowReport={job.flowReport} />
            </TabsContent>

            <TabsContent value="ir">
              <IRExplorer ir={job.ir} />
            </TabsContent>

            <TabsContent value="flow">
              <FlowExplorer flowText={job.flowText} flowReport={job.flowReport} />
            </TabsContent>

            <TabsContent value="scenarios">
              <ScenarioExplorer jobId={job.id} bundle={job.scenarios} busy={busy} onBuild={handleScenarios} />
            </TabsContent>

            <TabsContent value="mapping">
              <MappingEditor drafts={job.drafts ?? []} busy={busy} onDraft={handleDiscover} onSave={handleSaveMapping} />
            </TabsContent>

            <TabsContent value="convert">
              <ConvertPanel
                target={target}
                onTarget={setTarget}
                onConvert={handleConvert}
                busy={busy}
                hasMapping={!!job.mappingPath}
                converted={job.converted}
                onOpenFile={openFile}
                selFile={selFile}
                fileContent={fileContent}
              />
            </TabsContent>

            <TabsContent value="tests">
              <TestsPanel
                busy={busy}
                canRun={job.converted?.target === "go"}
                gap={job.gentestGap}
                testFiles={job.gentestFiles}
                onGap={() => handleGentest("check")}
                onGenerate={() => handleGentest("generate")}
              />
            </TabsContent>

            <TabsContent value="source">
              <Card>
                <CardContent className="p-0">
                  <ScrollArea className="max-h-[560px] border-0">
                    <CodeView content={job.sourcePreview ?? "(no preview)"} path={job.name} />
                  </ScrollArea>
                </CardContent>
              </Card>
            </TabsContent>

            <TabsContent value="logs">
              <Card>
                <CardContent className="p-0">
                  <ScrollArea className="max-h-[480px] border-0">
                    <pre className="whitespace-pre-wrap p-3 font-mono text-xs">
                      {logs.length ? logs.join("\n") : "(no log lines yet)"}
                    </pre>
                  </ScrollArea>
                  <div className="border-t p-2">
                    <Button size="sm" variant="ghost" onClick={() => navigator.clipboard.writeText(logs.join("\n"))}>
                      Copy logs
                    </Button>
                  </div>
                </CardContent>
              </Card>
            </TabsContent>
          </Tabs>
        )}

        <footer className="mt-8 border-t pt-4 text-[11px] text-muted-foreground">
          Deterministic viewer — every run shells out to <code className="font-mono">tuxconv</code> with{" "}
          <code className="font-mono">-no-llm</code>. Jobs are in-memory tmpdirs; re-upload after a restart.
        </footer>
      </main>
    </div>
  );
}

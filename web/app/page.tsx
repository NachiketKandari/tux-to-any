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
  Activity,
  Waypoints,
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
import { LineageExplorer } from "@/components/lineage-explorer";
import { TestsPanel } from "@/components/tests-panel";
import { MetricsPanel } from "@/components/metrics-panel";
import { CodeView } from "@/components/files";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { useJob } from "@/hooks/use-job";
import { useLineage } from "@/hooks/use-lineage";
import { provenanceForFile } from "@/lib/lineage";
import {
  uploadFile,
  loadSample,
  discover,
  buildScenarios,
  saveMapping,
  convert,
  gentest,
  readConvertedFile,
  saveConvertedFile,
  fetchDbStatus,
  fetchLLMStatus,
  type LLMStatus,
} from "@/lib/api-client";
import type { ConvertTarget } from "@/lib/targets";

type TabId = "overview" | "ir" | "flow" | "scenarios" | "mapping" | "convert" | "trace" | "tests" | "metrics" | "logs" | "source";

/** Map the 11-tab model onto the 6-step pipeline strip: IR/Flow read as
 *  Overview detail, Trace reads as Convert detail, Source/Logs/Metrics sit
 *  outside the linear flow. */
function stepperStepForTab(tab: TabId): string {
  if (tab === "ir" || tab === "flow") return "overview";
  if (tab === "trace") return "convert";
  if (tab === "source" || tab === "logs" || tab === "metrics") return "upload";
  return tab;
}

export default function Home() {
  const [jobId, setJobId] = React.useState<string | null>(null);
  const { job, setJob, logs, resetLogs } = useJob(jobId);
  const [tab, setTab] = React.useState<TabId>("overview");
  const [busy, setBusy] = React.useState(false);
  const [err, setErr] = React.useState("");
  const [target, setTarget] = React.useState<ConvertTarget>("go");
  const [useLLMMapping, setUseLLMMapping] = React.useState(false);
  const [useLLMConvert, setUseLLMConvert] = React.useState(false);
  const [dbStatus, setDbStatus] = React.useState<{ enabled: boolean; driver: string; source: string } | null>(null);
  const [llmStatus, setLlmStatus] = React.useState<LLMStatus | null>(null);
  const [selFile, setSelFile] = React.useState<string | null>(null);
  const [fileContent, setFileContent] = React.useState("");
  const [loadingFile, setLoadingFile] = React.useState(false);
  const [savingFile, setSavingFile] = React.useState(false);

  // Source→output trace: refetch whenever a new converted tree lands.
  const lineageStamp = job?.converted ? `${job.id}:${job.converted.target}:${job.converted.files.length}` : "";
  const { lineage, loading: lineageLoading, error: lineageError } = useLineage(jobId, lineageStamp);
  const selProvenance = React.useMemo(
    () => (selFile ? provenanceForFile(lineage, selFile) : []),
    [lineage, selFile]
  );

  React.useEffect(() => {
    fetchDbStatus().then(setDbStatus).catch(() => setDbStatus(null));
    fetchLLMStatus().then(setLlmStatus).catch(() => setLlmStatus(null));
  }, []);

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
    const j = await run(() => discover(job.id, t, useLLMMapping));
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
    const ok = await run(async () => {
      await saveMapping(job.id, path, content);
      return true;
    });
    if (ok) setTab("convert");
  }

  async function handleConvert() {
    if (!job) return;
    // Mapping-first: Go/C# need a reviewed mapping (Step 1). Python converts
    // directly. The backend keeps draft-and-stop as a safety net, but the UI
    // enforces the order so conversion never silently drafts.
    if ((target === "go" || target === "cs") && !job.mappingPath) {
      setTab("mapping");
      setErr("Step 1 first — draft a mapping, save it, then convert (Step 2).");
      return;
    }
    setSelFile(null);
    setFileContent("");
    const res = await run(() => convert(job.id, target, useLLMConvert));
    if (res) {
      setJob(res.job);
      // Draft-and-stop safety net (e.g. mapping deleted mid-run).
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
    setLoadingFile(true);
    try {
      setFileContent(await readConvertedFile(job.id, p));
    } catch (e) {
      setFileContent(`// ${e instanceof Error ? e.message : "cannot read file"}`);
    } finally {
      setLoadingFile(false);
    }
  }

  async function openTraceFile(p: string) {
    setTab("convert");
    await openFile(p);
  }

  async function handleSaveFile(p: string, content: string) {
    if (!job) return;
    setSavingFile(true);
    try {
      await saveConvertedFile(job.id, p, content);
      setFileContent(content);
    } catch (e) {
      setErr(e instanceof Error ? e.message : "save failed");
    } finally {
      setSavingFile(false);
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
      <SiteHeader
        jobName={job?.name}
        status={job?.status}
        llmMapping={useLLMMapping}
        llmConvert={useLLMConvert}
        dbEnabled={dbStatus?.enabled ?? false}
        llmKey={llmStatus ? llmStatus.anyKey : null}
      />

      <main className="container max-w-7xl py-6">
        {/* Hero */}
        <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
          <div>
            <h1 className="text-2xl font-bold tracking-tight">Drop a Pro*C file — watch it become an API</h1>
            <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
              Tree-sitter parse → IR breakdown → dispatch-axis scenarios →{" "}
              <span className="font-medium text-foreground">Step 1 mapping</span> →{" "}
              <span className="font-medium text-foreground">Step 2 Tux → Go · Python · C#</span> → tests.
              LLM off works with no keys; LLM on upgrades where a key resolves, otherwise falls back.
              Oracle access is optional — offline by default.
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
          <PipelineSteps
            active={job ? stepperStepForTab(tab) : "upload"}
            done={done}
            onGo={(id) => {
              // The stepper only models the 6 pipeline stages; IR/Flow are
              // sub-views of Overview and Source/Logs sit outside the flow.
              // Never leave Tabs in a blank "upload" state.
              if (id === "upload") setTab("overview");
              else setTab(id as TabId);
            }}
          />
        </div>

        {!job ? (
          busy ? (
            <div className="grid grid-cols-1 gap-3 lg:grid-cols-[1fr_380px]" aria-label="Loading job">
              <Card>
                <CardContent className="space-y-2 p-4">
                  <Skeleton className="h-5 w-1/3" />
                  <Skeleton className="h-4 w-full" />
                  <Skeleton className="h-4 w-5/6" />
                  <Skeleton className="h-32 w-full" />
                </CardContent>
              </Card>
              <Card>
                <CardContent className="space-y-2 p-4">
                  <Skeleton className="h-5 w-1/2" />
                  <Skeleton className="h-8 w-full" />
                  <Skeleton className="h-8 w-full" />
                </CardContent>
              </Card>
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-3 lg:grid-cols-[1fr_380px]">
              <Dropzone onFile={handleUpload} disabled={busy} />
              <SamplesPanel busy={busy} onPick={handleSample} />
            </div>
          )
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
            <AlertDescription className="flex flex-wrap items-center gap-2">
              <span className="flex-1">{err}</span>
              <Button size="sm" variant="ghost" className="h-6 px-2 text-xs" onClick={() => setErr("")}>
                Dismiss
              </Button>
            </AlertDescription>
          </Alert>
        )}

        {job && (
          <Tabs value={tab} onValueChange={(v) => setTab(v as TabId)}>
            <TabsList className="flex h-auto max-w-full flex-wrap justify-start gap-1 overflow-x-auto">
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
                {(job.scenarios?.artifacts.length ?? 0) > 0 && (
                  <Badge variant="secondary" className="ml-1 px-1.5 py-0 text-[10px] tabular-nums">
                    {job.scenarios?.artifacts.length}
                  </Badge>
                )}
              </TabsTrigger>
              <TabsTrigger value="mapping">
                <PencilLine className="h-3.5 w-3.5" /> Mapping
                {(job.drafts?.length ?? 0) > 0 && (
                  <Badge variant="secondary" className="ml-1 px-1.5 py-0 text-[10px] tabular-nums">
                    {job.drafts?.length}
                  </Badge>
                )}
              </TabsTrigger>
              <TabsTrigger value="convert">
                <Hammer className="h-3.5 w-3.5" /> Convert
                {(job.converted?.files.length ?? 0) > 0 && (
                  <Badge variant="secondary" className="ml-1 px-1.5 py-0 text-[10px] tabular-nums">
                    {job.converted?.files.length}
                  </Badge>
                )}
              </TabsTrigger>
              <TabsTrigger value="trace">
                <Waypoints className="h-3.5 w-3.5" /> Trace
                {(lineage?.nodes.length ?? 0) > 0 && (
                  <Badge variant="secondary" className="ml-1 px-1.5 py-0 text-[10px] tabular-nums">
                    {lineage?.nodes.length}
                  </Badge>
                )}
              </TabsTrigger>
              <TabsTrigger value="tests">
                <FlaskConical className="h-3.5 w-3.5" /> Tests
                {(job.gentestFiles?.length ?? 0) > 0 && (
                  <Badge variant="secondary" className="ml-1 px-1.5 py-0 text-[10px] tabular-nums">
                    {job.gentestFiles?.length}
                  </Badge>
                )}
              </TabsTrigger>
              <TabsTrigger value="metrics">
                <Activity className="h-3.5 w-3.5" /> Metrics
              </TabsTrigger>
              <TabsTrigger value="source">
                <FileCode2 className="h-3.5 w-3.5" /> Source
              </TabsTrigger>
              <TabsTrigger value="logs">
                <ScrollText className="h-3.5 w-3.5" /> Logs
              </TabsTrigger>
            </TabsList>

            <TabsContent value="overview">
              <OverviewDashboard
                ir={job.ir}
                flowReport={job.flowReport}
                onGoMetrics={() => setTab("metrics")}
                onGoTrace={job.converted ? () => setTab("trace") : undefined}
              />
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
              <MappingEditor
                drafts={job.drafts ?? []}
                busy={busy}
                onDraft={handleDiscover}
                onSave={handleSaveMapping}
                useLLM={useLLMMapping}
                onUseLLM={setUseLLMMapping}
                llmEffective={job.mappingLLMEffective}
                llmNote={job.mappingLLMNote}
                noKey={llmStatus ? !llmStatus.anyKey : false}
              />
            </TabsContent>

            <TabsContent value="convert">
              <ConvertPanel
                target={target}
                onTarget={setTarget}
                onConvert={handleConvert}
                busy={busy}
                hasMapping={!!job.mappingPath}
                useLLM={useLLMConvert}
                onUseLLM={setUseLLMConvert}
                onGoMapping={() => setTab("mapping")}
                converted={job.converted}
                onOpenFile={openFile}
                onSaveFile={handleSaveFile}
                savingFile={savingFile}
                selFile={selFile}
                fileContent={fileContent}
                loadingFile={loadingFile}
                provenance={selProvenance}
                onGoTrace={() => setTab("trace")}
                jobId={job.id}
                noKey={llmStatus ? !llmStatus.anyKey : false}
              />
            </TabsContent>

            <TabsContent value="trace">
              <LineageExplorer
                lineage={lineage}
                loading={lineageLoading}
                error={lineageError}
                onOpenFile={openTraceFile}
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
                onGoConvert={() => setTab("convert")}
              />
            </TabsContent>

            <TabsContent value="metrics">
              <MetricsPanel />
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
          Mapping-first flow — <span className="font-medium">Step 1</span> drafts & saves the mapping,{" "}
          <span className="font-medium">Step 2</span> converts. Each step has its own LLM toggle (off ={" "}
          <code className="font-mono">-no-llm</code>, no keys needed; on = LLM seam when a key resolves
          (<code className="font-mono">VLLM_API_KEY</code> / <code className="font-mono">OPENROUTER_API_KEY</code> in
          the server env), deterministic fallback otherwise — the panels report requested vs effective). Converted
          trees live in disposable server tmpdirs — use <span className="font-medium">Download .zip</span> for the
          durable copy. Oracle access is optional — offline unless a DSN is set
          (<code className="font-mono">tuxconv dbcheck</code>). Jobs are disposable tmpdirs; re-upload
          after a restart. Master totals accumulate in{" "}
          <code className="font-mono">conversion_logs/web-metrics.jsonl</code>.
        </footer>
      </main>
    </div>
  );
}

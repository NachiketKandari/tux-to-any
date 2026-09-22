"use client";
import * as React from "react";
import { FlaskConical, GitBranch, FileJson, ScrollText, Hammer, PencilLine, Loader2 } from "lucide-react";
import { Dropzone } from "@/components/dropzone";
import { FileTree, CodeView } from "@/components/files";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea, Select } from "@/components/ui/fields";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { ScrollArea, Alert } from "@/components/ui/misc";

interface JobState {
  id: string;
  name: string;
  status: "ready" | "running" | "done" | "error";
  error?: string;
  ir?: Record<string, unknown>;
  flowText?: string;
  drafts?: { path: string; content: string }[];
  mappingPath?: string;
  converted?: { target: string; root: string; files: string[]; summary: string };
  gentestGap?: string;
  gentestFiles?: string[];
}

function asArray(ir: Record<string, unknown> | undefined, keys: string[]): Record<string, unknown>[] {
  if (!ir) return [];
  for (const k of keys) {
    const v = ir[k];
    if (Array.isArray(v)) return v as Record<string, unknown>[];
  }
  return [];
}

function scalarRows(obj: Record<string, unknown>, maxCols = 6): [string, string][] {
  return Object.entries(obj)
    .filter(([, v]) => v === null || ["string", "number", "boolean"].includes(typeof v))
    .slice(0, maxCols)
    .map(([k, v]) => [k, String(v)]);
}

function DataTable({ items, empty }: { items: Record<string, unknown>[]; empty: string }) {
  if (items.length === 0) return <p className="text-xs text-muted-foreground">{empty}</p>;
  const cols = Array.from(new Set(items.flatMap((o) => scalarRows(o).map(([k]) => k)))).slice(0, 6);
  return (
    <Table>
      <THead>
        <TR>
          {cols.map((c) => (
            <TH key={c}>{c}</TH>
          ))}
        </TR>
      </THead>
      <TBody>
        {items.slice(0, 100).map((o, i) => (
          <TR key={i}>
            {cols.map((c) => (
              <TD key={c}>{o[c] === undefined || o[c] === null ? "—" : String(o[c])}</TD>
            ))}
          </TR>
        ))}
      </TBody>
    </Table>
  );
}

export default function Home() {
  const [job, setJob] = React.useState<JobState | null>(null);
  const [tab, setTab] = React.useState("ir");
  const [busy, setBusy] = React.useState(false);
  const [target, setTarget] = React.useState("go");
  const [draftIdx, setDraftIdx] = React.useState(0);
  const [mappingText, setMappingText] = React.useState("");
  const [selFile, setSelFile] = React.useState<string | null>(null);
  const [fileContent, setFileContent] = React.useState("");
  const [logs, setLogs] = React.useState<string[]>([]);
  const [err, setErr] = React.useState("");

  const refresh = React.useCallback(async (id: string) => {
    const r = await fetch(`/api/jobs/${id}`);
    if (r.ok) setJob(await r.json());
  }, []);

  React.useEffect(() => {
    if (!job || job.status !== "running") return;
    const t = setInterval(() => refresh(job.id), 1500);
    return () => clearInterval(t);
  }, [job, refresh]);

  React.useEffect(() => {
    if (!job) return;
    const t = setInterval(async () => {
      const r = await fetch(`/api/jobs/${job.id}/logs?since=${logsRef.current}`);
      if (r.ok) {
        const d = await r.json();
        if (d.lines?.length) setLogs((prev) => [...prev, ...d.lines]);
        logsRef.current = d.next;
      }
    }, 1500);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [job?.id]);
  const logsRef = React.useRef(0);

  async function upload(f: File) {
    setBusy(true);
    setErr("");
    setLogs([]);
    logsRef.current = 0;
    try {
      const fd = new FormData();
      fd.append("file", f);
      const r = await fetch("/api/jobs", { method: "POST", body: fd });
      const d = await r.json();
      if (!r.ok) throw new Error(d.error ?? "upload failed");
      await refresh(d.id);
      setTab("ir");
    } catch (e) {
      setErr(e instanceof Error ? e.message : "upload failed");
    } finally {
      setBusy(false);
    }
  }

  async function call(path: string, body?: unknown, method = "POST") {
    if (!job) return null;
    setBusy(true);
    setErr("");
    try {
      const r = await fetch(path, { method, headers: { "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
      const d = await r.json();
      if (!r.ok) throw new Error(d.error ?? "request failed");
      await refresh(job.id);
      return d;
    } catch (e) {
      setErr(e instanceof Error ? e.message : "request failed");
      return null;
    } finally {
      setBusy(false);
    }
  }

  async function openFile(p: string) {
    if (!job) return;
    setSelFile(p);
    const r = await fetch(`/api/jobs/${job.id}/files?path=${encodeURIComponent(p)}`);
    const d = await r.json();
    setFileContent(r.ok ? d.content : `// ${d.error}`);
  }

  const drafts = job?.drafts ?? [];
  React.useEffect(() => {
    setMappingText(drafts[draftIdx]?.content ?? "");
  }, [drafts, draftIdx]);

  const conditions = asArray(job?.ir, ["conditions", "Conditions"]);
  const queries = asArray(job?.ir, ["queries", "Queries", "queryUnits", "QueryUnits"]);
  const functions = asArray(job?.ir, ["functions", "Functions"]);
  const fmlOps = asArray(job?.ir, ["fml_ops", "fmlOps"]);
  const hostVars = asArray(job?.ir, ["host_vars", "hostVars"]);

  return (
    <main className="container max-w-6xl py-8">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">tux-to-any viewer</h1>
          <p className="text-sm text-muted-foreground">Drop a Pro*C file — watch it break down into IR, flow, mapping, code, tests.</p>
        </div>
        {job && <Badge variant="secondary">{job.status}</Badge>}
      </div>

      <Dropzone onFile={upload} disabled={busy} />
      {err && (
        <Alert className="mt-4 border-red-300 text-sm text-red-700">{err}</Alert>
      )}

      {job && (
        <Card className="mt-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              {busy && <Loader2 className="h-4 w-4 animate-spin" />}
              {job.name}
            </CardTitle>
            <CardDescription>
              entry <code>{String(job.ir?.entry ?? job.ir?.Entry ?? "?")}</code>
              {" · "}{conditions.length} conditions · {queries.length} queries · {functions.length} functions
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Tabs value={tab} onValueChange={setTab}>
              <TabsList>
                <TabsTrigger value="ir"><span className="flex items-center gap-1"><FileJson className="h-3.5 w-3.5" />IR</span></TabsTrigger>
                <TabsTrigger value="flow"><span className="flex items-center gap-1"><GitBranch className="h-3.5 w-3.5" />Flow</span></TabsTrigger>
                <TabsTrigger value="mapping"><span className="flex items-center gap-1"><PencilLine className="h-3.5 w-3.5" />Mapping</span></TabsTrigger>
                <TabsTrigger value="convert"><span className="flex items-center gap-1"><Hammer className="h-3.5 w-3.5" />Convert</span></TabsTrigger>
                <TabsTrigger value="tests"><span className="flex items-center gap-1"><FlaskConical className="h-3.5 w-3.5" />Tests</span></TabsTrigger>
                <TabsTrigger value="logs"><span className="flex items-center gap-1"><ScrollText className="h-3.5 w-3.5" />Logs</span></TabsTrigger>
              </TabsList>

              <TabsContent value="ir">
                <div className="space-y-4">
                  <div>
                    <h4 className="mb-2 text-sm font-semibold">Conditions ({conditions.length})</h4>
                    <DataTable items={conditions} empty="No conditions extracted." />
                  </div>
                  <div>
                    <h4 className="mb-2 text-sm font-semibold">Query units ({queries.length})</h4>
                    <DataTable items={queries} empty="No query units extracted." />
                  </div>
                  <div>
                    <h4 className="mb-2 text-sm font-semibold">Functions ({functions.length})</h4>
                    <DataTable items={functions} empty="No functions inventoried." />
                  </div>
                  <div>
                    <h4 className="mb-2 text-sm font-semibold">FML ops ({fmlOps.length})</h4>
                    <DataTable items={fmlOps} empty="No FML ops recorded." />
                  </div>
                  <div>
                    <h4 className="mb-2 text-sm font-semibold">Host vars ({hostVars.length})</h4>
                    <DataTable items={hostVars} empty="No host vars recorded." />
                  </div>
                </div>
              </TabsContent>

              <TabsContent value="flow">
                <ScrollArea className="max-h-[480px]">
                  <pre className="whitespace-pre-wrap p-3 font-mono text-xs">{job.flowText ?? "(upload a file first)"}</pre>
                </ScrollArea>
              </TabsContent>

              <TabsContent value="mapping">
                <div className="mb-3 flex flex-wrap items-center gap-2">
                  <Button size="sm" variant="secondary" disabled={busy} onClick={() => call(`/api/jobs/${job.id}/discover`, { target: "go" })}>Draft Go mapping</Button>
                  <Button size="sm" variant="secondary" disabled={busy} onClick={() => call(`/api/jobs/${job.id}/discover`, { target: "cs" })}>Draft C# mapping</Button>
                  {drafts.length > 1 && (
                    <Select value={String(draftIdx)} onChange={(e) => setDraftIdx(Number(e.target.value))}>
                      {drafts.map((d, i) => (
                        <option key={d.path} value={i}>{d.path.split("/").pop()}</option>
                      ))}
                    </Select>
                  )}
                  <Button
                    size="sm"
                    disabled={busy || !drafts[draftIdx]}
                    onClick={async () => {
                      const d = await call(`/api/jobs/${job.id}/mapping`, { path: drafts[draftIdx].path, content: mappingText }, "PUT");
                      if (d) setTab("convert");
                    }}
                  >
                    Save &amp; continue
                  </Button>
                </div>
                {drafts.length === 0 ? (
                  <p className="text-xs text-muted-foreground">No drafts yet — run one of the draft passes above, then review names/routes here.</p>
                ) : (
                  <Textarea rows={24} value={mappingText} onChange={(e) => setMappingText(e.target.value)} spellCheck={false} />
                )}
              </TabsContent>

              <TabsContent value="convert">
                <div className="mb-3 flex flex-wrap items-center gap-2">
                  <Select value={target} onChange={(e) => setTarget(e.target.value)}>
                    <option value="go">Go services</option>
                    <option value="py">Python batch</option>
                    <option value="cs">C# components</option>
                  </Select>
                  <Button
                    size="sm"
                    disabled={busy}
                    onClick={async () => {
                      setSelFile(null);
                      setFileContent("");
                      await call(`/api/jobs/${job.id}/convert`, { target });
                    }}
                  >
                    Convert{job.mappingPath ? "" : " (drafts first if unmapped)"}
                  </Button>
                  {job.converted && <Badge>{job.converted.files.length} files</Badge>}
                </div>
                {job.converted && <pre className="mb-3 whitespace-pre-wrap font-mono text-xs text-muted-foreground">{job.converted.summary}</pre>}
                <div className="grid grid-cols-1 gap-4 md:grid-cols-[240px_1fr]">
                  <ScrollArea className="max-h-[480px]">
                    <FileTree files={job.converted?.files ?? []} selected={selFile} onSelect={openFile} />
                  </ScrollArea>
                  <ScrollArea className="max-h-[480px]">
                    {selFile ? <CodeView content={fileContent} /> : <p className="p-3 text-xs text-muted-foreground">Select a file to view it.</p>}
                  </ScrollArea>
                </div>
              </TabsContent>

              <TabsContent value="tests">
                <div className="mb-3 flex items-center gap-2">
                  <Button size="sm" variant="secondary" disabled={busy || job.converted?.target !== "go"} onClick={() => call(`/api/jobs/${job.id}/gentest`, { mode: "check" })}>Gap report</Button>
                  <Button size="sm" disabled={busy || job.converted?.target !== "go"} onClick={() => call(`/api/jobs/${job.id}/gentest`, { mode: "generate" })}>Generate tests</Button>
                </div>
                {job.converted?.target !== "go" && <p className="mb-2 text-xs text-muted-foreground">gentest targets converted Go trees — convert to Go first.</p>}
                <ScrollArea className="max-h-[480px]">
                  <pre className="whitespace-pre-wrap p-3 font-mono text-xs">{job.gentestGap ?? "(no report yet)"}</pre>
                </ScrollArea>
                {(job.gentestFiles?.length ?? 0) > 0 && (
                  <p className="mt-2 text-xs text-muted-foreground">{job.gentestFiles!.length} test files — see the Convert tab tree.</p>
                )}
              </TabsContent>

              <TabsContent value="logs">
                <ScrollArea className="max-h-[480px]">
                  <pre className="whitespace-pre-wrap p-3 font-mono text-xs">{logs.length ? logs.join("\n") : "(no log lines yet)"}</pre>
                </ScrollArea>
              </TabsContent>
            </Tabs>
          </CardContent>
        </Card>
      )}
    </main>
  );
}

"use client";

import * as React from "react";
import { FileCode2, Folder, Search } from "lucide-react";
import { cn } from "@/lib/utils";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";

export function FileTree({
  files,
  selected,
  onSelect,
}: {
  files: string[];
  selected: string | null;
  onSelect: (p: string) => void;
}) {
  const [q, setQ] = React.useState("");
  const filtered = React.useMemo(() => {
    const needle = q.trim().toLowerCase();
    if (!needle) return files;
    return files.filter((f) => f.toLowerCase().includes(needle));
  }, [files, q]);

  const tree = React.useMemo(() => {
    const root: Record<string, unknown> = {};
    for (const f of filtered) {
      const parts = f.split("/");
      let node = root;
      for (let i = 0; i < parts.length; i++) {
        const last = i === parts.length - 1;
        if (last) (node as Record<string, string>)["\0" + parts[i]] = f;
        else {
          node[parts[i]] = node[parts[i]] ?? {};
          node = node[parts[i]] as Record<string, unknown>;
        }
      }
    }
    return root;
  }, [filtered]);

  function render(node: Record<string, unknown>, depth: number): React.ReactNode {
    return Object.entries(node)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([k, v]) => {
        if (k.startsWith("\0")) {
          const name = k.slice(1);
          const full = v as string;
          return (
            <button
              key={full}
              onClick={() => onSelect(full)}
              title={full}
              className={cn(
                "flex w-full items-center gap-1.5 rounded px-2 py-1 text-left font-mono text-xs hover:bg-accent",
                selected === full && "bg-accent font-semibold"
              )}
              style={{ paddingLeft: depth * 12 + 8 }}
            >
              <FileCode2 className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <span className="truncate">{name}</span>
            </button>
          );
        }
        return (
          <div key={k + depth}>
            <div
              className="flex items-center gap-1.5 px-2 py-1 font-mono text-xs text-muted-foreground"
              style={{ paddingLeft: depth * 12 + 8 }}
            >
              <Folder className="h-3.5 w-3.5" />
              {k}
            </div>
            {render(v as Record<string, unknown>, depth + 1)}
          </div>
        );
      });
  }

  if (files.length === 0) return <p className="p-3 text-xs text-muted-foreground">No files yet — run a conversion.</p>;
  return (
    <div className="py-1">
      <div className="sticky top-0 flex items-center gap-2 bg-background/80 p-2 backdrop-blur">
        <div className="relative flex-1">
          <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Filter files…" className="h-7 pl-7 text-xs" />
        </div>
        {q && (
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={() => setQ("")}>
            Clear
          </Button>
        )}
      </div>
      {filtered.length === 0 ? (
        <p className="p-3 text-xs text-muted-foreground">No files match “{q}”.</p>
      ) : (
        render(tree, 0)
      )}
    </div>
  );
}

export function CodeView({ content, path }: { content: string; path?: string }) {
  const [copied, setCopied] = React.useState(false);
  const lines = React.useMemo(() => content.split("\n"), [content]);

  async function copy() {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable */
    }
  }

  function download() {
    const blob = new Blob([content], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = (path ?? "file.txt").split("/").pop() ?? "file.txt";
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div>
      <div className="sticky top-0 flex items-center gap-2 border-b bg-background/90 p-2 backdrop-blur">
        <code className="truncate font-mono text-[11px] text-muted-foreground">{path ?? "preview"}</code>
        <span className="ml-auto flex gap-1">
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={copy}>
            {copied ? "Copied" : "Copy"}
          </Button>
          <Button variant="ghost" size="sm" className="h-7 px-2 text-xs" onClick={download}>
            Download
          </Button>
        </span>
      </div>
      <pre className="overflow-auto p-3 font-mono text-xs leading-5">
        {lines.map((l, i) => (
          <div key={i} className="flex hover:bg-accent/40">
            <span className="w-10 shrink-0 select-none pr-3 text-right text-muted-foreground/60">{i + 1}</span>
            <code className="whitespace-pre-wrap break-all">{l}</code>
          </div>
        ))}
      </pre>
    </div>
  );
}

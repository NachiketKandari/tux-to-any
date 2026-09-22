"use client";
import * as React from "react";
import { FileCode2, Folder } from "lucide-react";
import { cn } from "@/lib/utils";

export function FileTree({ files, selected, onSelect }: { files: string[]; selected: string | null; onSelect: (p: string) => void }) {
  const tree = React.useMemo(() => {
    const root: Record<string, unknown> = {};
    for (const f of files) {
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
  }, [files]);

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
            <div className="flex items-center gap-1.5 px-2 py-1 font-mono text-xs text-muted-foreground" style={{ paddingLeft: depth * 12 + 8 }}>
              <Folder className="h-3.5 w-3.5" />
              {k}
            </div>
            {render(v as Record<string, unknown>, depth + 1)}
          </div>
        );
      });
  }

  if (files.length === 0) return <p className="p-3 text-xs text-muted-foreground">No files yet — run a conversion.</p>;
  return <div className="py-1">{render(tree, 0)}</div>;
}

export function CodeView({ content }: { content: string }) {
  const lines = React.useMemo(() => content.split("\n"), [content]);
  return (
    <pre className="overflow-auto p-3 font-mono text-xs leading-5">
      {lines.map((l, i) => (
        <div key={i} className="flex">
          <span className="w-10 shrink-0 select-none pr-3 text-right text-muted-foreground/60">{i + 1}</span>
          <code className="whitespace-pre">{l}</code>
        </div>
      ))}
    </pre>
  );
}

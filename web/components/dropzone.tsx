"use client";

import * as React from "react";
import { UploadCloud, FileCheck2, TriangleAlert } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";

const ACCEPT = [".pc", ".pcf"];

export function Dropzone({
  onFile,
  disabled,
  compact,
}: {
  onFile: (f: File) => void;
  disabled?: boolean;
  compact?: boolean;
}) {
  const [over, setOver] = React.useState(false);
  const [reject, setReject] = React.useState("");
  const input = React.useRef<HTMLInputElement>(null);

  function accept(f: File | undefined) {
    if (!f || disabled) return;
    const ok = ACCEPT.some((ext) => f.name.toLowerCase().endsWith(ext));
    if (!ok) {
      setReject(`“${f.name}” is not a .pc/.pcf file — Pro*C sources only.`);
      return;
    }
    if (f.size > 5 * 1024 * 1024) {
      setReject(`“${f.name}” exceeds the 5MB upload limit.`);
      return;
    }
    setReject("");
    onFile(f);
  }

  return (
    <div>
      <div
        role="button"
        tabIndex={0}
        aria-label="Upload a Pro*C file"
        onKeyDown={(e) => {
          if ((e.key === "Enter" || e.key === " ") && !disabled) input.current?.click();
        }}
        onDragOver={(e) => {
          e.preventDefault();
          setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setOver(false);
          accept(e.dataTransfer.files?.[0]);
        }}
        onClick={() => !disabled && input.current?.click()}
        className={cn(
          "flex cursor-pointer flex-col items-center justify-center gap-2 rounded-xl border-2 border-dashed text-center transition-colors",
          compact ? "p-5" : "p-8",
          over ? "border-primary bg-primary/5" : "border-input hover:border-primary/50 hover:bg-accent/50",
          disabled && "cursor-wait opacity-60"
        )}
      >
        <span className="flex h-10 w-10 items-center justify-center rounded-full bg-primary/10">
          {reject ? (
            <TriangleAlert className="h-5 w-5 text-destructive" />
          ) : over ? (
            <FileCheck2 className="h-5 w-5 text-primary" />
          ) : (
            <UploadCloud className="h-5 w-5 text-muted-foreground" />
          )}
        </span>
        <p className="text-sm font-medium">
          Drag &amp; drop a <code className="rounded bg-muted px-1 font-mono">.pc</code> /{" "}
          <code className="rounded bg-muted px-1 font-mono">.pcf</code> file here, or{" "}
          <span className="text-primary underline underline-offset-2">browse</span>
        </p>
        <p className="max-w-md text-xs text-muted-foreground">
          Parsed locally by the tree-sitter stack — deterministic, no LLM calls on upload. Max 5MB.
        </p>
        <input
          ref={input}
          type="file"
          accept={ACCEPT.join(",")}
          className="hidden"
          onChange={(e) => {
            accept(e.target.files?.[0]);
            e.target.value = "";
          }}
        />
      </div>
      {reject && (
        <div className="mt-2 flex items-center justify-between gap-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs text-destructive">
          <span>{reject}</span>
          <Button size="sm" variant="ghost" className="h-6 px-2 text-xs" onClick={() => setReject("")}>
            Dismiss
          </Button>
        </div>
      )}
    </div>
  );
}

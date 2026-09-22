"use client";
import * as React from "react";
import { UploadCloud } from "lucide-react";
import { cn } from "@/lib/utils";

export function Dropzone({ onFile, disabled }: { onFile: (f: File) => void; disabled?: boolean }) {
  const [over, setOver] = React.useState(false);
  const input = React.useRef<HTMLInputElement>(null);
  return (
    <div
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        const f = e.dataTransfer.files?.[0];
        if (f && !disabled) onFile(f);
      }}
      onClick={() => !disabled && input.current?.click()}
      className={cn(
        "flex cursor-pointer flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed p-8 text-center transition-colors",
        over ? "border-primary bg-accent" : "border-input hover:border-primary/50",
        disabled && "cursor-wait opacity-60"
      )}
    >
      <UploadCloud className="h-8 w-8 text-muted-foreground" />
      <p className="text-sm font-medium">Drag &amp; drop a <code>.pc</code> / <code>.pcf</code> file here, or click to browse</p>
      <p className="text-xs text-muted-foreground">max 5MB — parsed by the tree-sitter stack, never leaves this machine</p>
      <input
        ref={input}
        type="file"
        accept=".pc,.pcf"
        className="hidden"
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) onFile(f);
          e.target.value = "";
        }}
      />
    </div>
  );
}

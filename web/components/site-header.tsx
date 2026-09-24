"use client";

import { Database, FileCode2, ArrowRight, Container, Braces } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { ThemeToggle } from "@/components/theme-toggle";

export function SiteHeader({ jobName, status }: { jobName?: string; status?: string }) {
  return (
    <header className="sticky top-0 z-40 w-full border-b bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/60">
      <div className="container flex h-14 max-w-7xl items-center gap-3">
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
          <Container className="h-4 w-4" />
        </div>
        <div className="flex flex-col leading-none">
          <span className="text-sm font-bold tracking-tight">tux-to-any</span>
          <span className="text-[11px] text-muted-foreground">Tuxedo Pro*C → Go · Python · C#</span>
        </div>
        <div className="ml-2 hidden items-center gap-1 text-xs text-muted-foreground md:flex">
          <FileCode2 className="h-3.5 w-3.5" />
          <span>tree-sitter parse</span>
          <ArrowRight className="h-3 w-3" />
          <Database className="h-3.5 w-3.5" />
          <span>IR</span>
          <ArrowRight className="h-3 w-3" />
          <Braces className="h-3.5 w-3.5" />
          <span>plan → code → tests</span>
        </div>
        <div className="ml-auto flex items-center gap-2">
          {jobName && (
            <Badge variant="secondary" className="max-w-[220px] truncate font-mono">
              {jobName}
            </Badge>
          )}
          {status && <Badge variant={status === "error" ? "destructive" : "default"}>{status}</Badge>}
          <ThemeToggle />
        </div>
      </div>
    </header>
  );
}

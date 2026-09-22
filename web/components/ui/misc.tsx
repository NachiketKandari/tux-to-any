import * as React from "react";
import { cn } from "@/lib/utils";

export function ScrollArea({ className, children }: { className?: string; children: React.ReactNode }) {
  return <div className={cn("overflow-auto rounded-md border bg-muted/20", className)}>{children}</div>;
}

export function Alert({ className, children }: { className?: string; children: React.ReactNode }) {
  return <div className={cn("rounded-md border border-input bg-background px-4 py-3 text-sm", className)}>{children}</div>;
}

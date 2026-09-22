"use client";
import * as React from "react";
import { cn } from "@/lib/utils";

// Minimal shadcn-style tabs without radix deps (single-page viewer).
export function Tabs({
  value,
  onValueChange,
  children,
}: {
  value: string;
  onValueChange: (v: string) => void;
  children: React.ReactNode;
}) {
  return <div data-tabs={value}>{React.Children.map(children, (c) => (React.isValidElement(c) ? React.cloneElement(c as React.ReactElement<{ _tabCtx?: unknown }>, { _tabCtx: { value, onValueChange } }) : c))}</div>;
}

export function TabsList({ className, children, ...rest }: React.HTMLAttributes<HTMLDivElement> & { _tabCtx?: { value: string; onValueChange: (v: string) => void } }) {
  void rest._tabCtx;
  return <div className={cn("inline-flex h-9 items-center justify-center rounded-lg bg-muted p-1 text-muted-foreground", className)}>{children}</div>;
}

export function TabsTrigger({
  value,
  _tabCtx,
  className,
  children,
}: {
  value: string;
  _tabCtx?: { value: string; onValueChange: (v: string) => void };
  className?: string;
  children: React.ReactNode;
}) {
  const active = _tabCtx?.value === value;
  return (
    <button
      onClick={() => _tabCtx?.onValueChange(value)}
      className={cn(
        "inline-flex items-center justify-center whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium transition-all disabled:opacity-50",
        active ? "bg-background text-foreground shadow" : "hover:text-foreground",
        className
      )}
    >
      {children}
    </button>
  );
}

export function TabsContent({ value, _tabCtx, className, children }: { value: string; _tabCtx?: { value: string }; className?: string; children: React.ReactNode }) {
  if (_tabCtx?.value !== value) return null;
  return <div className={cn("mt-4", className)}>{children}</div>;
}

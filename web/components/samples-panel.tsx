"use client";

import * as React from "react";
import { Database, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { listSamples } from "@/lib/api-client";

export function SamplesPanel({ busy, onPick }: { busy: boolean; onPick: (path: string) => void }) {
  const [samples, setSamples] = React.useState<
    { path: string; label: string; detail: string; bytes: number; available: boolean }[]
  >([]);
  const [loading, setLoading] = React.useState(true);

  React.useEffect(() => {
    listSamples()
      .then(setSamples)
      .finally(() => setLoading(false));
  }, []);

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <Database className="h-4 w-4" />
          Try a sample — no file handy?
        </CardTitle>
        <CardDescription>Bundled fixtures from the golden test suite. One click loads the full pipeline.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-wrap gap-2">
        {loading ? (
          <span className="flex items-center gap-2 text-xs text-muted-foreground">
            <Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading samples…
          </span>
        ) : (
          samples
            .filter((s) => s.available)
            .map((s) => (
              <Button key={s.path} size="sm" variant="outline" disabled={busy} onClick={() => onPick(s.path)} title={s.path}>
                {s.label}
                <span className="font-normal text-muted-foreground">· {(s.bytes / 1024).toFixed(1)}KB</span>
              </Button>
            ))
        )}
      </CardContent>
    </Card>
  );
}

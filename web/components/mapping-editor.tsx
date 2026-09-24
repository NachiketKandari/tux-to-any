"use client";

import * as React from "react";
import { PencilLine, Loader2, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

export function MappingEditor({
  drafts,
  busy,
  onDraft,
  onSave,
}: {
  drafts: { path: string; content: string }[];
  busy: boolean;
  onDraft: (target: "go" | "cs") => void;
  onSave: (path: string, content: string) => void;
}) {
  const [idx, setIdx] = React.useState(0);
  const [text, setText] = React.useState("");
  const [dirty, setDirty] = React.useState(false);

  React.useEffect(() => {
    setText(drafts[idx]?.content ?? "");
    setDirty(false);
  }, [drafts, idx]);

  const current = drafts[idx];

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <PencilLine className="h-4 w-4" />
          Mapping — tag endpoints
        </CardTitle>
        <CardDescription>
          Drafts are advisory defaults. Review names/routes, delete what you don&apos;t want, then save &amp; convert.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="secondary" disabled={busy} onClick={() => onDraft("go")}>
            {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
            Draft Go mapping
          </Button>
          <Button size="sm" variant="secondary" disabled={busy} onClick={() => onDraft("cs")}>
            Draft C# mapping
          </Button>
          {drafts.length > 1 && (
            <Select value={String(idx)} onValueChange={(v) => setIdx(Number(v))}>
              <SelectTrigger className="h-8 w-[220px] text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {drafts.map((d, i) => (
                  <SelectItem key={d.path} value={String(i)}>
                    {d.path.split("/").pop()}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {current && dirty && <Badge variant="outline">edited</Badge>}
          <Button
            size="sm"
            className="ml-auto"
            disabled={busy || !current}
            onClick={() => {
              if (current) {
                onSave(current.path, text);
                setDirty(false);
              }
            }}
          >
            <Save className="h-3.5 w-3.5" />
            Save &amp; continue
          </Button>
        </div>
        {drafts.length === 0 ? (
          <p className="rounded-md border border-dashed p-4 text-center text-xs text-muted-foreground">
            No drafts yet — run one of the draft passes above.
          </p>
        ) : (
          <Textarea
            rows={22}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setDirty(true);
            }}
            spellCheck={false}
          />
        )}
      </CardContent>
    </Card>
  );
}

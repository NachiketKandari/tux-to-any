import type { Metadata } from "next";
import "./globals.css";
import { TooltipProvider } from "@/components/ui/tooltip";

export const metadata: Metadata = {
  title: "tux-to-any — Tuxedo Pro*C to Go, Python, C#",
  description:
    "Drop a .pc file — visualize the IR breakdown, explore dispatch-axis scenarios, tag mappings, and convert to Go, Python, or C#.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body className="min-h-screen bg-background antialiased">
        <TooltipProvider delayDuration={200}>{children}</TooltipProvider>
      </body>
    </html>
  );
}

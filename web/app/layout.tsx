import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "tux-to-any viewer",
  description: "Drop a .pc file, watch it break down: IR, flow, mapping, conversion, tests.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="min-h-screen antialiased">{children}</body>
    </html>
  );
}

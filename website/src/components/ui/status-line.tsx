import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

export type StatusTone = "on" | "off" | "waiting";

const DOT: Record<StatusTone, string> = {
  on: "bg-chive",
  off: "bg-neutral-400",
  waiting: "bg-accent-500",
};

// StatusLine is how a connection says where it stands: a dot and a word for
// its state, one sentence of what that means, and the one thing to do about
// it. Telegram, Discord and Google all use it, so the three read alike, and
// none of them shows a raw config value ("disabled", an empty cell) as status.
export function StatusLine({ tone, label, detail, action, className }: { tone: StatusTone; label: string; detail?: ReactNode; action?: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-wrap items-center justify-between gap-x-4 gap-y-2 rounded-card bg-neutral-100 px-4 py-3", className)} role="status">
      <div className="flex min-w-0 items-start gap-2.5">
        <span aria-hidden="true" className={cn("mt-[7px] h-2 w-2 shrink-0 rounded-full", DOT[tone])} />
        <div className="min-w-0">
          <p className="text-small font-medium">{label}</p>
          {detail && <p className="mt-0.5 text-caption text-neutral-700">{detail}</p>}
        </div>
      </div>
      {action && <div className="ml-auto shrink-0">{action}</div>}
    </div>
  );
}

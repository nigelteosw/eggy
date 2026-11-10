import { cn } from "../../lib/utils";

// The one mark that a setting is the deployment's rather than the reader's.
// Everywhere something is shared it reads the same, so "this changes it for
// everyone" is recognised at a glance rather than re-read in each card's prose.
export function SharedLabel({ className }: { className?: string }) {
  return <span className={cn("inline-flex shrink-0 items-center rounded-full bg-neutral-200/70 px-2.5 py-0.5 text-caption font-normal text-neutral-700", className)}>Shared with everyone</span>;
}

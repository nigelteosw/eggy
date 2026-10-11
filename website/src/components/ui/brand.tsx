import { cn } from "../../lib/utils";

// The Eggy mark and wordmark, as the docs site draws them: an egg outline
// tilted a few degrees, with a yolk. Written in CSS with the docs' own values
// so the two stay the same shape. The outline and the wordmark take the
// surrounding text colour, so the one component reads on the light rail and
// on the dark sign-in panel alike. The yolk is the one fixed colour.
export function EggyBrand({ className }: { className?: string }) {
  return (
    <span className={cn("inline-flex items-center gap-2.5 font-brand", className)}>
      <span
        aria-hidden="true"
        className="grid h-[34px] w-[27px] shrink-0 place-items-center rotate-[5deg] rounded-[52%_48%_48%_52%/62%_62%_38%_38%] border-2 border-current"
      >
        <span className="h-2 w-2 rounded-full bg-[#dba400]" />
      </span>
      <span className="text-[18px] font-[760] tracking-[-0.03em]">Eggy</span>
    </span>
  );
}

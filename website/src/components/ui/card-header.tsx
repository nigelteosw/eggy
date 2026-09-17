import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

// The heading every settings card and setup section opens with. One place
// for the type scale, so the cards stay a set rather than nineteen copies.
export function CardHeader({
  title,
  description,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("mx-0.5", className)}>
      <h3 className="text-[15px] font-semibold tracking-tight">{title}</h3>
      {description && <p className="mt-1.5 max-w-[620px] text-[12.5px] leading-relaxed text-neutral-700">{description}</p>}
    </div>
  );
}

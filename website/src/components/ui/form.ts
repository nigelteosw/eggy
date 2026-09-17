// Class strings the settings forms share. They live here rather than on the
// Input and Button primitives because those keep the shadcn defaults the
// chat surface uses; the settings forms are the flatter, tinted variant.

// An input sitting on a neutral-100 card: no border, a one-pixel inset ring.
export const FIELD = "rounded-xl border-0 bg-background shadow-[inset_0_0_0_1px_hsl(var(--neutral-200))]";

// A labelled input in a compact grid, where the Input primitive's height and
// ring are replaced wholesale rather than layered.
export const FIELD_COMPACT =
  "h-[42px] w-full rounded-xl border border-neutral-200 bg-background px-3.5 text-[13px] text-foreground caret-accent-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-600/30";

export const FIELD_LABEL = "text-[11.5px] font-normal text-neutral-700";

export const PRIMARY_BUTTON =
  "min-h-10 whitespace-nowrap rounded-xl bg-primary px-4 text-[13.5px] font-semibold text-primary-foreground transition-opacity hover:opacity-90 disabled:pointer-events-none disabled:opacity-50";

export const SECONDARY_BUTTON =
  "min-h-10 whitespace-nowrap rounded-xl px-4 text-[13.5px] font-medium text-neutral-700 transition-colors hover:bg-neutral-200 disabled:pointer-events-none disabled:opacity-50";

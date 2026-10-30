import { useEffect, useRef, useState } from "react";
import { cn } from "../../lib/utils";
import { canCopyImages, canShareImages, copyImage, imageURL, saveImage, shareImage } from "../../lib/images";
import { CheckIcon, ChevronLeftIcon, ChevronRightIcon, CloseIcon, CopyIcon, DownloadIcon, ExpandIcon, ShareIcon } from "./icons";

// A drawn image in place: bounded so a tall one does not push the page away,
// opening full screen on a click, with a quick save on hover.
export function ImageThumb({ id, onOpen, className }: { id: string; onOpen: () => void; className?: string }) {
  return (
    <span className={cn("not-prose group/image relative my-2 inline-block max-w-full", className)}>
      <button
        type="button"
        onClick={onOpen}
        aria-label="View image"
        className="block cursor-zoom-in overflow-hidden rounded-2xl bg-muted ring-1 ring-border/70 transition-shadow hover:shadow-lift focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        <img src={imageURL(id)} alt="Generated image" loading="lazy" className="block max-h-[28rem] max-w-full transition-transform duration-300 group-hover/image:scale-[1.015]" />
      </button>
      <span className="pointer-events-none absolute right-2 top-2 flex gap-1.5 opacity-0 transition-opacity group-focus-within/image:opacity-100 group-hover/image:opacity-100">
        <ThumbAction label="Save image" onClick={() => void saveImage(id)}>
          <DownloadIcon />
        </ThumbAction>
        <ThumbAction label="Open full screen" onClick={onOpen}>
          <ExpandIcon />
        </ThumbAction>
      </span>
    </span>
  );
}

function ThumbAction({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className="pointer-events-auto flex h-8 w-8 items-center justify-center rounded-full bg-black/55 text-white backdrop-blur-md transition-colors hover:bg-black/75"
    >
      {children}
    </button>
  );
}

type Feedback = { label: string; failed?: boolean } | null;

// The full-screen view: the image on a dark scrim, the actions above it, and
// arrows between a reply's images. Escape, the scrim and the close button all
// leave; nothing here navigates away from the page.
export function ImageViewer({ ids, index, onClose }: { ids: string[]; index: number; onClose: () => void }) {
  const [current, setCurrent] = useState(Math.min(Math.max(index, 0), ids.length - 1));
  const [feedback, setFeedback] = useState<Feedback>(null);
  const [loaded, setLoaded] = useState(false);
  const closeRef = useRef<HTMLButtonElement | null>(null);
  const id = ids[current];
  const many = ids.length > 1;
  // Read once on open: whether a phone share sheet or the clipboard exists
  // does not change while the viewer is up.
  const [abilities] = useState(() => ({ share: canShareImages(), copy: canCopyImages() }));

  const step = (by: number) => {
    setLoaded(false);
    setCurrent((at) => (at + by + ids.length) % ids.length);
  };

  useEffect(() => {
    closeRef.current?.focus();
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
      if (many && event.key === "ArrowRight") step(1);
      if (many && event.key === "ArrowLeft") step(-1);
    };
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = previousOverflow;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onClose, many]);

  useEffect(() => {
    if (!feedback) return;
    const timer = setTimeout(() => setFeedback(null), 1800);
    return () => clearTimeout(timer);
  }, [feedback]);

  async function run(action: (id: string) => Promise<void>, done: string) {
    try {
      await action(id);
      setFeedback({ label: done });
    } catch (error) {
      setFeedback({ label: error instanceof Error ? error.message : "That didn't work.", failed: true });
    }
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Image viewer"
      onClick={onClose}
      className="not-prose fixed inset-0 z-[70] flex animate-in fade-in flex-col bg-black/90 text-white backdrop-blur-xl duration-200"
    >
      <header
        onClick={(event) => event.stopPropagation()}
        className="flex items-center gap-2 px-3 pb-2 pt-[max(0.75rem,env(safe-area-inset-top))] sm:px-5"
      >
        <span className="min-w-0 truncate text-sm text-white/70">
          Generated image{many && <span className="tabular-nums"> · {current + 1} of {ids.length}</span>}
        </span>
        <div className="ml-auto flex items-center gap-1">
          <ViewerAction label="Save image" text="Save" onClick={() => void run(saveImage, "Saved")}>
            <DownloadIcon />
          </ViewerAction>
          {abilities.copy && (
            <ViewerAction label="Copy image" text="Copy" onClick={() => void run(copyImage, "Copied")}>
              <CopyIcon />
            </ViewerAction>
          )}
          {abilities.share && (
            <ViewerAction label="Share image" text="Share" onClick={() => void run(shareImage, "Shared")}>
              <ShareIcon />
            </ViewerAction>
          )}
          <span className="mx-1 h-5 w-px bg-white/15" aria-hidden="true" />
          <ViewerAction label="Close" buttonRef={closeRef} onClick={onClose}>
            <CloseIcon />
          </ViewerAction>
        </div>
      </header>

      <div className="relative flex min-h-0 flex-1 items-center justify-center px-4 pb-4 sm:px-16">
        {!loaded && <span className="absolute h-8 w-8 animate-spin rounded-full border-2 border-white/20 border-t-white/80" aria-hidden="true" />}
        <img
          key={id}
          src={imageURL(id)}
          alt="Generated image"
          onLoad={() => setLoaded(true)}
          onClick={(event) => event.stopPropagation()}
          className={cn(
            "max-h-full max-w-full select-none rounded-xl object-contain shadow-[0_24px_80px_-12px_rgba(0,0,0,0.8)] transition-all duration-300",
            loaded ? "scale-100 opacity-100" : "scale-[0.98] opacity-0",
          )}
        />
        {many && (
          <>
            <ArrowButton label="Previous image" side="left" onClick={() => step(-1)}>
              <ChevronLeftIcon className="h-5 w-5" />
            </ArrowButton>
            <ArrowButton label="Next image" side="right" onClick={() => step(1)}>
              <ChevronRightIcon className="h-5 w-5" />
            </ArrowButton>
          </>
        )}
      </div>

      {many && (
        <nav
          aria-label="Images"
          onClick={(event) => event.stopPropagation()}
          className="flex justify-center gap-2 overflow-x-auto px-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
        >
          {ids.map((thumb, at) => (
            <button
              key={thumb}
              type="button"
              aria-label={`Image ${at + 1}`}
              aria-current={at === current ? "true" : undefined}
              onClick={() => {
                if (at !== current) setLoaded(false);
                setCurrent(at);
              }}
              className={cn(
                "h-14 w-14 shrink-0 overflow-hidden rounded-lg ring-2 transition-all",
                at === current ? "opacity-100 ring-white/90" : "opacity-50 ring-transparent hover:opacity-80",
              )}
            >
              <img src={imageURL(thumb)} alt="" className="h-full w-full object-cover" />
            </button>
          ))}
        </nav>
      )}

      <div aria-live="polite" className="pointer-events-none absolute inset-x-0 bottom-24 flex justify-center">
        {feedback && (
          <span
            className={cn(
              "flex animate-in fade-in slide-in-from-bottom-2 items-center gap-1.5 rounded-full px-4 py-2 text-sm font-medium shadow-lg backdrop-blur-md",
              feedback.failed ? "bg-destructive/90 text-destructive-foreground" : "bg-white/15 text-white",
            )}
          >
            {!feedback.failed && <CheckIcon className="h-4 w-4" />}
            {feedback.label}
          </span>
        )}
      </div>
    </div>
  );
}

function ViewerAction({
  label,
  text,
  onClick,
  buttonRef,
  children,
}: {
  label: string;
  text?: string;
  onClick: () => void;
  buttonRef?: React.Ref<HTMLButtonElement>;
  children: React.ReactNode;
}) {
  return (
    <button
      ref={buttonRef}
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className="flex h-10 min-w-10 items-center justify-center gap-2 rounded-full px-2.5 text-sm text-white/85 transition-colors hover:bg-white/10 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/50"
    >
      {children}
      {text && <span className="hidden sm:inline">{text}</span>}
    </button>
  );
}

function ArrowButton({ label, side, onClick, children }: { label: string; side: "left" | "right"; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      onClick={(event) => {
        event.stopPropagation();
        onClick();
      }}
      className={cn(
        "absolute top-1/2 hidden h-11 w-11 -translate-y-1/2 items-center justify-center rounded-full bg-white/10 text-white backdrop-blur-md transition-colors hover:bg-white/20 sm:flex",
        side === "left" ? "left-3" : "right-3",
      )}
    >
      {children}
    </button>
  );
}

// What the chat shows while an image is being drawn: the frame the picture
// will land in, a field of dots breathing under a slow sheen.
export function ImagePlaceholder() {
  return (
    <div
      role="img"
      aria-label="Drawing an image"
      aria-busy="true"
      className="image-placeholder relative aspect-square w-full max-w-[22rem] animate-fade-in-up overflow-hidden rounded-3xl bg-surface ring-1 ring-border/70"
    >
      <span className="image-placeholder-dots absolute inset-0" aria-hidden="true" />
      <span className="image-placeholder-sheen absolute inset-0" aria-hidden="true" />
    </div>
  );
}


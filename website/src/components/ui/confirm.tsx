import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/utils";

export type ConfirmOptions = {
  title: string;
  body?: string;
  confirmLabel: string;
  destructive?: boolean;
};

type Pending = ConfirmOptions & { answer: (ok: boolean) => void };

// useConfirm asks the owner before something happens, in Eggy's own dialog
// rather than the browser's. It returns the asker and the dialog to render
// once in the component. The asker resolves true only for the confirm button,
// so Escape, the scrim and Cancel all decline.
export function useConfirm(): [(options: ConfirmOptions) => Promise<boolean>, ReactNode] {
  const [pending, setPending] = useState<Pending | null>(null);
  const ask = useCallback(
    (options: ConfirmOptions) => new Promise<boolean>((resolve) => setPending({ ...options, answer: resolve })),
    [],
  );
  const dialog = pending ? (
    <ConfirmDialog
      {...pending}
      onAnswer={(ok) => {
        pending.answer(ok);
        setPending(null);
      }}
    />
  ) : null;
  return [ask, dialog];
}

// A sheet anchored to the bottom edge on a phone, a centred card elsewhere. The
// safe action is focused, so pressing Enter by reflex does not destroy anything.
export function ConfirmDialog({ title, body, confirmLabel, destructive = false, onAnswer }: ConfirmOptions & { onAnswer: (ok: boolean) => void }) {
  const cancelRef = useRef<HTMLButtonElement | null>(null);

  useEffect(() => {
    cancelRef.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onAnswer(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onAnswer]);

  return (
    <div className="fixed inset-0 z-[60] flex items-end justify-center bg-foreground/25 sm:items-center sm:p-6" onClick={() => onAnswer(false)}>
      <div
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        aria-describedby={body ? "confirm-body" : undefined}
        onClick={(event) => event.stopPropagation()}
        className="flex w-full flex-col gap-4 rounded-t-3xl bg-card p-6 shadow-lift sm:max-w-sm sm:rounded-2xl"
      >
        <div className="flex flex-col gap-1.5">
          <h2 id="confirm-title" className="text-base font-semibold tracking-tight">
            {title}
          </h2>
          {body && (
            <p id="confirm-body" className="whitespace-pre-line text-sm leading-relaxed text-neutral-700">
              {body}
            </p>
          )}
        </div>
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <button
            ref={cancelRef}
            type="button"
            onClick={() => onAnswer(false)}
            className="min-h-11 rounded-xl px-4 text-sm font-medium text-neutral-700 transition-colors hover:bg-neutral-200 sm:min-h-10"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() => onAnswer(true)}
            className={cn(
              "min-h-11 rounded-xl px-4 text-sm font-semibold transition-opacity hover:opacity-90 sm:min-h-10",
              destructive ? "bg-destructive text-destructive-foreground" : "bg-primary text-primary-foreground",
            )}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}

import { useEffect, useRef, useState } from "react";
import { PRIMARY_BUTTON } from "./form";

// SaveBar is a form's primary action on a phone. The Save button at the foot of
// a long form is out of thumb reach, so once the form has changes the action
// rides at the bottom of the screen, above the home indicator. Above md the
// form's own button does the job and this renders nothing visible.
//
// Given `dirty` and `onSave`, the caller decides. Without them it watches the
// form it sits in: any edit shows it, a submit or reset hides it, and its
// button submits that form -- so a form gains the bar with one line.
export function SaveBar({
  dirty,
  saving = false,
  label,
  onSave,
}: {
  dirty?: boolean;
  saving?: boolean;
  label: string;
  onSave?: () => void;
}) {
  const anchor = useRef<HTMLSpanElement | null>(null);
  const [edited, setEdited] = useState(false);
  const watching = dirty === undefined;

  useEffect(() => {
    if (!watching) return;
    const form = anchor.current?.closest("form");
    if (!form) return;
    const mark = () => setEdited(true);
    const clear = () => setEdited(false);
    form.addEventListener("input", mark);
    form.addEventListener("change", mark);
    form.addEventListener("submit", clear);
    form.addEventListener("reset", clear);
    return () => {
      form.removeEventListener("input", mark);
      form.removeEventListener("change", mark);
      form.removeEventListener("submit", clear);
      form.removeEventListener("reset", clear);
    };
  }, [watching]);

  const show = watching ? edited : dirty;
  return (
    <>
      {watching && <span ref={anchor} hidden />}
      {show && (
        <div className="fixed inset-x-0 bottom-0 z-30 flex justify-end border-t border-border bg-card px-4 pb-[max(0.75rem,env(safe-area-inset-bottom))] pt-3 md:hidden">
          <button type={onSave ? "button" : "submit"} onClick={onSave} disabled={saving} className={PRIMARY_BUTTON}>
            {saving ? "Saving..." : label}
          </button>
        </div>
      )}
    </>
  );
}

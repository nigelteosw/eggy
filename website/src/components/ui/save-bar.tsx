import { PRIMARY_BUTTON } from "./form";

// SaveBar is a form's primary action on a phone. The Save button at the foot of
// a long form is out of thumb reach, so once the form has changes the action
// rides at the bottom of the screen, above the home indicator. Above md the
// form's own button does the job and this renders nothing.
export function SaveBar({
  dirty,
  saving,
  label,
  onSave,
}: {
  dirty: boolean;
  saving: boolean;
  label: string;
  onSave: () => void;
}) {
  if (!dirty) return null;
  return (
    <div className="fixed inset-x-0 bottom-0 z-30 flex justify-end border-t border-border bg-card px-4 pb-[max(0.75rem,env(safe-area-inset-bottom))] pt-3 md:hidden">
      <button type="button" onClick={onSave} disabled={saving} className={PRIMARY_BUTTON}>
        {saving ? "Saving..." : label}
      </button>
    </div>
  );
}

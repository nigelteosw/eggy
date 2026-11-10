import { useId, type ReactElement, cloneElement } from "react";
import { cn } from "../../lib/utils";
import { FIELD_LABEL } from "./form";

// Field is a settings input with its label in words and, where the value
// lands in config.yaml, the key as a quiet hint -- "API key variable ·
// api_key_env" -- so a person reads what it is and an operator can still find
// it in the file. The label is a real <label> bound to the control.
export function Field({
  label,
  configKey,
  hint,
  className,
  children,
}: {
  label: string;
  configKey?: string;
  hint?: string;
  className?: string;
  children: ReactElement<{ id?: string }>;
}) {
  const generated = useId();
  const id = children.props.id ?? generated;
  return (
    <div className={cn("flex min-w-0 flex-col gap-1.5", className)}>
      <label htmlFor={id} className={FIELD_LABEL}>
        {label}
        {configKey && <span className="ml-1.5 font-mono text-neutral-500">{configKey}</span>}
      </label>
      {cloneElement(children, { id })}
      {hint && <p className="text-caption text-neutral-700">{hint}</p>}
    </div>
  );
}

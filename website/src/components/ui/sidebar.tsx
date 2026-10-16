import * as React from "react";

// A boolean that survives a reload, for the two rails that remember their
// width. Private-mode Safari throws on localStorage rather than returning
// null, and a rail that cannot remember its width is still a usable rail, so
// both ends of this swallow the failure.
export function useStoredFlag(key: string, fallback: boolean): [boolean, (value: boolean) => void] {
  const [value, setValue] = React.useState(() => {
    try {
      const stored = window.localStorage.getItem(key);
      return stored === null ? fallback : stored === "true";
    } catch {
      return fallback;
    }
  });
  const update = React.useCallback(
    (next: boolean) => {
      setValue(next);
      try {
        window.localStorage.setItem(key, String(next));
      } catch {
        /* see above */
      }
    },
    [key],
  );
  return [value, update];
}

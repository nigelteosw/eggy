import { useCallback, useEffect, useState } from "react";
import { CommandResult, ConfigSection, SessionExpiredError, getConfig, setConfig } from "./api";

export function useConfigSection(section: ConfigSection, onSessionExpired: () => void) {
  const [result, setResult] = useState<CommandResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const load = useCallback(() => {
    getConfig(section)
      .then(setResult)
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(err instanceof Error ? err.message : "Failed to load");
      });
  }, [section, onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

  const save = useCallback(
    async (values: Record<string, string>) => {
      setSaving(true);
      setError(null);
      try {
        const saved = await setConfig(section, values);
        setResult(saved);
        load();
      } catch (err) {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(err instanceof Error ? err.message : "Failed to save");
      } finally {
        setSaving(false);
      }
    },
    [section, load, onSessionExpired],
  );

  return { result, error, saving, save };
}

import { useCallback, useEffect, useState } from "react";
import {
  CommandResult,
  SessionExpiredError,
  approveChatDecision,
  getAutoMode,
  listApprovals,
  toggleAutoMode,
} from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";
import { Switch } from "./components/ui/switch";

// Column positions in the rows /api/approvals returns.
const ID = 0;
const STATE = 3;

// The auto-mode routes report the state in a field rather than only in prose,
// so the switch reflects the server's answer instead of what the click assumed.
function readAutoMode(result: CommandResult): boolean {
  return result.fields?.some((field) => field.label === "auto_mode" && field.value === "enabled") ?? false;
}

/**
 * Approvals were countable and not inspectable: status reports "1 approval
 * waiting" and nothing could say what it was. Deciding one still goes through
 * the same route a chat tap uses, so this card is the missing view rather than
 * a second way to approve something.
 */
export function ApprovalsCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [result, setResult] = useState<CommandResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [deciding, setDeciding] = useState<string | null>(null);
  const [auto, setAuto] = useState(false);
  const [autoMessage, setAutoMessage] = useState<string | null>(null);
  const [switching, setSwitching] = useState(false);

  const load = useCallback(() => {
    listApprovals()
      .then(setResult)
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
          return;
        }
        setError(err instanceof Error ? err.message : "Failed to load");
      });
    getAutoMode()
      .then((mode) => setAuto(readAutoMode(mode)))
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
        }
        // A gate whose state cannot be read is left showing off. Rendering it
        // as on would tell the owner calls are being reviewed when nothing
        // here knows that.
      });
  }, [onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

  async function switchAuto() {
    setSwitching(true);
    setError(null);
    try {
      const mode = await toggleAutoMode();
      setAuto(readAutoMode(mode));
      setAutoMessage(mode.title ?? null);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : "Could not switch auto mode");
    } finally {
      setSwitching(false);
    }
  }

  async function decide(row: string[], approved: boolean) {
    setDeciding(row[ID]);
    setError(null);
    try {
      await approveChatDecision(row[ID], approved);
      // The decision is enqueued rather than applied inline, so reload
      // instead of assuming the row is gone.
      load();
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : "Could not record decision");
    } finally {
      setDeciding(null);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Approvals</CardTitle>
        <CardDescription>
          Protected actions waiting on you, oldest first. An approval past its window shows as expired: it still counts
          as pending until it is decided, which is why it appears here rather than vanishing.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex flex-col gap-2 rounded-md border border-border px-3 py-3">
          <Switch checked={auto} disabled={switching} onCheckedChange={switchAuto} label="Auto mode" />
          <p className="text-sm text-muted-foreground">
            {autoMessage ??
              (auto
                ? "Auto mode enabled. Approval-gated tool calls now run without asking."
                : "Auto mode disabled. Approval-gated tool calls will ask before running.")}
          </p>
        </div>
        <DataTable
          headers={result?.table_headers}
          rows={result?.table_rows}
          empty="Nothing is waiting on you."
          renderRowAction={(row) => (
            <div className="flex items-center justify-end gap-1">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={deciding === row[ID] || row[STATE] === "expired"}
                onClick={() => decide(row, true)}
              >
                Approve
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={deciding === row[ID]}
                onClick={() => decide(row, false)}
              >
                Reject
              </Button>
            </div>
          )}
        />
        {error && (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
            {error}
          </p>
        )}
      </CardContent>
    </Card>
  );
}

import { useCallback, useEffect, useState } from "react";
import {
  CommandResult,
  SessionExpiredError,
  approveChatDecision,
  getApprovalMode,
  listApprovals,
  setApprovalMode,
} from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";
import { Label } from "./components/ui/label";
import { Select } from "./components/ui/select";

// Column positions in the rows /api/approvals returns.
const ID = 0;
const STATE = 3;

// The mode routes report the state in a field rather than only in prose, so
// the picker reflects the server's answer instead of what the click assumed.
function readApprovalMode(result: CommandResult): string {
  return result.fields?.find((field) => field.label === "approval_mode")?.value ?? "";
}

const MODES = [
  { value: "strict", label: "Strict — ask before every tool call" },
  { value: "normal", label: "Normal — ask before anything that writes" },
  { value: "auto", label: "Auto — never ask" },
];

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
  const [mode, setMode] = useState("");
  const [modeMessage, setModeMessage] = useState<string | null>(null);
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
    getApprovalMode()
      .then((result) => {
        setMode(readApprovalMode(result));
        setModeMessage(result.title ?? null);
      })
      .catch((err) => {
        if (err instanceof SessionExpiredError) {
          onSessionExpired();
        }
        // A mode that cannot be read is left blank rather than defaulting to
        // one. Showing "normal" would tell the owner writes are being reviewed
        // when nothing here knows that.
      });
  }, [onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

  async function chooseMode(next: string) {
    setSwitching(true);
    setError(null);
    try {
      const result = await setApprovalMode(next);
      setMode(readApprovalMode(result));
      setModeMessage(result.title ?? null);
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : "Could not change the approval mode");
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
          <Label htmlFor="approval-mode">Approval mode</Label>
          <Select
            id="approval-mode"
            value={mode}
            disabled={switching || mode === ""}
            onChange={(event) => chooseMode(event.target.value)}
          >
            {/* Present only until the first read lands, so an unreadable mode
                shows as unknown rather than as one of the three. */}
            {mode === "" && <option value="">Loading…</option>}
            {MODES.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </Select>
          <p className="text-sm text-muted-foreground">{modeMessage ?? "Reading the current mode…"}</p>
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

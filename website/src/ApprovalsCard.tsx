import { useCallback, useEffect, useState } from "react";
import { CommandResult, SessionExpiredError, approveChatDecision, listApprovals } from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";

// Column positions in the rows /api/approvals returns.
const ID = 0;
const STATE = 3;

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
  }, [onSessionExpired]);

  useEffect(() => {
    load();
  }, [load]);

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

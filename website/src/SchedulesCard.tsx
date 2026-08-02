import { useCallback, useEffect, useState } from "react";
import { CommandResult, SessionExpiredError, cancelSchedule, listSchedules } from "./api";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import { DataTable } from "./components/ui/data-table";

// Column positions in the rows /api/schedules returns.
const ID = 0;
const INSTRUCTION = 2;

/**
 * Schedules are created by asking Eggy, not by filling in a form: "every
 * weekday at nine, check the deploy" is a better interface than a cron field.
 * What a conversation is bad at is seeing all of them at once and removing the
 * one that was a mistake, so this card is a list and a cancel button rather
 * than full CRUD.
 */
export function SchedulesCard({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [result, setResult] = useState<CommandResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [cancelling, setCancelling] = useState<string | null>(null);

  const load = useCallback(() => {
    listSchedules()
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

  async function handleCancel(row: string[]) {
    if (!window.confirm(`Cancel this schedule?\n\n${row[INSTRUCTION]}`)) return;
    setCancelling(row[ID]);
    setError(null);
    try {
      await cancelSchedule(row[ID]);
      load();
    } catch (err) {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : "Could not cancel schedule");
    } finally {
      setCancelling(null);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Schedules</CardTitle>
        <CardDescription>
          Everything Eggy will do on a timer, soonest first. Ask it in chat to create one; cancel it here. The heartbeat
          is a separate mechanism and does not appear in this list.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DataTable
          headers={result?.table_headers}
          rows={result?.table_rows}
          empty="Nothing is scheduled."
          renderRowAction={(row) => (
            <Button type="button" variant="ghost" size="sm" disabled={cancelling === row[ID]} onClick={() => handleCancel(row)}>
              {cancelling === row[ID] ? "Cancelling..." : "Cancel"}
            </Button>
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

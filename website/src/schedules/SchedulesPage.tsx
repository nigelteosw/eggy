import { PageShell } from "../components/ui/page-shell";
import { PageHeader } from "../components/ui/page-header";
import { SchedulesCard } from "./SchedulesCard";

// The Scheduled destination. Schedules are created by asking Eggy in chat, so
// this page only lists what is pending and lets the owner cancel one. The
// card below says so; the page header does not say it a second time.
export function SchedulesPage({ onSessionExpired, onOpenSidebar }: { onSessionExpired: () => void; onOpenSidebar?: () => void }) {
  return (
    <PageShell>
        <PageHeader
          title="Scheduled"
          onOpenSidebar={onOpenSidebar}
          className="pb-1"
        />

        <SchedulesCard onSessionExpired={onSessionExpired} />
    </PageShell>
  );
}

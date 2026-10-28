import { PageHeader } from "../components/ui/page-header";
import { SchedulesCard } from "./SchedulesCard";

// The Scheduled destination. Schedules are created by asking Eggy in chat, so
// this page only lists what is pending and lets the owner cancel one.
export function SchedulesPage({ onSessionExpired, onOpenSidebar }: { onSessionExpired: () => void; onOpenSidebar?: () => void }) {
  return (
    <main className="app-canvas scrollbar-slim h-full min-h-0 min-w-0 overflow-y-auto">
      <div className="mx-auto flex max-w-[860px] flex-col gap-6 px-5 pb-11 pt-10 sm:px-7">
        <PageHeader
          title="Scheduled"
          onOpenSidebar={onOpenSidebar}
          description="Tasks Eggy will run later. Ask Eggy in chat to set one up, and cancel it here."
          className="pb-1"
        />

        <SchedulesCard onSessionExpired={onSessionExpired} />
      </div>
    </main>
  );
}

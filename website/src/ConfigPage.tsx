import { useState, type ReactNode } from "react";
import { logout, type Theme } from "./api";
import { cn } from "./lib/utils";
import { ProvidersCard } from "./ProvidersCard";
import { ModelsCard } from "./ModelsCard";
import { McpCard } from "./McpCard";
import { GoogleCard } from "./GoogleCard";
import { DiscordCard } from "./DiscordCard";
import { HeartbeatCard } from "./HeartbeatCard";
import { WatchCard } from "./WatchCard";
import { ToolsCard } from "./ToolsCard";
import { TracingCard } from "./TracingCard";
import { SchedulesCard } from "./SchedulesCard";
import { ApprovalsCard } from "./ApprovalsCard";
import { AppearanceCard } from "./AppearanceCard";
import { AdvancedCard } from "./AdvancedCard";
import { AccountsCard } from "./AccountsCard";
import { PersonalSettingsCard } from "./PersonalSettingsCard";
import { RestartCard } from "./RestartCard";
import {
  CheckShieldIcon,
  ClockIcon,
  CpuIcon,
  FileCodeIcon,
  LogoutIcon,
  PaletteIcon,
  PlugIcon,
  UsersIcon,
  WrenchIcon,
} from "./components/ui/icons";

export type SectionId = "personal" | "automation" | "permissions" | "accounts" | "models" | "connections" | "capabilities" | "appearance" | "advanced";

// Settings fall into three areas with different owners. "My settings" is
// the signed-in person's own runtime state and documents; "People" is the
// trusted-user list everyone administers; "Shared deployment" is the
// configuration file and integrations everyone shares. Every person can
// reach all three -- there are no roles -- but the headings say whose a
// change is before it is made, so a shared control never reads as a
// personal preference.
type GroupId = "personal" | "people" | "shared";

type Section = {
  id: SectionId;
  group: GroupId;
  label: string;
  title: string;
  description: string;
  icon: ReactNode;
};

export const GROUPS: { id: GroupId; heading: string; note: string }[] = [
  { id: "personal", heading: "My settings", note: "Applies to your Telegram and web sessions." },
  { id: "people", heading: "People", note: "Trusted users who can administer this deployment." },
  { id: "shared", heading: "Shared deployment", note: "Changes here affect everyone." },
];

export const SECTIONS: Section[] = [
  { id: "personal", group: "personal", label: "Model & approvals", title: "My settings — applies to your Telegram and web sessions", description: "Your model, reasoning effort, thinking visibility and approval mode. Yours alone.", icon: <CpuIcon /> },
  { id: "automation", group: "personal", label: "Automation", title: "My automation — applies to your Telegram and web sessions", description: "Your schedules and your watch list. The heartbeat cadence is shared deployment configuration.", icon: <ClockIcon /> },
  { id: "permissions", group: "personal", label: "Pending approvals", title: "My pending approvals", description: "Actions waiting on you.", icon: <CheckShieldIcon /> },
  { id: "accounts", group: "people", label: "People", title: "People — trusted users who can administer this deployment", description: "Who can use this Eggy, how each signs in, and which Google account Eggy itself is.", icon: <UsersIcon /> },
  { id: "models", group: "shared", label: "Models", title: "Shared deployment — changes here affect everyone", description: "Providers, API keys, and the aliases that route to them, used by every person.", icon: <CpuIcon /> },
  { id: "connections", group: "shared", label: "Connections", title: "Shared deployment — changes here affect everyone", description: "External tools, the shared Google Workspace grant, and chat bots.", icon: <PlugIcon /> },
  { id: "capabilities", group: "shared", label: "Capabilities", title: "Shared deployment — capabilities", description: "What Eggy can use during anyone's turn.", icon: <WrenchIcon /> },
  { id: "appearance", group: "shared", label: "Appearance", title: "Shared deployment — appearance", description: "How the panel looks, for everyone.", icon: <PaletteIcon /> },
  { id: "advanced", group: "shared", label: "Advanced", title: "Shared deployment — changes here affect everyone", description: "Heartbeat, tracing, raw configuration, and restart controls.", icon: <FileCodeIcon /> },
];

export function ConfigPage({
  theme,
  onThemeChange,
  onSessionExpired,
  initialSection = "personal",
}: {
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
  onSessionExpired: () => void;
  initialSection?: SectionId;
}) {
  const [active, setActive] = useState<SectionId>(initialSection);
  const section = SECTIONS.find((candidate) => candidate.id === active) ?? SECTIONS[0];

  async function handleLogout() {
    try {
      await logout();
    } finally {
      // A logout that failed still means the owner asked to leave, and the
      // session check on the way back in is the authority either way.
      onSessionExpired();
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col md:flex-row">
      <div className="sticky top-0 z-20 shrink-0 border-b border-neutral-200 bg-background md:hidden">
        <div className="flex items-center gap-2 px-3 py-2">
          <select
            aria-label="Mobile settings navigation"
            value={active}
            onChange={(event) => setActive(event.target.value as SectionId)}
            className="h-11 min-w-0 flex-1 rounded-xl border-0 bg-neutral-100 px-3 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            {GROUPS.map((group) => (
              <optgroup key={group.id} label={group.heading}>
                {SECTIONS.filter((candidate) => candidate.group === group.id).map((candidate) => (
                  <option key={candidate.id} value={candidate.id}>
                    {candidate.label}
                  </option>
                ))}
              </optgroup>
            ))}
          </select>
          <button
            type="button"
            onClick={handleLogout}
            aria-label="Log out"
            title="Log out"
            className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl text-neutral-700 transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            <LogoutIcon />
          </button>
        </div>
      </div>

      <div className="hidden min-h-0 w-[238px] shrink-0 flex-col bg-neutral-100 md:flex">
        <div className="shrink-0 px-4 pb-2 pt-4 text-[14.5px] font-semibold tracking-tight">Settings</div>
        <div className="scrollbar-slim min-h-0 flex-1 overflow-y-auto px-3 pb-3">
          {GROUPS.map((group) => (
            <div key={group.id} className="mb-3">
              <p className="px-3.5 pb-1 pt-2 text-[11.5px] font-semibold uppercase tracking-wide text-neutral-700" title={group.note}>
                {group.heading}
              </p>
              {SECTIONS.filter((candidate) => candidate.group === group.id).map((candidate) => (
                <button
                  key={candidate.id}
                  type="button"
                  aria-current={candidate.id === active ? "page" : undefined}
                  onClick={() => setActive(candidate.id)}
                  className={cn(
                    "mb-0.5 block min-h-[42px] w-full rounded-lg px-3.5 py-2.5 text-left text-sm text-foreground transition-colors",
                    candidate.id === active ? "bg-background font-semibold" : "font-normal hover:bg-background/60",
                  )}
                >
                  {candidate.label}
                </button>
              ))}
            </div>
          ))}
        </div>
        <div className="shrink-0 px-3 pb-4 pt-2 shadow-[inset_0_1px_0_hsl(var(--neutral-200))]">
          <button
            type="button"
            onClick={handleLogout}
            className="flex min-h-[42px] w-full items-center gap-2.5 rounded-lg px-3.5 text-left text-sm text-foreground hover:bg-background/60"
          >
            <LogoutIcon className="h-[17px] w-[17px] text-neutral-700" />
            Log out
          </button>
        </div>
      </div>

      <div className="app-canvas scrollbar-slim min-h-0 min-w-0 flex-1 overflow-y-auto shadow-[inset_1px_0_0_hsl(var(--neutral-200))]">
        <div className="mx-auto flex max-w-[860px] flex-col gap-6 px-5 pb-11 pt-6 sm:px-7">
          <header className="flex flex-col gap-1.5 pb-1">
            <h1 className="text-xl font-semibold tracking-tight">{section.title}</h1>
            <p className="max-w-2xl text-sm leading-6 text-neutral-700">{section.description}</p>
          </header>
          {active === "personal" && <PersonalSettingsCard onSessionExpired={onSessionExpired} />}
          {active === "models" && (
            <>
              <ProvidersCard onSessionExpired={onSessionExpired} />
              <ModelsCard onSessionExpired={onSessionExpired} />
              <p className="text-sm text-muted-foreground">New model choices appear in chat after restart.</p>
              <RestartCard onSessionExpired={onSessionExpired} />
            </>
          )}
          {active === "connections" && (
            <>
              <McpCard onSessionExpired={onSessionExpired} />
              <GoogleCard onSessionExpired={onSessionExpired} />
              <DiscordCard onSessionExpired={onSessionExpired} />
            </>
          )}
          {active === "capabilities" && <ToolsCard onSessionExpired={onSessionExpired} />}
          {active === "automation" && (
            <>
              <SchedulesCard onSessionExpired={onSessionExpired} />
              <WatchCard onSessionExpired={onSessionExpired} />
            </>
          )}
          {active === "permissions" && <ApprovalsCard onSessionExpired={onSessionExpired} />}
          {active === "accounts" && <AccountsCard onSessionExpired={onSessionExpired} />}
          {active === "appearance" && (
            <AppearanceCard theme={theme} onThemeChange={onThemeChange} onSessionExpired={onSessionExpired} />
          )}
          {active === "advanced" && (
            <>
              <HeartbeatCard onSessionExpired={onSessionExpired} />
              <TracingCard onSessionExpired={onSessionExpired} />
              <AdvancedCard onSessionExpired={onSessionExpired} />
              <RestartCard onSessionExpired={onSessionExpired} />
            </>
          )}
        </div>
      </div>
    </div>
  );
}

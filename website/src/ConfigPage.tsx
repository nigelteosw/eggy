import { useState, type ReactNode } from "react";
import { logout, type Theme } from "./api";
import { ProvidersCard } from "./ProvidersCard";
import { ModelsCard } from "./ModelsCard";
import { McpCard } from "./McpCard";
import { GoogleCard } from "./GoogleCard";
import { HeartbeatCard } from "./HeartbeatCard";
import { WatchCard } from "./WatchCard";
import { ToolsCard } from "./ToolsCard";
import { TracingCard } from "./TracingCard";
import { SchedulesCard } from "./SchedulesCard";
import { ApprovalsCard } from "./ApprovalsCard";
import { AppearanceCard } from "./AppearanceCard";
import { AdvancedCard } from "./AdvancedCard";
import { RestartCard } from "./RestartCard";
import { Sidebar, SidebarItem, SidebarSeparator } from "./components/ui/sidebar";
import {
  CheckShieldIcon,
  ChevronLeftIcon,
  ClockIcon,
  CpuIcon,
  FileCodeIcon,
  LogoutIcon,
  PaletteIcon,
  PlugIcon,
  WrenchIcon,
} from "./components/ui/icons";

type SectionId = "models" | "connections" | "capabilities" | "automation" | "permissions" | "appearance" | "advanced";

type Section = {
  id: SectionId;
  label: string;
  title: string;
  description: string;
  icon: ReactNode;
};

const SECTIONS: Section[] = [
  { id: "models", label: "Models", title: "Models", description: "Providers and the aliases that route to them.", icon: <CpuIcon /> },
  { id: "connections", label: "Connections", title: "Connections", description: "Connect external tools and Google Workspace.", icon: <PlugIcon /> },
  { id: "capabilities", label: "Capabilities", title: "Capabilities", description: "See what Eggy can use during a turn.", icon: <WrenchIcon /> },
  { id: "automation", label: "Automation", title: "Automation", description: "Scheduled runs and the periodic check-in.", icon: <ClockIcon /> },
  { id: "permissions", label: "Permissions", title: "Permissions", description: "Review actions that need your approval.", icon: <CheckShieldIcon /> },
  { id: "appearance", label: "Appearance", title: "Appearance", description: "How the panel looks.", icon: <PaletteIcon /> },
  { id: "advanced", label: "Advanced", title: "Advanced", description: "Tracing, raw configuration, and restart controls.", icon: <FileCodeIcon /> },
];

export function ConfigPage({
  theme,
  onThemeChange,
  onSessionExpired,
  onBackToChat,
}: {
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
  onSessionExpired: () => void;
  onBackToChat: () => void;
}) {
  const [active, setActive] = useState<SectionId>("models");
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
      <div className="sticky top-0 z-20 shrink-0 border-b bg-background md:hidden">
        <div className="flex items-center gap-2 px-3 py-2">
          <button
            type="button"
            onClick={onBackToChat}
            className="flex h-11 shrink-0 items-center gap-1.5 rounded-md px-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            <ChevronLeftIcon />
            <span>Chat</span>
          </button>
          <select
            aria-label="Mobile settings navigation"
            value={active}
            onChange={(event) => setActive(event.target.value as SectionId)}
            className="h-11 min-w-0 flex-1 rounded-md border border-input bg-card px-3 text-sm text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            {SECTIONS.map((candidate) => (
              <option key={candidate.id} value={candidate.id}>
                {candidate.label}
              </option>
            ))}
          </select>
          <button
            type="button"
            onClick={handleLogout}
            aria-label="Log out"
            title="Log out"
            className="flex h-11 w-11 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            <LogoutIcon />
          </button>
        </div>
      </div>

      <Sidebar collapsed={false} className="hidden md:flex">
        <SidebarItem
          icon={<ChevronLeftIcon />}
          label="Back to chat"
          collapsed={false}
          onClick={onBackToChat}
        />
        <SidebarSeparator />
        {SECTIONS.map((candidate) => (
          <SidebarItem
            key={candidate.id}
            icon={candidate.icon}
            label={candidate.label}
            active={candidate.id === active}
            collapsed={false}
            onClick={() => setActive(candidate.id)}
          />
        ))}
        <div className="mt-auto" />
        <SidebarSeparator />
        <SidebarItem icon={<LogoutIcon />} label="Log out" collapsed={false} onClick={handleLogout} />
      </Sidebar>

      <div className="app-canvas scrollbar-slim min-h-0 min-w-0 flex-1 overflow-y-auto">
        <div className="page-shell max-w-3xl">
          <header className="flex flex-col gap-1.5 pb-1">
            <h1 className="text-3xl font-semibold tracking-tight">{section.title}</h1>
            <p className="max-w-2xl text-sm leading-6 text-muted-foreground">{section.description}</p>
          </header>
          {active === "models" && (
            <>
              <ProvidersCard onSessionExpired={onSessionExpired} />
              <ModelsCard onSessionExpired={onSessionExpired} />
            </>
          )}
          {active === "connections" && (
            <>
              <McpCard onSessionExpired={onSessionExpired} />
              <GoogleCard onSessionExpired={onSessionExpired} />
            </>
          )}
          {active === "capabilities" && <ToolsCard onSessionExpired={onSessionExpired} />}
          {active === "automation" && (
            <>
              <SchedulesCard onSessionExpired={onSessionExpired} />
              <HeartbeatCard onSessionExpired={onSessionExpired} />
              <WatchCard onSessionExpired={onSessionExpired} />
            </>
          )}
          {active === "permissions" && <ApprovalsCard onSessionExpired={onSessionExpired} />}
          {active === "appearance" && (
            <AppearanceCard theme={theme} onThemeChange={onThemeChange} onSessionExpired={onSessionExpired} />
          )}
          {active === "advanced" && (
            <>
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

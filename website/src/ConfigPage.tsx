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
import { Sidebar, SidebarItem, SidebarSeparator, useSidebarCollapsed } from "./components/ui/sidebar";
import {
  CheckShieldIcon,
  ChevronLeftIcon,
  ClockIcon,
  CpuIcon,
  FileCodeIcon,
  GoogleIcon,
  LogoutIcon,
  PaletteIcon,
  PanelIcon,
  PlugIcon,
  TraceIcon,
  WrenchIcon,
} from "./components/ui/icons";

type SectionId = "models" | "mcp" | "google" | "tools" | "automation" | "tracing" | "approvals" | "appearance" | "advanced";

type Section = {
  id: SectionId;
  label: string;
  title: string;
  description: string;
  icon: ReactNode;
};

// The destinations the old single-column scroll had as stacked cards. Two cards share a page where they answer the same question:
// Providers and Models are both "which backend", and Schedules and Heartbeat
// are both "what fires on a timer".
const SECTIONS: Section[] = [
  { id: "models", label: "Models", title: "Models", description: "Providers and the aliases that route to them.", icon: <CpuIcon /> },
  { id: "mcp", label: "MCP", title: "MCP", description: "External tool servers Eggy can call during a turn.", icon: <PlugIcon /> },
  { id: "google", label: "Google", title: "Google Workspace", description: "One grant across every enabled product.", icon: <GoogleIcon /> },
  { id: "tools", label: "Tools", title: "Tools", description: "Every tool a turn can call, kernel and MCP alike.", icon: <WrenchIcon /> },
  { id: "automation", label: "Automation", title: "Automation", description: "Scheduled runs and the periodic check-in.", icon: <ClockIcon /> },
  { id: "tracing", label: "Tracing", title: "Tracing", description: "What gets recorded about each turn, and for how long.", icon: <TraceIcon /> },
  { id: "approvals", label: "Approvals", title: "Approvals", description: "Protected actions waiting on you.", icon: <CheckShieldIcon /> },
  { id: "appearance", label: "Appearance", title: "Appearance", description: "How the panel looks.", icon: <PaletteIcon /> },
  { id: "advanced", label: "Advanced", title: "Advanced", description: "The config file behind every form here, and the restart that applies it.", icon: <FileCodeIcon /> },
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
  const [collapsed, setCollapsed] = useSidebarCollapsed();
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
      <div className="shrink-0 border-b border-border/80 bg-background md:hidden">
        <div className="flex items-center gap-2 px-3 py-2">
          <button
            type="button"
            onClick={onBackToChat}
            className="flex h-10 shrink-0 items-center gap-1.5 rounded-md px-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            <ChevronLeftIcon />
            <span>Chat</span>
          </button>
          <select
            aria-label="Mobile settings navigation"
            value={active}
            onChange={(event) => setActive(event.target.value as SectionId)}
            className="h-10 min-w-0 flex-1 rounded-md border border-input bg-card px-3 text-sm text-foreground shadow-subtle focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
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
            className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            <LogoutIcon />
          </button>
        </div>
      </div>

      <Sidebar collapsed={collapsed} className="hidden md:flex">
        <SidebarItem
          icon={<ChevronLeftIcon />}
          label="Back to chat"
          collapsed={collapsed}
          onClick={onBackToChat}
        />
        <SidebarItem
          icon={<PanelIcon />}
          label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          collapsed={collapsed}
          onClick={() => setCollapsed(!collapsed)}
        />
        <SidebarSeparator />
        {SECTIONS.map((candidate) => (
          <SidebarItem
            key={candidate.id}
            icon={candidate.icon}
            label={candidate.label}
            active={candidate.id === active}
            collapsed={collapsed}
            onClick={() => setActive(candidate.id)}
          />
        ))}
        <div className="mt-auto" />
        <SidebarSeparator />
        <SidebarItem icon={<LogoutIcon />} label="Log out" collapsed={collapsed} onClick={handleLogout} />
      </Sidebar>

      <div className="app-canvas scrollbar-slim min-h-0 min-w-0 flex-1 overflow-y-auto">
        <div className="page-shell max-w-3xl">
          <header className="flex flex-col gap-1.5 pb-1">
            <p className="section-eyebrow">Settings</p>
            <h1 className="text-3xl font-semibold tracking-tight">{section.title}</h1>
            <p className="max-w-2xl text-sm leading-6 text-muted-foreground">{section.description}</p>
          </header>
          {active === "models" && (
            <>
              <ProvidersCard onSessionExpired={onSessionExpired} />
              <ModelsCard onSessionExpired={onSessionExpired} />
            </>
          )}
          {active === "mcp" && <McpCard onSessionExpired={onSessionExpired} />}
          {active === "google" && <GoogleCard onSessionExpired={onSessionExpired} />}
          {active === "tools" && <ToolsCard onSessionExpired={onSessionExpired} />}
          {active === "automation" && (
            <>
              <SchedulesCard onSessionExpired={onSessionExpired} />
              <HeartbeatCard onSessionExpired={onSessionExpired} />
              <WatchCard onSessionExpired={onSessionExpired} />
            </>
          )}
          {active === "tracing" && <TracingCard onSessionExpired={onSessionExpired} />}
          {active === "approvals" && <ApprovalsCard onSessionExpired={onSessionExpired} />}
          {active === "appearance" && (
            <AppearanceCard theme={theme} onThemeChange={onThemeChange} onSessionExpired={onSessionExpired} />
          )}
          {active === "advanced" && (
            <>
              <AdvancedCard onSessionExpired={onSessionExpired} />
              <RestartCard onSessionExpired={onSessionExpired} />
            </>
          )}
        </div>
      </div>
    </div>
  );
}

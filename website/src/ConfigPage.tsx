import { useState, type ReactNode } from "react";
import type { Theme } from "./api";
import { cn } from "./lib/utils";
import { ProvidersCard } from "./ProvidersCard";
import { ModelsCard } from "./ModelsCard";
import { DiscordCard } from "./DiscordCard";
import { HeartbeatCard } from "./HeartbeatCard";
import { WatchCard } from "./WatchCard";
import { SoulCard } from "./SoulCard";
import { ToolsCard } from "./ToolsCard";
import { TracingCard } from "./TracingCard";
import { FinanceCard } from "./extensions/finance/FinanceCard";
import { MarketplaceCard } from "./extensions/marketplace/MarketplaceCard";
import { AgentRunnerCard } from "./extensions/agent-runner/AgentRunnerCard";
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
  ChevronDownIcon,
  CloseIcon,
  HeartIcon,
  PaletteIcon,
  PlugIcon,
  SearchIcon,
  UsersIcon,
  WrenchIcon,
} from "./components/ui/icons";

export type SectionId = "personal" | "automation" | "permissions" | "accounts" | "models" | "connections" | "capabilities" | "soul" | "appearance" | "advanced";

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
  { id: "personal", group: "personal", label: "Model & approvals", title: "My settings", description: "Applies to your Telegram and web sessions. Your model, reasoning effort, thinking visibility and approval mode are yours alone.", icon: <CpuIcon /> },
  { id: "automation", group: "personal", label: "Check-ins", title: "My check-ins — applies to your Telegram and web sessions", description: "Whether Eggy checks in on you, what it keeps an eye on, and how often. Schedules are on the Scheduled page.", icon: <ClockIcon /> },
  { id: "permissions", group: "personal", label: "Pending approvals", title: "My pending approvals", description: "Actions waiting on you.", icon: <CheckShieldIcon /> },
  { id: "accounts", group: "people", label: "People", title: "People — trusted users who can administer this deployment", description: "Who can use this Eggy, how each signs in, and which Google account Eggy itself is.", icon: <UsersIcon /> },
  { id: "models", group: "shared", label: "Models", title: "Shared deployment — changes here affect everyone", description: "Providers, API keys, and the aliases that route to them, used by every person.", icon: <CpuIcon /> },
  { id: "connections", group: "shared", label: "Connections", title: "Shared deployment — changes here affect everyone", description: "Chat bots. Skills, MCP servers and Google are on the Customize page.", icon: <PlugIcon /> },
  { id: "capabilities", group: "shared", label: "Capabilities", title: "Shared deployment — capabilities", description: "What Eggy can use during anyone's turn.", icon: <WrenchIcon /> },
  { id: "soul", group: "shared", label: "Soul", title: "Shared deployment — Eggy's soul", description: "Who Eggy is and how it sounds, for everyone.", icon: <HeartIcon /> },
  { id: "appearance", group: "shared", label: "Appearance", title: "Shared deployment — appearance", description: "How the panel looks, for everyone.", icon: <PaletteIcon /> },
  { id: "advanced", group: "shared", label: "Advanced", title: "Shared deployment — changes here affect everyone", description: "Tracing, raw configuration, and restart controls.", icon: <FileCodeIcon /> },
];

// sectionMatches finds a settings section by its name, its title, or what its
// description says it holds, so searching for a channel or a setting finds the
// place that changes it.
export function sectionMatches(section: Section, query: string): boolean {
  const needle = query.trim().toLowerCase();
  if (needle === "") return true;
  return [section.label, section.title, section.description].some((text) => text.toLowerCase().includes(needle));
}

export function ConfigPage({
  theme,
  onThemeChange,
  onSessionExpired,
  features = [],
  initialSection = "personal",
  onClose,
}: {
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
  onSessionExpired: () => void;
  // The optional capabilities this process started with. Finance's settings
  // card reads whether it is running, so it can say a restart is pending.
  features?: string[];
  initialSection?: SectionId;
  // Passed by the settings popup. Its close control sits in the phone's top
  // bar, where the sticky header would otherwise cover it.
  onClose?: () => void;
}) {
  const [active, setActive] = useState<SectionId>(initialSection);
  // On a phone, whether the page for the active section is pushed over the list.
  const [opened, setOpened] = useState(initialSection !== "personal");
  const [query, setQuery] = useState("");
  const section = SECTIONS.find((candidate) => candidate.id === active) ?? SECTIONS[0];

  return (
    <div className="flex h-full min-h-0 flex-col md:flex-row">
      {/* Phones: the sections are a list, and opening one pushes its page with a
          back control, as iOS Settings does. Wide screens keep the rail. */}
      <div className="sticky top-0 z-20 shrink-0 border-b border-neutral-200 bg-background md:hidden">
        <div className="flex h-14 items-center gap-2 px-3">
          {opened ? (
            <button
              type="button"
              onClick={() => setOpened(false)}
              className="flex h-11 min-w-0 items-center gap-1 rounded-xl px-2 text-sm font-medium text-foreground transition-colors hover:bg-neutral-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
            >
              <ChevronDownIcon className="h-4 w-4 rotate-90" />
              Settings
            </button>
          ) : (
            <span className="min-w-0 flex-1 px-2 text-base font-semibold">Settings</span>
          )}
          {opened && <span className="min-w-0 flex-1 truncate text-sm text-neutral-700">{section.label}</span>}
          {onClose && (
            <button
              type="button"
              onClick={onClose}
              aria-label="Close settings"
              title="Close"
              className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl text-neutral-700 transition-colors hover:bg-neutral-100 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
            >
              <CloseIcon />
            </button>
          )}
        </div>
      </div>
      <nav aria-label="Mobile settings navigation" className={`scrollbar-slim min-h-0 flex-1 overflow-y-auto md:hidden ${opened ? "hidden" : ""}`}>
        {GROUPS.map((group) => (
          <section key={group.id} className="px-4 pb-4 pt-5">
            <p className="px-1 pb-2 text-[13px] text-neutral-700" title={group.note}>
              {group.heading}
            </p>
            <div className="flex flex-col overflow-hidden rounded-2xl bg-neutral-100">
              {SECTIONS.filter((candidate) => candidate.group === group.id).map((candidate) => (
                <button
                  key={candidate.id}
                  type="button"
                  onClick={() => {
                    setActive(candidate.id);
                    setOpened(true);
                  }}
                  className="flex min-h-12 items-center gap-3 px-4 text-left text-base text-foreground shadow-[inset_0_1px_0_hsl(var(--neutral-200))] first:shadow-none"
                >
                  <span className="flex h-5 w-5 shrink-0 items-center justify-center text-neutral-700 [&>svg]:h-5 [&>svg]:w-5">{candidate.icon}</span>
                  <span className="min-w-0 flex-1">{candidate.label}</span>
                  <ChevronDownIcon className="h-4 w-4 -rotate-90 text-neutral-500" />
                </button>
              ))}
            </div>
          </section>
        ))}
      </nav>

      <div className="hidden min-h-0 w-[238px] shrink-0 flex-col bg-neutral-100 md:flex">
        <div className="shrink-0 px-3 pb-2 pt-3">
          <label className="flex h-9 items-center gap-2 rounded-lg border border-neutral-200 bg-background px-2.5 text-neutral-700 focus-within:ring-2 focus-within:ring-ring/40">
            <SearchIcon className="h-4 w-4 shrink-0" />
            <input
              type="search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search"
              aria-label="Search settings"
              className="min-w-0 flex-1 bg-transparent text-sm text-foreground placeholder:text-neutral-500 focus-visible:outline-none"
            />
          </label>
        </div>
        <div className="shrink-0 px-4 pb-1 pt-3 text-[13px] text-neutral-700">Settings</div>
        <div className="scrollbar-slim min-h-0 flex-1 overflow-y-auto px-3 pb-3">
          {GROUPS.map((group) => {
            const matches = SECTIONS.filter((candidate) => candidate.group === group.id && sectionMatches(candidate, query));
            if (matches.length === 0) return null;
            return (
              <div key={group.id} className="mb-3">
                <p className="px-3 pb-1 pt-3 text-[13px] text-neutral-700" title={group.note}>
                  {group.heading}
                </p>
                {matches.map((candidate) => (
                  <button
                    key={candidate.id}
                    type="button"
                    aria-current={candidate.id === active ? "page" : undefined}
                    onClick={() => setActive(candidate.id)}
                    className={cn(
                      "mb-0.5 flex min-h-[36px] w-full items-center gap-3 rounded-lg px-3 py-1.5 text-left text-sm text-foreground transition-colors",
                      candidate.id === active ? "bg-neutral-200/70 font-medium" : "font-normal hover:bg-neutral-200/40",
                    )}
                  >
                    <span className="flex h-4 w-4 shrink-0 items-center justify-center text-neutral-700 [&>svg]:h-4 [&>svg]:w-4">{candidate.icon}</span>
                    {candidate.label}
                  </button>
                ))}
              </div>
            );
          })}
          {query.trim() !== "" && SECTIONS.every((candidate) => !sectionMatches(candidate, query)) && (
            <p className="px-3 py-2 text-sm text-neutral-700">No settings match “{query.trim()}”.</p>
          )}
        </div>
      </div>

      <div className={`app-canvas scrollbar-slim min-h-0 min-w-0 flex-1 overflow-y-auto shadow-[inset_1px_0_0_hsl(var(--neutral-200))] ${opened ? "" : "hidden"} md:block`}>
        <div className="mx-auto flex max-w-[860px] flex-col gap-6 px-5 pb-11 pt-6 sm:px-7 sm:pr-14 lg:max-w-[960px] lg:px-10 lg:pt-8">
          <header className="flex flex-col gap-1.5 pb-1">
            <h1 className="text-xl font-semibold tracking-tight">{section.title}</h1>
            <p className="max-w-2xl text-sm leading-6 text-neutral-700">{section.description}</p>
          </header>
          {active === "personal" && <PersonalSettingsCard onSessionExpired={onSessionExpired} />}
          {active === "models" && (
            <>
              <ProvidersCard onSessionExpired={onSessionExpired} />
              <ModelsCard onSessionExpired={onSessionExpired} />
              <p className="text-sm text-muted-foreground">New model choices appear in chat after restart. The bar at the top of the panel restarts Eggy.</p>
            </>
          )}
          {active === "connections" && (
            <>
              <DiscordCard onSessionExpired={onSessionExpired} />
            </>
          )}
          {active === "capabilities" && (
            <>
              <ToolsCard onSessionExpired={onSessionExpired} />
              <FinanceCard onSessionExpired={onSessionExpired} running={features.includes("finance")} />
              <MarketplaceCard onSessionExpired={onSessionExpired} running={features.includes("marketplace")} />
              <AgentRunnerCard onSessionExpired={onSessionExpired} running={features.includes("agent_runner")} />
            </>
          )}
          {active === "soul" && <SoulCard onSessionExpired={onSessionExpired} />}
          {active === "automation" && (
            <>
              <WatchCard onSessionExpired={onSessionExpired} />
              <HeartbeatCard onSessionExpired={onSessionExpired} />
            </>
          )}
          {active === "permissions" && <ApprovalsCard onSessionExpired={onSessionExpired} />}
          {active === "accounts" && <AccountsCard onSessionExpired={onSessionExpired} />}
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

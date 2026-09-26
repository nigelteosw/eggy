export type DocNavItem = {
  title: string;
  path: `/${string}` | "/";
  description: string;
};

export type DocNavGroup = {
  label: string;
  items: readonly DocNavItem[];
};

export const navigation: readonly DocNavGroup[] = [
  {
    label: "Get started",
    items: [
      { title: "Introduction", path: "/", description: "Meet Eggy and understand its core workflow." },
      { title: "Quickstart", path: "/get-started/quickstart", description: "Run a local Eggy instance." },
      { title: "Deploy on Railway", path: "/get-started/deploy-railway", description: "Deploy Eggy with durable storage." },
    ],
  },
  {
    label: "Use Eggy",
    items: [
      { title: "Web chat and settings", path: "/use/web-chat", description: "Chat, traces, and the runtime settings panel in one authenticated UI." },
      { title: "Telegram", path: "/use/telegram", description: "Use Eggy's direct Telegram commands and selections." },
      { title: "Models and reasoning effort", path: "/use/models", description: "Select configured model aliases and browse what a provider serves." },
      { title: "Long turns and steering", path: "/use/long-turns", description: "Correct a running turn, stop it, and see what a long turn keeps." },
      { title: "Skills", path: "/use/skills", description: "Teach Eggy a procedure once with a Markdown file." },
      { title: "Tool catalog", path: "/use/tools", description: "Every tool Eggy can call and whether it asks first." },
      { title: "Reading traces", path: "/use/traces", description: "Inspect a turn as it actually ran." },
      { title: "Approvals and protected actions", path: "/use/approvals", description: "Understand payload-bound approvals and what they authorize." },
    ],
  },
  {
    label: "Configure",
    items: [
      { title: "Configuration overview", path: "/configure/configuration", description: "Configure the daemon without storing secrets in YAML." },
      { title: "Accounts", path: "/configure/accounts", description: "Several people, each with their own Google sign-in, sharing one Eggy." },
      { title: "Model providers", path: "/configure/model-providers", description: "Connect OpenAI-compatible model providers." },
      { title: "MCP servers", path: "/configure/mcp-servers", description: "Connect trusted HTTP or stdio MCP servers." },
      { title: "Schedules and heartbeat", path: "/configure/automation", description: "Run turns you are not present for, paced by a watch list." },
      { title: "Google Workspace", path: "/configure/google-workspace", description: "Gmail, Calendar, Drive, Docs, Sheets and Contacts through one grant." },
      { title: "Web search", path: "/configure/web-search", description: "Search the open web and read pages with Tavily." },
      { title: "Discord", path: "/configure/discord", description: "Talk to Eggy in a private Discord DM, with the bot added from the panel." },
      { title: "Repository inspection", path: "/configure/repositories", description: "Configure trusted read-only repository access." },
    ],
  },
  {
    label: "Operate",
    items: [
      { title: "Persistence and memory", path: "/operate/persistence-memory", description: "Understand Eggy's files, SQLite database, and volume." },
      { title: "Health checks", path: "/operate/health-checks", description: "Monitor liveness and readiness." },
      { title: "Safe mode", path: "/operate/safe-mode", description: "Repair a config that will not load from the browser." },
      { title: "Security model", path: "/operate/security", description: "Review trust boundaries and owner controls." },
      { title: "Troubleshooting", path: "/operate/troubleshooting", description: "Diagnose common startup and delivery failures." },
    ],
  },
  {
    label: "Project",
    items: [
      { title: "Architecture", path: "/project/architecture", description: "Understand the ports-and-adapters modular monolith." },
      { title: "Adding an adapter", path: "/project/adding-adapter", description: "Extend Eggy without changing its provider-neutral core." },
      { title: "Local development", path: "/project/local-development", description: "Set up and run the development environment." },
      { title: "Testing and releases", path: "/project/testing-releases", description: "Run required verification and build artifacts." },
    ],
  },
] as const;

export const flatNavigation: readonly DocNavItem[] = navigation.flatMap(
  (group) => group.items,
);

export function findNavItem(pathname: string): DocNavItem | undefined {
  const normalized =
    pathname === "/" ? "/" : (`/${pathname.replace(/^\/|\/$/g, "")}` as const);
  return flatNavigation.find((item) => item.path === normalized);
}

export function getAdjacentItems(pathname: string): {
  previous?: DocNavItem;
  next?: DocNavItem;
} {
  const current = findNavItem(pathname);
  const index = current ? flatNavigation.indexOf(current) : -1;
  if (index < 0) return {};
  return {
    previous: index > 0 ? flatNavigation[index - 1] : undefined,
    next: index < flatNavigation.length - 1 ? flatNavigation[index + 1] : undefined,
  };
}

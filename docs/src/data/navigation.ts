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
      { title: "Web chat", path: "/use/web-chat", description: "Chat with Eggy in the authenticated web UI." },
      { title: "Telegram", path: "/use/telegram", description: "Use Eggy's direct Telegram commands and selections." },
      { title: "Models and reasoning effort", path: "/use/models", description: "Select configured model aliases." },
      { title: "Approvals and protected actions", path: "/use/approvals", description: "Understand payload-bound approvals and what they authorize." },
    ],
  },
  {
    label: "Configure",
    items: [
      { title: "Configuration overview", path: "/configure/configuration", description: "Configure the daemon without storing secrets in YAML." },
      { title: "Model providers", path: "/configure/model-providers", description: "Connect OpenAI-compatible model providers." },
      { title: "MCP servers", path: "/configure/mcp-servers", description: "Connect trusted HTTP or stdio MCP servers." },
      { title: "Repository inspection", path: "/configure/repositories", description: "Configure trusted read-only repository access." },
    ],
  },
  {
    label: "Operate",
    items: [
      { title: "Persistence and memory", path: "/operate/persistence-memory", description: "Understand Eggy's files, SQLite database, and volume." },
      { title: "Health checks", path: "/operate/health-checks", description: "Monitor liveness and readiness." },
      { title: "Security model", path: "/operate/security", description: "Review trust boundaries and owner controls." },
      { title: "Troubleshooting", path: "/operate/troubleshooting", description: "Diagnose common startup and delivery failures." },
    ],
  },
  {
    label: "Project",
    items: [
      { title: "Architecture", path: "/project/architecture", description: "Understand the ports-and-adapters modular monolith." },
      { title: "Adding an adapter", path: "/project/adding-adapter", description: "Extend Eggy without changing its provider-neutral kernel." },
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

export type View = "chat" | "config" | "traces" | "finance";

const VIEW_PATHS: Record<View, string> = {
  chat: "/",
  config: "/settings",
  traces: "/traces",
  finance: "/finance",
};

export function pathForView(view: View): string {
  return VIEW_PATHS[view];
}

export function viewForPath(pathname: string): View {
  const path = pathname.replace(/\/+$/, "") || "/";
  if (path === "/settings") return "config";
  if (path === "/traces") return "traces";
  if (path === "/finance") return "finance";
  return "chat";
}

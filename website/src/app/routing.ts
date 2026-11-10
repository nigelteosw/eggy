// Finance is not a view: it is an extension that fills the work area from the rail.
export type View = "home" | "chat" | "scheduled" | "customize" | "traces" | "settings";

export const VIEW_PATHS: Record<View, string> = {
  home: "/home",
  chat: "/",
  scheduled: "/scheduled",
  customize: "/customize",
  traces: "/traces",
  settings: "/settings",
};

export function pathForView(view: View): string {
  return VIEW_PATHS[view];
}

export function viewForPath(pathname: string): View {
  const path = pathname.replace(/\/+$/, "") || "/";
  const match = (Object.entries(VIEW_PATHS) as [View, string][]).find(([, candidate]) => candidate === path);
  return match ? match[0] : "chat";
}

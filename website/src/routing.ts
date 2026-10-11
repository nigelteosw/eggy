// Settings is not a view: it opens as a popup over whatever is on screen, so
// /settings is a path that opens the popup on the chat behind it. Finance is
// not a view either; it is an extension that fills the work area from the rail.
export type View = "chat" | "scheduled" | "traces";

export const SETTINGS_PATH = "/settings";

const VIEW_PATHS: Record<View, string> = {
  chat: "/",
  scheduled: "/scheduled",
  traces: "/traces",
};

export function pathForView(view: View): string {
  return VIEW_PATHS[view];
}

export function isSettingsPath(pathname: string): boolean {
  return (pathname.replace(/\/+$/, "") || "/") === SETTINGS_PATH;
}

export function viewForPath(pathname: string): View {
  const path = pathname.replace(/\/+$/, "") || "/";
  if (path === "/traces") return "traces";
  if (path === "/scheduled") return "scheduled";
  return "chat";
}

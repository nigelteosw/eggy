// Finance is not a view: it opens as a dialog from the sidebar, so a bookmarked
// /finance lands on chat rather than on a page that no longer exists.
export type View = "chat" | "scheduled" | "config" | "traces";

const VIEW_PATHS: Record<View, string> = {
  chat: "/",
  scheduled: "/scheduled",
  config: "/settings",
  traces: "/traces",
};

export function pathForView(view: View): string {
  return VIEW_PATHS[view];
}

export function viewForPath(pathname: string): View {
  const path = pathname.replace(/\/+$/, "") || "/";
  if (path === "/settings") return "config";
  if (path === "/traces") return "traces";
  if (path === "/scheduled") return "scheduled";
  return "chat";
}

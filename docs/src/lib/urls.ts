const base = import.meta.env.BASE_URL.replace(/\/$/, "");

export function docUrl(pathname: string): string {
  const normalized =
    pathname === "/" ? "/" : `/${pathname.replace(/^\/|\/$/g, "")}/`;
  return `${base}${normalized}`;
}

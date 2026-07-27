import { useCallback, useEffect, useMemo, useState } from "react";
import {
  FileAccess,
  HomeFile,
  SessionExpiredError,
  listFiles,
  readFile,
  writeFile,
} from "./api";
import { Button } from "./components/ui/button";

// FilesPage is the raw view of Eggy's home directory: pick a file on the
// left, edit its actual text on the right. It deliberately shows the file as
// it is on disk -- no form, no schema -- because the point is that
// config.yaml and SOUL.md are files an owner owns.

const ACCESS_LABEL: Record<FileAccess, string> = {
  edit: "editable",
  read: "read-only",
  secret: "on host only",
};

// Files are listed under the directory they live in, with the top-level ones
// gathered together first.
function groupOf(path: string): string {
  const slash = path.indexOf("/");
  return slash === -1 ? "home" : path.slice(0, slash);
}

function fileName(path: string): string {
  const slash = path.lastIndexOf("/");
  return slash === -1 ? path : path.slice(slash + 1);
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function FilesPage({ onSessionExpired }: { onSessionExpired: () => void }) {
  const [root, setRoot] = useState("");
  const [files, setFiles] = useState<HomeFile[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [content, setContent] = useState("");
  // Kept alongside the editable copy so the Save button can stay disabled
  // until something actually changed.
  const [saved, setSaved] = useState("");
  const [access, setAccess] = useState<FileAccess>("edit");
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const handleFailure = useCallback(
    (err: unknown, fallback: string) => {
      if (err instanceof SessionExpiredError) {
        onSessionExpired();
        return;
      }
      setError(err instanceof Error ? err.message : fallback);
    },
    [onSessionExpired],
  );

  useEffect(() => {
    listFiles()
      .then((listing) => {
        setRoot(listing.root);
        setFiles(listing.files);
        setSelected((current) => current ?? listing.files.find((file) => file.access === "edit")?.path ?? null);
      })
      .catch((err) => handleFailure(err, "Failed to list files"));
  }, [handleFailure]);

  useEffect(() => {
    if (!selected) return;
    const entry = files.find((file) => file.path === selected);
    if (entry?.access === "secret") {
      setContent("");
      setSaved("");
      setAccess("secret");
      return;
    }
    setLoading(true);
    setError(null);
    setNotice(null);
    readFile(selected)
      .then((file) => {
        setContent(file.content);
        setSaved(file.content);
        setAccess(file.access);
      })
      .catch((err) => handleFailure(err, "Failed to read file"))
      .finally(() => setLoading(false));
  }, [selected, files, handleFailure]);

  const grouped = useMemo(() => {
    const groups = new Map<string, HomeFile[]>();
    for (const file of files) {
      const group = groupOf(file.path);
      groups.set(group, [...(groups.get(group) ?? []), file]);
    }
    return [...groups.entries()];
  }, [files]);

  const entry = files.find((file) => file.path === selected);
  const dirty = content !== saved;

  async function handleSave() {
    if (!selected) return;
    setSaving(true);
    setError(null);
    setNotice(null);
    try {
      const result = await writeFile(selected, content);
      setSaved(content);
      setNotice([result.title, result.detail].filter(Boolean).join(" "));
      const listing = await listFiles();
      setFiles(listing.files);
    } catch (err) {
      handleFailure(err, "Failed to save file");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="pt-6">
      <div className="mx-auto flex max-w-5xl flex-col gap-6">
        <header className="flex flex-col gap-1 pb-2">
          <h1 className="text-2xl font-semibold tracking-tight">Files</h1>
          <p className="text-sm text-muted-foreground">
            Eggy's home directory{root ? ` at ${root}` : ""}. Edits are written straight to disk.
          </p>
        </header>

        <div className="grid grid-cols-1 gap-6 md:grid-cols-[16rem_minmax(0,1fr)]">
          <nav className="flex flex-col gap-4" aria-label="Home directory">
            {grouped.map(([group, entries]) => (
              <div key={group} className="flex flex-col gap-1">
                <span className="px-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  {group === "home" ? "home" : `${group}/`}
                </span>
                {entries.map((file) => (
                  <button
                    key={file.path}
                    type="button"
                    onClick={() => setSelected(file.path)}
                    className={`flex flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left text-sm transition-colors ${
                      file.path === selected ? "bg-muted text-foreground" : "text-muted-foreground hover:bg-muted/60"
                    }`}
                  >
                    <span className="font-medium">{fileName(file.path)}</span>
                    <span className="text-xs text-muted-foreground">
                      {file.missing ? "not created yet" : formatSize(file.size)}
                      {file.access !== "edit" ? ` · ${ACCESS_LABEL[file.access]}` : ""}
                    </span>
                  </button>
                ))}
              </div>
            ))}
          </nav>

          <section className="flex min-w-0 flex-col gap-3">
            {selected && (
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <div className="flex flex-col gap-0.5">
                  <h2 className="font-mono text-sm font-medium">{selected}</h2>
                  {entry?.note && <p className="text-xs text-muted-foreground">{entry.note}</p>}
                </div>
                {access === "edit" && (
                  <Button type="button" onClick={handleSave} disabled={saving || loading || !dirty}>
                    {saving ? "Saving..." : dirty ? "Save" : "Saved"}
                  </Button>
                )}
              </div>
            )}

            {access === "secret" ? (
              <p className="rounded-md border border-border bg-muted/40 px-3 py-8 text-center text-sm text-muted-foreground">
                This file holds live credentials, so its contents are never sent to the browser. Edit it on the
                host.
              </p>
            ) : (
              <textarea
                value={content}
                onChange={(event) => setContent(event.target.value)}
                readOnly={access !== "edit"}
                spellCheck={false}
                aria-label={selected ?? "file contents"}
                className="h-[60vh] w-full resize-y rounded-md border border-border bg-card px-3 py-2 font-mono text-xs leading-relaxed text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 read-only:text-muted-foreground"
                placeholder={loading ? "Loading..." : "This file is empty."}
              />
            )}

            {notice && (
              <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="status">
                {notice}
              </p>
            )}
            {error && (
              <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
                {error}
              </p>
            )}
          </section>
        </div>
      </div>
    </div>
  );
}

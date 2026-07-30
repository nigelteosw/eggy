type SearchItem = {
  title: string;
  description: string;
  path: string;
  headings: string[];
  text: string;
};

const dialog = document.querySelector<HTMLDialogElement>("[data-search-dialog]");
const searchInput = document.querySelector<HTMLInputElement>("[data-search-input]");
const searchResults = document.querySelector<HTMLElement>("[data-search-results]");
let searchItems: SearchItem[] | null = null;

async function openSearch() {
  if (!dialog) return;
  dialog.showModal();
  searchInput?.focus();
  if (!searchItems) {
    const endpoint = dialog.dataset.searchIndex;
    if (endpoint) {
      const response = await fetch(endpoint);
      searchItems = response.ok ? await response.json() : [];
      if (searchInput?.value.trim()) {
        searchInput.dispatchEvent(new Event("input"));
      }
    }
  }
}

function closeSearch() {
  dialog?.close();
}

document.querySelectorAll("[data-search-open]").forEach((button) => {
  button.addEventListener("click", openSearch);
});
document.querySelectorAll("[data-search-close]").forEach((button) => {
  button.addEventListener("click", closeSearch);
});
dialog?.addEventListener("click", (event) => {
  if (event.target === dialog) closeSearch();
});
document.addEventListener("keydown", (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
    event.preventDefault();
    void openSearch();
  }
});

searchInput?.addEventListener("input", () => {
  if (!searchResults) return;
  const query = searchInput.value.trim().toLowerCase();
  if (!query) {
    searchResults.innerHTML =
      '<div class="search-empty"><span class="search-empty-mark" aria-hidden="true"></span><p>Search setup, configuration, security, and development guides.</p></div>';
    return;
  }

  const terms = query.split(/\s+/);
  const matches = (searchItems ?? [])
    .map((item) => {
      const title = item.title.toLowerCase();
      const haystack = `${item.title} ${item.description} ${item.headings.join(" ")} ${item.text}`.toLowerCase();
      const matchesAll = terms.every((term) => haystack.includes(term));
      const score = title.includes(query) ? 2 : matchesAll ? 1 : 0;
      return { item, score };
    })
    .filter(({ score }) => score > 0)
    .sort((a, b) => b.score - a.score)
    .slice(0, 8);

  if (!matches.length) {
    searchResults.innerHTML = `<p class="no-results">No pages found for “${escapeHTML(query)}”.</p>`;
    return;
  }

  searchResults.innerHTML = matches
    .map(
      ({ item }) => `
        <a class="search-result" href="${item.path}">
          <strong>${escapeHTML(item.title)}</strong>
          <span>${escapeHTML(item.description)}</span>
        </a>`,
    )
    .join("");
});

function escapeHTML(value: string): string {
  const element = document.createElement("span");
  element.textContent = value;
  return element.innerHTML;
}

const drawerBackdrop = document.querySelector<HTMLElement>("[data-drawer-backdrop]");
const drawer = document.querySelector<HTMLElement>("[data-drawer]");
const drawerTrigger = document.querySelector<HTMLButtonElement>("[data-drawer-open]");
const drawerClose = document.querySelector<HTMLButtonElement>("[data-drawer-close]");

function openDrawer() {
  if (!drawerBackdrop) return;
  drawerBackdrop.hidden = false;
  requestAnimationFrame(() => drawerBackdrop.classList.add("open"));
  drawerTrigger?.setAttribute("aria-expanded", "true");
  document.body.classList.add("drawer-open");
  drawerClose?.focus();
}

function closeDrawer() {
  if (!drawerBackdrop) return;
  drawerBackdrop.classList.remove("open");
  drawerTrigger?.setAttribute("aria-expanded", "false");
  document.body.classList.remove("drawer-open");
  window.setTimeout(() => {
    drawerBackdrop.hidden = true;
  }, 160);
  drawerTrigger?.focus();
}

drawerTrigger?.addEventListener("click", openDrawer);
drawerClose?.addEventListener("click", closeDrawer);
drawerBackdrop?.addEventListener("click", (event) => {
  if (event.target === drawerBackdrop) closeDrawer();
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && drawerBackdrop?.classList.contains("open")) {
    closeDrawer();
  }
  if (event.key === "Tab" && drawerBackdrop?.classList.contains("open") && drawer) {
    const focusable = [...drawer.querySelectorAll<HTMLElement>("a, button")];
    if (!focusable.length) return;
    const first = focusable[0];
    const last = focusable.at(-1)!;
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
});

document.querySelectorAll("pre").forEach((block) => {
  const code = block.querySelector("code");
  if (!code) return;
  const button = document.createElement("button");
  button.type = "button";
  button.className = "copy-code";
  button.setAttribute("aria-label", "Copy code");
  button.textContent = "Copy";
  button.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(code.textContent ?? "");
      button.textContent = "Copied";
    } catch {
      button.textContent = "Copy failed";
    }
    window.setTimeout(() => {
      button.textContent = "Copy";
    }, 1500);
  });
  block.append(button);
});

const outlineLinks = new Map(
  [...document.querySelectorAll<HTMLAnchorElement>("[data-outline-link]")].map(
    (link) => [link.dataset.outlineLink!, link],
  ),
);
if ("IntersectionObserver" in window && outlineLinks.size) {
  const observer = new IntersectionObserver(
    (entries) => {
      const visible = entries.find((entry) => entry.isIntersecting);
      if (!visible) return;
      outlineLinks.forEach((link) => link.classList.remove("active"));
      outlineLinks.get(visible.target.id)?.classList.add("active");
    },
    { rootMargin: "-18% 0px -70% 0px" },
  );
  document.querySelectorAll<HTMLElement>("h2[id], h3[id]").forEach((heading) => observer.observe(heading));
}

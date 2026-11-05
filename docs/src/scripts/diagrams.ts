// Turns ```mermaid fences into SVG diagrams. Mermaid is imported only when a page
// has a diagram, so pages without one never download it. A diagram that fails to
// render keeps its source block, so the page still reads without JavaScript.

type Diagram = { source: string; figure: HTMLElement };

const blocks = [
  ...document.querySelectorAll<HTMLPreElement>('pre[data-language="mermaid"]'),
];

if (blocks.length) {
  void renderDiagrams(blocks);
}

async function renderDiagrams(sources: HTMLPreElement[]) {
  const { default: mermaid } = await import("mermaid");
  const diagrams: Diagram[] = [];
  let renders = 0;

  const configure = () =>
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: document.documentElement.dataset.theme === "light" ? "default" : "dark",
    });

  const draw = async ({ source, figure }: Diagram) => {
    try {
      const { svg } = await mermaid.render(`eggy-diagram-${++renders}`, source);
      figure.innerHTML = svg;
    } catch (error) {
      console.error("Mermaid diagram failed to render", error);
    }
  };

  configure();
  for (const pre of sources) {
    const source = pre.querySelector("code")?.textContent ?? "";
    if (!source.trim()) continue;
    const figure = document.createElement("figure");
    figure.className = "diagram";
    const diagram = { source, figure };
    try {
      const { svg } = await mermaid.render(`eggy-diagram-${++renders}`, source);
      figure.innerHTML = svg;
      pre.replaceWith(figure);
      diagrams.push(diagram);
    } catch (error) {
      console.error("Mermaid diagram failed to render", error);
    }
  }

  // Mermaid bakes colours into the SVG, so a theme switch redraws every diagram.
  new MutationObserver(async () => {
    configure();
    await Promise.all(diagrams.map(draw));
  }).observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
}

import { escape } from "./format.js";

export function inlineMarkdown(value) {
  return value
    .replace(
      /\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g,
      '<a href="$2" target="_blank" rel="noreferrer">$1</a>',
    )
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/__([^_]+)__/g, "<strong>$1</strong>")
    .replace(/\*([^*]+)\*/g, "<em>$1</em>");
}

export function markdown(value) {
  const blocks = [];
  let text = escape(value).replace(
    /```(?:[\w+-]+)?\n?([\s\S]*?)```/g,
    (_, code) => {
      const id = blocks.length;
      blocks.push(`<pre><code>${code.trimEnd()}</code></pre>`);
      return `\u0000CODE${id}\u0000`;
    },
  );
  text = text.replace(
    /(^\|.*\|\r?\n^\|(?:\s*:?-+:?\s*\|)+\r?\n(?:^\|.*\|(?:\r?\n|$))+)/gm,
    (table) => {
      const rows = table
        .trim()
        .split(/\r?\n/)
        .map((row) =>
          row
            .slice(1, -1)
            .split("|")
            .map((cell) => cell.trim()),
        );
      const headers = rows[0];
      const body = rows.slice(2);
      const html = `<div class="table-wrap"><table><thead><tr>${headers.map((cell) => `<th>${inlineMarkdown(cell)}</th>`).join("")}</tr></thead><tbody>${body.map((row) => `<tr>${row.map((cell) => `<td>${inlineMarkdown(cell)}</td>`).join("")}</tr>`).join("")}</tbody></table></div>`;
      const id = blocks.length;
      blocks.push(html);
      return `\u0000CODE${id}\u0000`;
    },
  );
  const output = [];
  let paragraph = [];
  let list = null;
  const flushParagraph = () => {
    if (paragraph.length) {
      output.push(`<p>${paragraph.map(inlineMarkdown).join("<br>")}</p>`);
      paragraph = [];
    }
  };
  const closeList = () => {
    if (list) {
      output.push(`</${list}>`);
      list = null;
    }
  };
  for (const line of text.split(/\r?\n/)) {
    const code = line.match(/^\u0000CODE(\d+)\u0000$/);
    const heading = line.match(/^(#{1,3})\s+(.+)$/);
    const unordered = line.match(/^\s*[-*]\s+(.+)$/);
    const ordered = line.match(/^\s*\d+\.\s+(.+)$/);
    const quote = line.match(/^\s*>\s?(.+)$/);
    if (!line.trim()) {
      flushParagraph();
      closeList();
      continue;
    }
    if (code) {
      flushParagraph();
      closeList();
      output.push(blocks[Number(code[1])]);
      continue;
    }
    if (heading) {
      flushParagraph();
      closeList();
      const level = Math.min(4, heading[1].length + 1);
      output.push(`<h${level}>${inlineMarkdown(heading[2])}</h${level}>`);
      continue;
    }
    if (unordered || ordered) {
      flushParagraph();
      const next = unordered ? "ul" : "ol";
      if (list !== next) {
        closeList();
        output.push(`<${next}>`);
        list = next;
      }
      output.push(`<li>${inlineMarkdown((unordered || ordered)[1])}</li>`);
      continue;
    }
    if (quote) {
      flushParagraph();
      closeList();
      output.push(`<blockquote>${inlineMarkdown(quote[1])}</blockquote>`);
      continue;
    }
    flushParagraph();
    paragraph.push(line);
  }
  flushParagraph();
  closeList();
  return output.join("");
}

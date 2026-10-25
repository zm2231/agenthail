export function alignTranscriptTop(container) {
  const top = container.getBoundingClientRect().top;
  const crossing = [...container.querySelectorAll("tr, pre, blockquote, li, h2, h3, h4, p")].find((element) => {
    const bounds = element.getBoundingClientRect();
    return bounds.top < top && bounds.bottom > top;
  });
  if (!crossing) return;
  const bounds = crossing.getBoundingClientRect();
  if (crossing.matches("p, li")) {
    const lineHeight = Number.parseFloat(getComputedStyle(crossing).lineHeight);
    if (Number.isFinite(lineHeight) && lineHeight > 0) {
      const lines = Math.floor((top - bounds.top) / lineHeight);
      container.scrollTop += bounds.top + lines * lineHeight - top;
      return;
    }
  }
  container.scrollTop += bounds.top - top;
}

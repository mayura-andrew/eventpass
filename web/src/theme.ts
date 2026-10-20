// SPDX-License-Identifier: Apache-2.0

const HEX = /^#[0-9a-f]{6}$/i;

function luminance(hex: string): number {
  const ch = (i: number) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * ch(1) + 0.7152 * ch(3) + 0.0722 * ch(5);
}

/** WCAG 2.1 contrast ratio, the same formula the server uses. */
export function contrastRatio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

/** Text color for a background: white unless black reads better. */
export function inkFor(bg: string): string {
  return contrastRatio(bg, "#ffffff") >= contrastRatio(bg, "#000000") ? "#ffffff" : "#000000";
}

/**
 * Applies workspace colors as CSS custom properties. Setting properties
 * through the CSSOM is allowed by the strict CSP (no inline style tags).
 */
export function applyTheme(settings: Record<string, string> | undefined): void {
  const root = document.documentElement.style;
  const primary = settings?.["primary_color"];
  const accent = settings?.["accent_color"];
  if (primary && HEX.test(primary)) {
    root.setProperty("--primary", primary);
    root.setProperty("--primary-ink", inkFor(primary));
  }
  if (accent && HEX.test(accent)) root.setProperty("--accent", accent);
}

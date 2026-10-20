// SPDX-License-Identifier: Apache-2.0
import { applyTheme, contrastRatio, inkFor } from "./theme";

describe("theme", () => {
  it("computes WCAG contrast like the server", () => {
    expect(contrastRatio("#000000", "#ffffff")).toBeCloseTo(21, 1);
    expect(contrastRatio("#1B4DDB", "#ffffff")).toBeGreaterThan(4.5);
    expect(contrastRatio("#FFE066", "#ffffff")).toBeLessThan(4.5);
  });

  it("picks readable text for a background", () => {
    expect(inkFor("#1B4DDB")).toBe("#ffffff");
    expect(inkFor("#FFE066")).toBe("#000000");
  });

  it("applies valid colors as CSS variables and ignores invalid ones", () => {
    applyTheme({ primary_color: "#FFE066", accent_color: "red" });
    const style = document.documentElement.style;
    expect(style.getPropertyValue("--primary")).toBe("#FFE066");
    expect(style.getPropertyValue("--primary-ink")).toBe("#000000");
    expect(style.getPropertyValue("--accent")).toBe("");
  });
});

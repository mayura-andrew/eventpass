// SPDX-License-Identifier: Apache-2.0
import js from "@eslint/js";
import jsxA11y from "eslint-plugin-jsx-a11y";
import reactHooks from "eslint-plugin-react-hooks";
import globals from "globals";
import tseslint from "typescript-eslint";

export default tseslint.config(
  { ignores: ["dist", "node_modules", "test-results", "playwright-report", "src/api/schema.ts"] },
  js.configs.recommended,
  ...tseslint.configs.strict,
  jsxA11y.flatConfigs.strict,
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: { globals: { ...globals.browser } },
    plugins: { "react-hooks": reactHooks },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // `any` needs a disable comment explaining why.
      "@typescript-eslint/no-explicit-any": "error",
    },
  },
);

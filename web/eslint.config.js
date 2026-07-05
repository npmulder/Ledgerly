import js from "@eslint/js";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

const hardCodedHexColorPattern =
  /#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})\b/g;

const designTokensPlugin = {
  rules: {
    "no-hardcoded-hex-colors": {
      meta: {
        type: "problem",
        docs: {
          description:
            "Disallow hard-coded hex colors outside src/styles/tokens.css.",
        },
        schema: [],
        messages: {
          hardCodedHex:
            "Use a design token instead of hard-coded hex color '{{ color }}'.",
        },
      },
      create(context) {
        return {
          Program() {
            const sourceCode = context.sourceCode;
            const sourceText = sourceCode.text;

            hardCodedHexColorPattern.lastIndex = 0;

            for (const match of sourceText.matchAll(hardCodedHexColorPattern)) {
              const color = match[0];
              const startIndex = match.index ?? 0;

              context.report({
                loc: sourceCode.getLocFromIndex(startIndex),
                messageId: "hardCodedHex",
                data: { color },
              });
            }
          },
        };
      },
    },
  },
};

export default tseslint.config(
  {
    ignores: ["dist", "coverage"],
  },
  js.configs.recommended,
  ...tseslint.configs.strict,
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2024,
      sourceType: "module",
      globals: {
        ...globals.browser,
        ...globals.es2024,
      },
    },
    plugins: {
      "design-tokens": designTokensPlugin,
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "design-tokens/no-hardcoded-hex-colors": "error",
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],
    },
  },
  {
    files: ["*.config.{js,ts}", "eslint.config.js"],
    languageOptions: {
      globals: {
        ...globals.node,
      },
    },
  },
);

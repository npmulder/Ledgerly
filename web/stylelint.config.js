export default {
  rules: {
    "color-no-hex": true,
  },
  overrides: [
    {
      files: ["src/styles/tokens.css"],
      rules: {
        "color-no-hex": null,
      },
    },
  ],
};

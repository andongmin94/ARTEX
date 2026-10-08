const { defineConfig } = require("@playwright/test");
module.exports = defineConfig({ ...require("./playwright.config.cjs"), testDir: "./package-tests", outputDir: "./test-results-package" });

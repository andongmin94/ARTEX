const { defineConfig } = require("@playwright/test");
module.exports = defineConfig({
  testDir: "./tests",
  timeout: 90_000,
  workers: 1,
  reporter: "list",
  use: { trace: "retain-on-failure" },
});

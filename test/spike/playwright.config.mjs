export default {
  testDir: ".",
  testMatch: "terminal.spec.mjs",
  timeout: 45_000,
  use: {
    browserName: "chromium",
    headless: true
  }
};

const { defineConfig } = require('@playwright/test');

module.exports = defineConfig({
  testDir: './tests',
  timeout: 30000,
  use: {
    baseURL: process.env.BASE_URL || 'http://host.docker.internal:8180',
    screenshot: 'only-on-failure',
  },
  reporter: [['list']],
});

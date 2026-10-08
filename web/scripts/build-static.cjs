const { spawnSync } = require("node:child_process");

// 셸별 환경변수 문법 없이 Windows와 macOS/Linux에서 동일하게 빌드한다.
const result = spawnSync(process.execPath, [require.resolve("next/dist/bin/next"), "build"], {
  cwd: require("node:path").resolve(__dirname, ".."),
  env: { ...process.env, NEXT_EXPORT: "1" },
  stdio: "inherit",
});

if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}
process.exit(result.status ?? 1);

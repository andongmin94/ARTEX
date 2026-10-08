async function refresh() {
  const status = await window.artexDesktop.status();
  document.getElementById("message").textContent = status.message ?? "준비 완료";
  document.getElementById("diagnostics").hidden = !status.details;
  document.getElementById("details").textContent = status.details ?? "";
  document.getElementById("retry").disabled = status.state !== "failed";
}
document.getElementById("retry").addEventListener("click", async () => {
  document.getElementById("retry").disabled = true;
  await window.artexDesktop.retry();
  await refresh();
});
document.getElementById("quit").addEventListener("click", () => window.artexDesktop.quit());
void refresh();
setInterval(() => void refresh(), 500);

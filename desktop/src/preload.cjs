const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("artexDesktop", {
  status: () => ipcRenderer.invoke("desktop:status"),
  retry: () => ipcRenderer.invoke("desktop:retry"),
  quit: () => ipcRenderer.invoke("desktop:quit"),
  backupStatus: () => ipcRenderer.invoke("desktop:backup-status"),
  setAutomaticBackup: (value) => ipcRenderer.invoke("desktop:backup-automatic", value),
  createBackup: () => ipcRenderer.invoke("desktop:backup-create"),
  restoreBackup: () => ipcRenderer.invoke("desktop:backup-restore"),
  openRestoredHome: () => ipcRenderer.invoke("desktop:backup-open-restored"),
  updateStatus: () => ipcRenderer.invoke("desktop:update-status"),
  checkUpdate: () => ipcRenderer.invoke("desktop:update-check"),
  downloadUpdate: () => ipcRenderer.invoke("desktop:update-download"),
  installUpdate: () => ipcRenderer.invoke("desktop:update-install"),
  openChatGPTLogin: (url) => ipcRenderer.invoke("desktop:chatgpt-login", url),
  openChatGPTUsage: () => ipcRenderer.invoke("desktop:chatgpt-usage"),
});

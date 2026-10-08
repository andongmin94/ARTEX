const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("artexDesktop", {
  status: () => ipcRenderer.invoke("desktop:status"),
  retry: () => ipcRenderer.invoke("desktop:retry"),
  quit: () => ipcRenderer.invoke("desktop:quit"),
});
